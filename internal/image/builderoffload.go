package image

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
)

// builderoffload.go holds the real (subprocess/filesystem/clock) seams for the
// macOS container-builder offload (J3). The pure lifecycle + argv live in
// internal/containerbuilder; this wires them to the host.

// ensureBuilderKey makes sure an ed25519 keypair exists under BuilderKeyDir and
// returns the public half (the string authorized in the builder container via
// RunArgv's pubkey env). Generates the pair with ssh-keygen on first use.
func ensureBuilderKey() (string, error) {
	keyDir := containerbuilder.BuilderKeyDir()
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return "", err
	}
	key := containerbuilder.BuilderKey()
	pub := key + ".pub"
	if _, err := os.Stat(pub); err != nil {
		// Generate a fresh keypair (no passphrase; the container regenerates its
		// host key each boot, so this is an ephemeral client key).
		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key, "-q")
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("ssh-keygen failed: %w", err)
		}
	}
	data, err := os.ReadFile(pub)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// realSessionDeps wires containerbuilder.Session to the host runtime.
func realSessionDeps(out io.Writer) containerbuilder.Deps {
	return containerbuilder.Deps{
		Run: func(argv []string) int {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdout, cmd.Stderr = nil, os.Stderr
			if err := cmd.Run(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					return ee.ExitCode()
				}
				return 1
			}
			return 0
		},
		Output: func(argv []string) (string, int) {
			cmd := exec.Command(argv[0], argv[1:]...)
			b, err := cmd.Output()
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					return string(b), ee.ExitCode()
				}
				return "", 1
			}
			return string(b), 0
		},
		Reachable: func(host string, port int) bool {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
			if err != nil {
				return false
			}
			_ = conn.Close()
			return true
		},
		Sleep: func(seconds float64) { time.Sleep(time.Duration(seconds * float64(time.Second))) },
		Now:   func() float64 { return float64(time.Now().UnixNano()) / 1e9 },
		Out:   out,
	}
}
