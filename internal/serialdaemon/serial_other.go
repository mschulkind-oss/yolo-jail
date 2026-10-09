//go:build !linux && !darwin

package serialdaemon

import (
	"fmt"
	"os"
)

func openAndConfigureSerial(devicePath string, baud int) (*os.File, error) {
	return nil, fmt.Errorf("serial ports are not supported on this platform")
}

// probeOpen is `list`'s accessibility check (serial_posix.go has the posix spelling).
func probeOpen(devicePath string) error {
	f, err := os.OpenFile(devicePath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}
