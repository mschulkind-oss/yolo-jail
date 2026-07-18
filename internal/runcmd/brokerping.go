package runcmd

import (
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// brokerPing ports _broker_ping: connect, send a length-prefixed
// {"action":"ping"} request, and expect a pong:true data frame (stream 0)
// before the exit frame (stream 2). Any error → false (a boolean liveness
// probe). Frame protocol: 4-byte BE length request; 1-byte stream id + 4-byte
// BE length response frames.
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

func readFull(r io.Reader, buf []byte) (int, error) { return io.ReadFull(r, buf) }
