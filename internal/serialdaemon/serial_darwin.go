//go:build darwin

package serialdaemon

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func configureTermios(fd int, baud int) error {
	termios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return fmt.Errorf("tiocgeta: %w", err)
	}

	speed := uint64(baud)
	termios.Ispeed = speed
	termios.Ospeed = speed

	// Raw mode
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL

	termios.Cc[unix.VMIN] = 0
	termios.Cc[unix.VTIME] = 1

	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, termios); err != nil {
		return fmt.Errorf("tiocseta: %w", err)
	}
	return nil
}
