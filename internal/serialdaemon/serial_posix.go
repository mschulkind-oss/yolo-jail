//go:build linux || darwin

package serialdaemon

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openAndConfigureSerial opens the serial port and sets raw mode + baud rate.
func openAndConfigureSerial(devicePath string, baud int) (*os.File, error) {
	fd, err := unix.Open(devicePath, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open serial port: %w", err)
	}

	file := os.NewFile(uintptr(fd), devicePath)

	if err := configureTermios(fd, baud); err != nil {
		file.Close()
		return nil, err
	}

	return file, nil
}
