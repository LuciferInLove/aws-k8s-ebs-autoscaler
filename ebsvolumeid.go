package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

func getListOfSecondaryDevices(secondariesList []string, devicesPathsList []string) ([]string, []string) {
	if len(secondariesList) == 0 {
		return nil, devicesPathsList
	}

	// Use the first item from the secondariesList as the current devicePath.
	devicePath := secondariesList[0]
	log.Debugln("Path in the current secondaries search iteration:", devicePath)

	pathForSearchSecondaries := filepath.Join(devicePath, "slaves")

	// Delete the current being checked item from the secondariesList.
	previous := len(secondariesList) - 1
	secondariesList[0] = secondariesList[previous]
	secondariesList = secondariesList[:previous]

	// Check if the current device path has slaves directory.
	openPathForSearchSecondaries, err := os.Open(pathForSearchSecondaries)
	if err != nil {
		if osError, ok := err.(*os.PathError); ok {
			switch osError.Err.(syscall.Errno) {
			case syscall.ENOENT:
				// If not, append it to the devicesPathsList.
				devicesPathsList = append(devicesPathsList, devicePath)
				return getListOfSecondaryDevices(secondariesList, devicesPathsList)
			default:
				log.Fatalln(osError.Error())
			}
		}
		log.Fatalln(err.Error())
	}
	defer openPathForSearchSecondaries.Close()

	// Also append the current device path in the devicesPathsList if it has empty slaves directory.
	_, err = openPathForSearchSecondaries.Readdirnames(1)
	if err == io.EOF {
		devicesPathsList = append(devicesPathsList, devicePath)
		return getListOfSecondaryDevices(secondariesList, devicesPathsList)
	}

	// Write absolute paths of secondaries from the slaves directory to the secondariesList.
	secondaries, err := ioutil.ReadDir(pathForSearchSecondaries)
	if err != nil {
		log.Fatalln(err)
	}
	for _, secondary := range secondaries {
		secondariesList = append(secondariesList, filepath.Join(pathForSearchSecondaries, secondary.Name()))
	}
	log.Debugf("Secondaries list in the current secondaries search iteration: %v", secondariesList)

	return getListOfSecondaryDevices(secondariesList, devicesPathsList)
}

func appendDeviceIfUnique(parentDevicesList []string, parentDevice string) []string {
	for _, item := range parentDevicesList {
		if item == parentDevice {
			return parentDevicesList
		}
	}
	return append(parentDevicesList, parentDevice)
}

// GetEBSVolumeIDsByMountPoint finds the device by its mount point, then
// finds the serial number of that device. If the program is running in AWS,
// then the serial number is the EBS VolumeID.
func GetEBSVolumeIDsByMountPoint(mountPoint string) []string {
	// Get mount points.
	mountPoints, err := ioutil.ReadFile(*hostProcPath + "/self/mounts")
	if err != nil {
		log.Fatalln(err)
	}

	// Search mountPoint in the mount points file.
	mountPointsFile := string(mountPoints)
	mountPointRegexp := regexp.MustCompile(`\S+\s` + mountPoint + `\s.*`)
	matches := mountPointRegexp.FindStringSubmatch(mountPointsFile)
	mountPointString := strings.Split(matches[0], " ")

	deviceFileSystem := mountPointString[2]
	device := mountPointString[0]
	deviceInfo, err := os.Stat(device)
	if err != nil {
		log.Fatalln(err)
	}

	if !strings.HasPrefix(device, "/dev/") {
		log.Fatalf("\"%s\" is not a device file.", device)
	}
	log.Infof("Found the device \"%s\" with %s filesystem matching the mount point \"%s\".", device, deviceFileSystem, mountPoint)

	mode := deviceInfo.Mode()

	if mode&os.ModeDevice != os.ModeDevice {
		log.Fatalln("Wrong file mode of the device file.")
	}

	// Search device path in sys.
	deviceMajorNumber := deviceInfo.Sys().(*syscall.Stat_t).Rdev / 256
	deviceMinorNumber := deviceInfo.Sys().(*syscall.Stat_t).Rdev % 256

	log.Debugf("Device major ID number and minor ID number: %d:%d", deviceMajorNumber, deviceMinorNumber)

	deviceSysPath := *hostSysPath + "/dev/block/" + fmt.Sprintf("%d:%d", deviceMajorNumber, deviceMinorNumber)

	if fileInfo, err := os.Stat(deviceSysPath); os.IsNotExist(err) || !fileInfo.IsDir() {
		log.Fatalf("%s is not a folder or does not exist.", deviceSysPath)
	}

	log.Debugf("Device path in %s: %s", *hostSysPath, deviceSysPath)

	// Get paths to physical devices of mount point.
	secondariesList := []string{deviceSysPath}
	secondariesList, devicesPathsList := getListOfSecondaryDevices(secondariesList, nil)

	log.Debugf("Secondary devices of the device \"%s\": %v", device, devicesPathsList)

	var parentDevicesList []string
	for _, path := range devicesPathsList {
		deviceLink, err := filepath.EvalSymlinks(path)
		if err != nil {
			log.Debugln(err)
		}
		log.Debugln("Device link: " + deviceLink)

		parentDevicePattern := regexp.MustCompile(`^.*\/(.*)\/.*$`)
		parentDevice := parentDevicePattern.ReplaceAllString(deviceLink, `$1`)
		log.Debugln("Parental device:", parentDevice)

		if fileInfo, err := os.Stat(filepath.Join("/dev", parentDevice)); os.IsNotExist(err) || fileInfo.Mode()&os.ModeDevice != os.ModeDevice {
			continue
		}

		parentDevicesList = appendDeviceIfUnique(parentDevicesList, parentDevice)
	}

	log.Debugf("Parental devices list: %v", parentDevicesList)
	if len(parentDevicesList) == 0 {
		log.Fatalf("No parental devices of \"%s\" found. Try to run the program with -log-level=debug flag.", device)
	}

	// Get list of parent devices volume ids.
	var volumeIDsList []string
	for _, parentDevice := range parentDevicesList {
		serial, err := ioutil.ReadFile(*hostSysPath + "/class/block/" + parentDevice + "/device/serial")
		if err != nil {
			log.Fatalf("Couldn't get serial of device \"%s\": %s", parentDevice, err)
		}
		volumeID := strings.TrimSpace(string(serial))
		log.Infof("Device \"%s\" serial is %s.", parentDevice, volumeID)

		serialRegexp := regexp.MustCompile(`vol(.*)`)
		hasVolumeIDEBSPrefix := serialRegexp.FindString(volumeID)
		if hasVolumeIDEBSPrefix == "" {
			log.Fatalf("Device \"%s\" is not an EBS volume.", parentDevice)
		}

		volumeID = serialRegexp.ReplaceAllString(volumeID, `vol-$1`)
		volumeIDsList = append(volumeIDsList, volumeID)
	}

	return volumeIDsList
}
