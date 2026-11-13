//go:build linux

package cli

import (
	"errors"
	"strconv"

	"golang.org/x/sys/unix"
)

// privateClientTrustPath holds pem in an anonymous memory file the client inherits across its
// exec and returns the path the client opens it by. The trust is in no directory, so no other
// process, in this jail or a peer, can replace it between the bootstrap's write and the client's
// read. The descriptor is deliberately not close-on-exec and never wrapped in an *os.File (whose
// finalizer would close it): it is the client's only copy.
func privateClientTrustPath(pem []byte) (string, error) {
	fd, err := unix.MemfdCreate("yolo-http-front-trust", 0)
	if err != nil {
		return "", err
	}
	for rest := pem; len(rest) > 0; {
		n, err := unix.Write(fd, rest)
		if err != nil || n <= 0 {
			_ = unix.Close(fd)
			return "", errors.Join(errors.New("trust could not be written"), err)
		}
		rest = rest[n:]
	}
	return "/proc/self/fd/" + strconv.Itoa(fd), nil
}
