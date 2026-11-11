package launcher

import (
	"os"
	"path/filepath"
	"sort"
)

// serialDevicePatterns name USB serial adapters: tty.usbserial-* and
// tty.usbmodem* on macOS, and ttyUSB* (USB-serial converters) and ttyACM*
// (CDC ACM, e.g. console ports of network devices) on Linux.
var serialDevicePatterns = []string{"tty.usb*", "ttyUSB*", "ttyACM*"}

func serialDevices(root string) ([]string, error) {
	var devices []string
	for _, pattern := range serialDevicePatterns {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err == nil && info.Mode()&os.ModeCharDevice != 0 {
				devices = append(devices, path)
			}
		}
	}
	sort.Strings(devices)
	return devices, nil
}
