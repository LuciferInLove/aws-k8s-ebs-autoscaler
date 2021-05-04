[![Go Report Card](https://goreportcard.com/badge/github.com/LuciferInLove/aws-k8s-ebs-autoscaler)](https://goreportcard.com/report/github.com/LuciferInLove/aws-k8s-ebs-autoscaler)
![Build status](https://github.com/LuciferInLove/aws-k8s-ebs-autoscaler/workflows/Build/badge.svg)

# aws-k8s-ebs-autoscaler

Enlarges the size of AWS EBS volumes directly or as Kubernetes PVC. It can be used in pair with [alertmanager-webhook-receiver](https://github.com/LuciferInLove/alertmanager-webhook-receiver). You can see a sample of usage in the How it works section below.

NOTE: This is an alpha version, you shouldn't use it in a production environment.
NOTE: Any value of the percents flag causes the size to increase to 1 Gb minimum. For example, if the size of the EBS volume is 5 Gb and the percents is less than 40, the EBS volume size will be increased to 1 Gb.

## Usage

```
Usage of aws-k8s-ebs-autoscaler:
  -dry-run
        If true, only show the result without enlarging the volume. (default false)
  -k8s-snapshot-class string
        The name of the VolumeSnapshotClass resource, which is used to create snapshots in Kubernetes. (default "csi-aws-vsc")
  -log-level string
        Only log messages with the given severity or above. One of: [debug, info, warn, error] (default "info")
  -mount-point string
        Mount point of the volume to be enlarged. (required if pvc isn't set)
  -percents int
        By what percentage to increase. (default 20)
  -proc-path string
        procfs mountpoint. (default "/proc")
  -pvc string
        PVC ID of the volume to be enlarged. (required if mount-point isn't set)
  -pvc-namespace string
        Kubernetes namespace where pvc is located. (required if mount-point isn't set)
  -snapshot
        If true, create a volume snapshot. (default false)
  -sys-path string
        sysfs mountpoint. (default "/sys")
  -wait-for-modifying
        If true, wait for enlarging the volume to be completed. (default false)
```

**aws-k8s-ebs-autoscaler** reads namespace from `/var/run/secrets/kubernetes.io/serviceaccount/namespace` if namespaces aren't defined in command line flags. It also reads [Kubernetes Service Account Token](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#service-account-tokens) from `/var/run/secrets/kubernetes.io/serviceaccount/token`.

## How it works

**aws-k8s-ebs-autoscaler** was created to run mainly as Kubernetes Job in pair with alertmanager-webhook-receiver. alertmanager-webhook-receiver responds to monitoring events and triggers Kubernetes Jobs with labels as parameters. For example, the event of insufficient disk space appears as an alert in the alertmanager. Alertmanager sends webhook to alertmanager-webhook-receiver. alertmanager-webhook-receiver takes the mount point or PVC from the labels and runs **aws-k8s-ebs-autoscaler** as Kubernetes Job.

Note that false and multiple consecutive alerts are the responsibility of the monitoring system, not of the alertmanager-webhook-receiver or **aws-k8s-ebs-autoscaler**.

**aws-k8s-ebs-autoscaler** performs actions depending on what was passed as an argument, mount-point or pvc.

### If mount-point is received in arguments

* **aws-k8s-ebs-autoscaler** searches for volume serial number by the mount point. In the case of EBS the serial number is EBS VolumeID.
* If the snapshot flag was provided as true, it creates an EBS volume snapshot.
* If the dry-run flag was provided as true, **aws-k8s-ebs-autoscaler** only shows information about enlarging.
* If not, it enlarges the EBS volume by a percentage, defined in the percents flag.
* If the wait-for-modifying flag was provided as true, **aws-k8s-ebs-autoscaler** waits for the in-use status of the EBS volume.

### If pvc is received in arguments

NOTE: The allowVolumeExpansion and ExpandInUsePersistentVolumes options should be enabled in your Kubernetes cluster for the PVC auto enlarging. Read [this](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/) doc.

* **aws-k8s-ebs-autoscaler** gets the PVC metadata in the given Kubernetes namespace provided in the pvc-namespace flag.
* If the snapshot flag was provided as true, it creates a [Kubernetes VolumeSnapshot](https://kubernetes.io/docs/concepts/storage/volume-snapshots/) resource. Note about the k8s-snapshot-class flag.
* If the dry-run flag was provided as true, **aws-k8s-ebs-autoscaler** only shows information about enlarging.
* If not, it enlarges the PVC size.
* If the wait-for-modifying flag was provided as true, **aws-k8s-ebs-autoscaler** waits for the Ready status of the PVC.
