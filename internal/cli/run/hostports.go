package run

// hostports handles the forward_host_ports config parser and the socat
// UNIX-LISTEN→TCP argv the run path spawns per forwarded port.

import (
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Socket-wait tuning (host side polls for socat's socket files before the
// container starts). Production defaults must stay 2s / 5ms.
const (
	SocketWaitDeadline     = 2 * time.Second
	SocketWaitPollInterval = 5 * time.Millisecond
)

// PortForward is one parsed forward: the port inside the jail (localPort) and
// the host 127.0.0.1 port it bridges to (hostPort).
type PortForward struct {
	LocalPort int
	HostPort  int
}

// ParsePortForwards parses forward_host_ports config entries into
// (localPort, hostPort) pairs:
//
//   - a JSON integer      → (n, n)
//   - a string "a:b"      → (int(a), int(b))   [split once]
//   - a plain string "n"  → (n, n)
//   - anything else       → a warning (returned via the warn callback), skipped
//
// A non-numeric string port surfaces as a returned error, aborting the parse —
// the caller decides. warn receives the "Warning: invalid port forward entry:
// %v" text for non-int/str entries; pass nil to ignore.
func ParsePortForwards(entries []any, warn func(string)) ([]PortForward, error) {
	var result []PortForward
	for _, entry := range entries {
		// A JSON integer literal (jsonx decodes ints as jsonInt).
		if jsonx.IsInt(entry) {
			n, _ := jsonx.AsInt(entry)
			result = append(result, PortForward{int(n), int(n)})
			continue
		}
		// A raw Go int (callers that build entries directly).
		if n, ok := entry.(int); ok {
			result = append(result, PortForward{n, n})
			continue
		}
		s, ok := entry.(string)
		if !ok {
			if warn != nil {
				warn(fmt.Sprintf("Warning: invalid port forward entry: %v", entry))
			}
			continue
		}
		if idx := indexByte(s, ':'); idx >= 0 {
			a, err := strconv.Atoi(s[:idx])
			if err != nil {
				return nil, fmt.Errorf("invalid port forward %q: %w", s, err)
			}
			b, err := strconv.Atoi(s[idx+1:])
			if err != nil {
				return nil, fmt.Errorf("invalid port forward %q: %w", s, err)
			}
			result = append(result, PortForward{a, b})
			continue
		}
		p, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("invalid port forward %q: %w", s, err)
		}
		result = append(result, PortForward{p, p})
	}
	return result, nil
}

// indexByte returns the index of the first b in s, or -1. Only the first ':'
// splits (like split(":", 1)), so we need its first index.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// SocketPath returns the host-side socket file path for a local port under
// socketDir: "port-<localPort>.sock".
func SocketPath(socketDir string, localPort int) string {
	return filepath.Join(socketDir, fmt.Sprintf("port-%d.sock", localPort))
}

// SocatArgv returns the host-side socat argv bridging a Unix listen socket to a
// host TCP port. Frozen contract (argv must not drift — the container-side socat
// depends on the exact form):
//
//	socat UNIX-LISTEN:<sock>,fork,mode=777 TCP:127.0.0.1:<hostPort>
func SocatArgv(sockPath string, hostPort int) []string {
	return []string{
		"socat",
		fmt.Sprintf("UNIX-LISTEN:%s,fork,mode=777", sockPath),
		fmt.Sprintf("TCP:127.0.0.1:%d", hostPort),
	}
}

// SocketNotReadyWarning is the stderr text emitted when socat's socket
// files don't appear before the deadline.
func SocketNotReadyWarning(missing []string) string {
	return fmt.Sprintf(
		"Warning: socat socket(s) not ready after %.1fs: %s",
		SocketWaitDeadline.Seconds(), joinComma(missing),
	)
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
