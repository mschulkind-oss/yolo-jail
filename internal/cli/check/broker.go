package check

import (
	"encoding/binary"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Broker singleton socket / pid file locations. Frozen contract (must not
// drift — the broker daemon in internal/loopholes writes/binds these exact
// paths; both sides must agree byte-for-byte).
const (
	brokerLoopholeName    = "claude-oauth-broker"
	brokerSingletonSocket = "/tmp/yolo-claude-oauth-broker.sock"
	brokerSingletonPIDFil = "/tmp/yolo-claude-oauth-broker.pid"
)

// hostServiceDefaultJailEndpoint returns the in-jail path of a host service's
// published endpoint file — a REGULAR FILE, which is why the in-jail probe tests
// it with `test -f` and not `test -S`.
func hostServiceDefaultJailEndpoint(name string) string {
	return paths.JailHostServicesDir + "/" + name + paths.ServiceEndpointExt
}

// hostServiceSocketsDir returns the per-jail host-side endpoint-file dir. It
// delegates to paths: this was a hand-copied third implementation of the same
// hash-and-join, in the one package whose whole job is telling the user whether
// the other two agree.
func hostServiceSocketsDir(cname string, isMacOS bool) string {
	return paths.HostServicesDir(cname, isMacOS)
}

// brokerStatus holds the broker liveness snapshot: pid, pid_live, socket_exists,
// socket_accepts.
type brokerStatus struct {
	pid           int
	pidPresent    bool
	pidLive       bool
	socketExists  bool
	socketAccepts bool
}

func (o *Options) brokerStatus() brokerStatus {
	pid, present := brokerReadPID()
	pidLive := present && execx.IsAlive(pid)
	sockExists := o.PathExists(brokerSingletonSocket)
	accepts := sockExists && brokerSocketAccepts(brokerSingletonSocket, 2*time.Second)
	return brokerStatus{
		pid:           pid,
		pidPresent:    present,
		pidLive:       pidLive,
		socketExists:  sockExists,
		socketAccepts: accepts,
	}
}

// brokerReadPID returns (pid, present).
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

// brokerSocketAccepts reports whether the host-wide singleton's socket accepts a
// connection. Connect, then close — nothing is written and nothing is read.
//
// IT USED TO BE A PROTOCOL PING on this socket and cannot be one any more, for the
// reason spelled out at broker.SingletonReachable: the singleton sits behind
// yolo's front now (`publishes: "socket"` + `scope: "host"`), so its first read on
// every connection is yolo's CONNECTION PREAMBLE, and a bare `{"action":"ping"}`
// is rejected as a preamble with no version. Writing a forged preamble from here
// would mean `yolo check` asserting a jail identity for a connection that belongs
// to no jail.
//
// The protocol round trip is not lost — it moved to the probe that can still
// legitimately make it. checkBrokerEndpoint dials the per-jail ENDPOINT, so the
// front writes a real preamble and brokerPingConn's ping reaches the handler
// exactly as a jail's would. That probe is also the one that tests the hop a jail
// actually travels, which is why it is the one worth having.
func brokerSocketAccepts(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// brokerPingConn sends the length-prefixed {"action":"ping"} request over a
// connection the caller already opened and expects a data frame (stream 0) whose
// JSON has pong:true, before the exit frame (stream 2). Any error → false.
//
// The caller opens the connection because reaching the broker means reading a
// jail's endpoint file, pinning its certificate and presenting its token before a
// single protocol byte is written — and because the front, not this function, is
// what puts the connection preamble ahead of these bytes.
func brokerPingConn(conn net.Conn, timeout time.Duration) bool {
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

// brokerEndpointVisibleInJail reports whether the RUNNING
// container sees the broker's endpoint file. Returns tri-state: visible=true,
// absent=false, unknown=nil (exec unavailable / exec-level failure). Represented
// as (*bool).
//
// `test -f`, not `test -S`: what crosses into the jail is a regular file naming
// the front's loopback listener, not a socket. The old -S probe would report every
// healthy jail as broken.
func (o *Options) brokerEndpointVisibleInJail(rt, cname string) *bool {
	if rt == "" || cname == "" {
		return nil
	}
	jailEndpoint := hostServiceDefaultJailEndpoint(brokerLoopholeName)
	res := o.Exec([]string{rt, "exec", cname, "sh", "-c", "test -f " + jailEndpoint}, "", nil, 10*time.Second)
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
