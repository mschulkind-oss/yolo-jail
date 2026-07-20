package check

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Broker singleton socket / pid file locations (mirrors
// loopholes_runtime.BROKER_SINGLETON_SOCKET / _PID_FILE and BROKER_LOOPHOLE_NAME).
const (
	brokerLoopholeName    = "claude-oauth-broker"
	brokerSingletonSocket = "/tmp/yolo-claude-oauth-broker.sock"
	brokerSingletonPIDFil = "/tmp/yolo-claude-oauth-broker.pid"
)

// hostServiceDefaultJailSocket ports _host_service_default_jail_socket.
func hostServiceDefaultJailSocket(name string) string {
	return paths.JailHostServicesDir + "/" + name + ".sock"
}

// hostServiceSocketsDir ports _host_service_sockets_dir: the per-jail host-side
// dir under /tmp keyed by an 8-hex sha1 of the container name. On macOS /tmp
// resolves to /private/tmp; the resolved form is used so socket paths match
// what the kernel sees.
func hostServiceSocketsDir(cname string, isMacOS bool) string {
	sum := sha1.Sum([]byte(cname))
	shortHash := hex.EncodeToString(sum[:])[:8]
	base := "/tmp"
	if isMacOS {
		if r, err := filepath.EvalSymlinks(base); err == nil {
			base = r
		}
	}
	return filepath.Join(base, "yolo-host-services-"+shortHash)
}

// brokerStatus ports _broker_status: pid, pid_live, socket_exists, ping_ok.
type brokerStatus struct {
	pid          int
	pidPresent   bool
	pidLive      bool
	socketExists bool
	pingOK       bool
}

func (o *Options) brokerStatus() brokerStatus {
	pid, present := brokerReadPID()
	pidLive := present && execx.IsAlive(pid)
	sockExists := o.PathExists(brokerSingletonSocket)
	pingOK := sockExists && brokerPing(brokerSingletonSocket, 2*time.Second)
	return brokerStatus{
		pid:          pid,
		pidPresent:   present,
		pidLive:      pidLive,
		socketExists: sockExists,
		pingOK:       pingOK,
	}
}

// brokerReadPID ports _broker_read_pid: (pid, present).
func brokerReadPID() (int, bool) {
	data, err := os.ReadFile(brokerSingletonPIDFil)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// brokerPing ports _broker_ping: connect to socketPath, send a length-prefixed
// {"action":"ping"} request, and expect a data frame (stream 0) whose JSON has
// pong:true, before the exit frame (stream 2). Any error → false.
func brokerPing(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	body := []byte(`{"action":"ping"}`)
	var lenPrefix [4]byte
	binary.BigEndian.PutUint32(lenPrefix[:], uint32(len(body)))
	if _, err := conn.Write(append(lenPrefix[:], body...)); err != nil {
		return false
	}

	for {
		hdr := make([]byte, 5)
		if _, err := readFull(conn, hdr); err != nil {
			return false
		}
		sid := hdr[0]
		ln := binary.BigEndian.Uint32(hdr[1:])
		payload := make([]byte, ln)
		if ln > 0 {
			if _, err := readFull(conn, payload); err != nil {
				return false
			}
		}
		switch sid {
		case 0: // STREAM_STDOUT
			decoded, err := jsonx.Decode(payload)
			if err != nil {
				return false
			}
			obj, ok := decoded.(*jsonx.OrderedMap)
			if !ok {
				return false
			}
			pong, _ := obj.Get("pong")
			b, _ := pong.(bool)
			return b
		case 2: // STREAM_EXIT without a pong first → not alive
			return false
		}
	}
}

// readFull reads len(buf) bytes or returns an error (io.ReadFull semantics,
// honoring the connection deadline).
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// relaySocketVisibleInJail ports _relay_socket_visible_in_jail: does the RUNNING
// container see the relay socket? Returns tri-state: visible=true, absent=false,
// unknown=nil (exec unavailable / exec-level failure). Represented as (*bool).
func (o *Options) relaySocketVisibleInJail(rt, cname string) *bool {
	if rt == "" || cname == "" {
		return nil
	}
	jailSock := hostServiceDefaultJailSocket(brokerLoopholeName)
	res := o.Exec([]string{rt, "exec", cname, "sh", "-c", "test -S " + jailSock}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout {
		return nil
	}
	switch res.RC {
	case 0:
		t := true
		return &t
	case 1:
		f := false
		return &f
	default:
		// 125/126/127…: exec-level failure, not a probe answer.
		return nil
	}
}
