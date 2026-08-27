//go:build !linux && !darwin

package serialdaemon

import (
	"fmt"
	"os"
)

func openAndConfigureSerial(devicePath string, baud int) (*os.File, error) {
	return nil, fmt.Errorf("serial ports are not supported on this platform")
}
