package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	volumesnapshotv1beta1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	volumeSnapshotAPIVersion = "snapshot.storage.k8s.io/v1beta1"
)

// EnlargePVC increases Kubernetes Persistent Volume size by the specified
// percentage.
func EnlargePVC(pvc string, namespace string, percents *int64, createSnapshot, dryRun, waitForModifying *bool) error {
	// Create k8s in-cluster config.
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	// Initialize k8s clientset.
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	c := *clientset

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Get PVC metadata.
	pvcMetadata, err := c.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvc, v1.GetOptions{})
	if err != nil {
		return err
	}

	log.Debugln("PVC metadata:", pvcMetadata)

	if *createSnapshot {
		log.Infoln("Creating snapshot for the volume...")

		// Description of the VolumeSnapshot object.
		snapshot := volumesnapshotv1beta1.VolumeSnapshot{
			TypeMeta: v1.TypeMeta{
				Kind:       "VolumeSnapshot",
				APIVersion: volumeSnapshotAPIVersion,
			},
			ObjectMeta: v1.ObjectMeta{
				GenerateName: pvc + "-",
				Namespace:    namespace,
			},
			Spec: volumesnapshotv1beta1.VolumeSnapshotSpec{
				Source: volumesnapshotv1beta1.VolumeSnapshotSource{
					PersistentVolumeClaimName: &pvc,
					VolumeSnapshotContentName: k8sSnapshotClass,
				},
				VolumeSnapshotClassName: k8sSnapshotClass,
			},
			Status: &volumesnapshotv1beta1.VolumeSnapshotStatus{},
		}

		snapshotRequestBody, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}

		responseData, err := c.RESTClient().
			Post().
			AbsPath("/apis/" + volumeSnapshotAPIVersion + "/namespaces/" + namespace + "/volumesnapshots").
			Body(snapshotRequestBody).
			DoRaw(ctx)

		if err != nil {
			return err
		}

		// Show created VolumeSnapshot metadata if the level is debug.
		runtimeScheme := runtime.NewScheme()
		_ = volumesnapshotv1beta1.AddToScheme(runtimeScheme)

		decode := serializer.NewCodecFactory(runtimeScheme).UniversalDeserializer().Decode
		obj, _, err := decode(responseData, nil, nil)
		if err != nil {
			return err
		}

		createdVolumeSnapshot := obj.(*volumesnapshotv1beta1.VolumeSnapshot)

		log.Infof("Creation of the snapshot \"%s\" in the namespace \"%s\" completed.", createdVolumeSnapshot.GetName(), namespace)
		log.Debugln("VolumeSnapshot metadata:", createdVolumeSnapshot)
	}

	// EBS Volume size fits GB.
	currentSizeInB, _ := pvcMetadata.Spec.Resources.Requests.Storage().AsInt64()
	currentSizeInGB := currentSizeInB / 1073741824
	log.Infof("Current size of the volume: %d GB", currentSizeInGB)

	newSize := strconv.FormatInt(currentSizeInGB+percentageIncrease(currentSizeInGB, *percents), 10)
	log.Infof("New volume size after the enlargement: %s GB", newSize)

	// Enlarge PVC
	var dryRunOption []string
	if *dryRun {
		dryRunOption = append(dryRunOption, "All")
	}

	patch := []byte(`{"spec":{"resources":{"requests":{"storage":"` + newSize + `Gi"}}}}`)
	patchedPvcMetadata, err := c.CoreV1().PersistentVolumeClaims(namespace).Patch(ctx, pvc, types.MergePatchType, patch, v1.PatchOptions{DryRun: dryRunOption})

	if err == nil && *dryRun {
		return errors.New("DryRunOperation")
	}

	if err != nil {
		return err
	}

	log.Infoln("PVC enlargement started.")
	log.Debugln(patchedPvcMetadata)

	if *waitForModifying {
		log.Infoln("Waiting for the volume enlargement to complete...")
		err = WaitUntilPVCModifyed(ctx, pvc, namespace, c)
		if err != nil {
			return err
		}
		log.Infoln("Enlargement completed.")
	}

	return nil
}

// EnlargeVolumeByID increases the disk size by the specified percentage.
func EnlargeVolumeByID(volumeID *string, percents *int64, createSnapshot, dryRun, waitForModifying *bool) error {
	log.Debugln("Current EBS volume ID:", *volumeID)

	awsEc2Client := ec2.New(session.New())
	ctx, cancel := context.WithTimeout(aws.BackgroundContext(), 15*time.Minute)
	defer cancel()

	if *createSnapshot {
		log.Infoln("Creating snapshot for the volume...")

		snapshotFilter := &ec2.CreateSnapshotInput{
			VolumeId: volumeID,
		}
		snapshot, err := awsEc2Client.CreateSnapshotWithContext(ctx, snapshotFilter)
		if err != nil {
			return err
		}

		log.Infoln("ID of the snapshot to be created:", *snapshot.SnapshotId)

		snapshotInput := &ec2.DescribeSnapshotsInput{
			SnapshotIds: []*string{snapshot.SnapshotId},
		}
		log.Infoln("Waiting for the volume snapshot to complete...")
		err = awsEc2Client.WaitUntilSnapshotCompletedWithContext(ctx, snapshotInput)
		if err != nil {
			return err
		}
		log.Infoln("Snapshot creation completed.")
	}

	volumesFilters := &ec2.DescribeVolumesInput{
		VolumeIds: []*string{volumeID},
	}

	volumeInfo, err := awsEc2Client.DescribeVolumes(volumesFilters)

	if err != nil {
		return err
	}

	log.Debugf("Current size of the EBS volume: %d GB", *volumeInfo.Volumes[0].Size)

	newSize := *volumeInfo.Volumes[0].Size + percentageIncrease(*volumeInfo.Volumes[0].Size, *percents)
	log.Debugf("New EBS volume size after the enlargement: %d GB", newSize)

	modifiedVolume := &ec2.ModifyVolumeInput{
		DryRun:   dryRun,
		Size:     &newSize,
		VolumeId: volumeID,
	}

	_, err = awsEc2Client.ModifyVolume(modifiedVolume)

	if err != nil {
		return err
	}

	log.Infoln("Volume enlargement started.")

	if *waitForModifying {
		log.Infoln("Waiting for the volume enlargement to complete...")
		err = ebsWaitForModifying(ctx, volumeID, awsEc2Client)
		if err != nil {
			return err
		}
		log.Infoln("Enlargement completed.")
	}

	return nil
}

func ebsWaitForModifying(ctx context.Context, volumeID *string, awsEc2Client *ec2.EC2) error {
	volumeModificationsInput := &ec2.DescribeVolumesModificationsInput{
		VolumeIds: []*string{volumeID},
	}

	err := WaitUntilVolumeModifyedWithContext(awsEc2Client, ctx, volumeModificationsInput)
	return err
}

// percentageIncrease calculates the increased size of the aws volume by the specified percentage.
func percentageIncrease(currentPartitionSizeGB int64, percentagesToIncrease int64) (result int64) {
	toPercentages := float64(currentPartitionSizeGB) / 100
	increased := toPercentages * float64(percentagesToIncrease)
	rounded := math.Ceil(increased)

	result = int64(rounded)
	return
}

// WaitUntilVolumeModifyedWithContext uses the Amazon EC2 API operation
// DescribeVolumesModificationsInput to wait for a condition to be met before
// returning. If the condition is not met within the max attempt window, an error
// will be returned.
func WaitUntilVolumeModifyedWithContext(awsEc2Client *ec2.EC2, ctx aws.Context, input *ec2.DescribeVolumesModificationsInput, options ...request.WaiterOption) error {
	waiter := request.Waiter{
		Name:        "WaitUntilVolumeModifyed",
		MaxAttempts: 40,
		Delay:       request.ConstantWaiterDelay(15 * time.Second),
		Acceptors: []request.WaiterAcceptor{
			{
				State:   request.SuccessWaiterState,
				Matcher: request.PathAllWaiterMatch, Argument: "VolumesModifications[].ModificationState",
				Expected: "completed",
			},
			{
				State:   request.FailureWaiterState,
				Matcher: request.PathAnyWaiterMatch, Argument: "VolumesModifications[].ModificationState",
				Expected: "failed",
			},
		},
		Logger: awsEc2Client.Config.Logger,
		NewRequest: func(options []request.Option) (*request.Request, error) {
			var volumesModificationsInput *ec2.DescribeVolumesModificationsInput
			if input != nil {
				tmp := *input
				volumesModificationsInput = &tmp
			}
			req, _ := awsEc2Client.DescribeVolumesModificationsRequest(volumesModificationsInput)
			req.SetContext(ctx)
			req.ApplyOptions(options...)
			return req, nil
		},
	}
	waiter.ApplyOptions(options...)

	return waiter.WaitWithContext(ctx)
}

// WaitUntilPVCModifyed watches PVC events to wait for a condition to be met
// before returning. If the condition is not met within the context timeout,
// an error will be returned.
func WaitUntilPVCModifyed(ctx context.Context, pvc, namespace string, c kubernetes.Clientset) error {
	fieldSelector, err := fields.ParseSelector("metadata.name=" + pvc)
	if err != nil {
		return err
	}

	log.Debugln("Volume selector:", fieldSelector.String())

	watchInterface, err := c.CoreV1().PersistentVolumeClaims(namespace).Watch(ctx, v1.ListOptions{FieldSelector: fieldSelector.String()})

	if err != nil {
		watchInterface.Stop()
		return err
	}

	log.Debugln("Watch interface:", watchInterface)

	// WaitGroup to wait for enlargement completion.
	var pvcWaitGroup sync.WaitGroup
	pvcWaitGroup.Add(1)

	pvcEventChannel := make(chan error, 0)

	// Get current status of the PVC.
	go func(pvcEventChannel chan error) {
		defer watchInterface.Stop()
		defer close(pvcEventChannel)
		defer pvcWaitGroup.Done()

		for event := range watchInterface.ResultChan() {
			log.Debugln("Type of the current event:", event.Type)
			p, ok := event.Object.(*corev1.PersistentVolumeClaim)
			if !ok {
				pvcEventChannel <- errors.New("Unknown PVC event type")
				return
			}
			log.Debugln("PVC status phase:", p.Status.Phase)
			if len(p.Status.Conditions) > 0 {
				log.Infof("Current state of the volume: %v...", p.Status.Conditions[0].Type)
			} else {
				return
			}
		}

		select {
		case <-ctx.Done():
			pvcEventChannel <- ctx.Err()
			return
		}
	}(pvcEventChannel)

	err = <-pvcEventChannel
	if err != nil {
		if err.Error() == context.DeadlineExceeded.Error() {
			return errors.New("ContextTimeout")
		}
		return err
	}

	pvcWaitGroup.Wait()

	return nil
}
