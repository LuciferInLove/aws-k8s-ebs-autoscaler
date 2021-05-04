package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/sirupsen/logrus"
)

var (
	hostSysPath      *string        = flag.String("sys-path", "/sys", "sysfs mountpoint.")
	hostProcPath     *string        = flag.String("proc-path", "/proc", "procfs mountpoint.")
	mountPoint       *string        = flag.String("mount-point", "", "Mount point of the volume to be enlarged. (required if pvc isn't set)")
	pvc              *string        = flag.String("pvc", "", "PVC ID of the volume to be enlarged. (required if mount-point isn't set)")
	pvcNamespace     *string        = flag.String("pvc-namespace", "", "Kubernetes namespace where pvc is located. (required if mount-point isn't set)")
	percents         *int64         = flag.Int64("percents", 20, "By what percentage to increase.")
	createSnapshot   *bool          = flag.Bool("snapshot", false, "If true, create a volume snapshot. (default false)")
	k8sSnapshotClass *string        = flag.String("k8s-snapshot-class", "csi-aws-vsc", "The name of the VolumeSnapshotClass resource, which is used to create snapshots in Kubernetes.")
	dryRun           *bool          = flag.Bool("dry-run", false, "If true, only show the result without enlarging the volume. (default false)")
	waitForModifying *bool          = flag.Bool("wait-for-modifying", false, "If true, wait for enlarging the volume to be completed. (default false)")
	logLevel         *string        = flag.String("log-level", "info", "Only log messages with the given severity or above. One of: [debug, info, warn, error]")
	log              *logrus.Logger = logrus.New()
	logLevelsList    [4]string      = [4]string{"debug", "info", "warn", "error"}
	dryRunMessage    string         = "Request would have succeeded, but -dry-run=true flag is set. Exiting..."
)

// LogLevelContains looks for defined log level in the logLevelsList
func LogLevelContains(slice [4]string, value string) (logrus.Level, error) {
	for _, item := range slice {
		if item == value {
			logrusLogLevel, err := logrus.ParseLevel(value)
			return logrusLogLevel, err
		}
	}
	return 0, fmt.Errorf("There was a wrong log level defined: %v", value)
}

func init() {
	log.SetFormatter(&logrus.TextFormatter{
		DisableColors: true,
		DisableQuote:  false,
		FullTimestamp: true,
	})
}

func main() {
	flag.Parse()

	logrusLogLevel, err := LogLevelContains(logLevelsList, *logLevel)

	if err != nil {
		flag.Usage()
		log.Fatalln(err)
	}

	log.SetLevel(logrusLogLevel)

	if runtime.GOOS != "linux" {
		log.Fatalln("The program only runs on Linux.")
	}

	// pvcNamespace must be defined if pvc is defined.
	if *pvc != "" && *pvcNamespace == "" {
		flag.Usage()
		log.Fatalln("pvc-namespace must be defined if pvc is defined.")
	}

	// Check if mountPoint or pvc is defined.
	// Depending on what is defined, run the appropriate function to get volumeIDsList.
	// If neither or both are defined, throw an error.
	switch {
	// If -pvc is specified, increase the PVC size.
	case *mountPoint == "" && *pvc != "":
		log.Infof("-pvc=%s is specified. Increasing PVC size...", *pvc)

		err := EnlargePVC(*pvc, *pvcNamespace, percents, createSnapshot, dryRun, waitForModifying)
		if err != nil {
			if err.Error() == "DryRunOperation" {
				log.Infoln(dryRunMessage)
			} else if err.Error() == "ContextTimeout" {
				log.Fatalf("Timeout while waiting for the PVC enlargement. See events in namespace \"%s\".", *pvcNamespace)
			} else {
				log.Fatalln(err.Error())
			}
		}
	// If -mount-point is specified, increase AWS EBS size directly.
	case *mountPoint != "" && *pvc == "":
		log.Infof("-mount-point=%s is specified. Increasing AWS EBS size directly...", *mountPoint)

		volumeIDsList := GetEBSVolumeIDsByMountPoint(*mountPoint)
		if len(volumeIDsList) == 0 {
			log.Fatalln("No volume IDs found. Try to run the program with -log-level=debug flag.")
		}

		for _, volumeID := range volumeIDsList {
			err := EnlargeVolumeByID(&volumeID, percents, createSnapshot, dryRun, waitForModifying)
			if err != nil {
				if awsError, ok := err.(awserr.Error); ok {
					switch awsError.Code() {
					case "DryRunOperation":
						log.Infoln(dryRunMessage)
						os.Exit(0)
					default:
						log.Fatalln(awsError.Error())
					}
				} else {
					log.Fatalln(err.Error())
				}
			}
		}
	case *mountPoint != "" && *pvc != "":
		flag.Usage()
		log.Fatalln("mount-point and pvc cannot be defined together.")
	default:
		flag.Usage()
		log.Fatalln("Either mount-point or pvc has to be defined.")
	}

	os.Exit(0)

}
