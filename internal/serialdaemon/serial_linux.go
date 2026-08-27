//go:build linux

package serialdaemon

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func baudToSpeedLinux(baud int) (uint32, error) {
	switch baud {
	case 9600:
		return unix.B9600, nil
	case 19200:
		return unix.B19200, nil
	case 38400:
		return unix.B38400, nil
	case 57600:
		return unix.B57600, nil
	case 115200:
		return unix.B115200, nil
	case 230400:
		return unix.B230400, nil
	case 460800:
		return unix.B460800, nil
	case 921600:
		return unix.B921600, nil
	default:
		return 0, fmt.Errorf("unsupported baud rate: %d", baud)
	}
}

func configureTermios(fd int, baud int) error {
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("tcgets: %w", err)
	}

	speed, err := baudToSpeedLinux(baud)
	if err != nil {
		return err
	}

	// Raw mode: disable echo, canonical mode, extended input processing, signal chars
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB | unix.CBAUD
	termios.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL | speed

	// 0.1s read timeout, min 0 bytes
	termios.Cc[unix.VMIN] = 0
	termios.Cc[unix.VTIME] = 1

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		return fmt.Errorf("tcsets: %w", err)
	}
	return nil
}
