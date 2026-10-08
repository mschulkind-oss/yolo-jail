package hostservice

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	StartupReasonFDEnv      = "YOLO_HOST_SERVICE_REASON_FD"
	StartupReasonAttemptEnv = "YOLO_HOST_SERVICE_ATTEMPT"
	StartupReasonServiceEnv = "YOLO_HOST_SERVICE_NAME"
	StartupReasonMaxBytes   = 4096
	startupReasonTextMax    = 600
)

type StartupReason struct {
	Version int    `json:"version"`
	Service string `json:"service"`
	Attempt string `json:"attempt"`
	Class   string `json:"class"`
	Reason  string `json:"reason"`
	Remedy  string `json:"remedy,omitempty"`
}

// NewStartupReasonChannel creates a private socketpair and opaque attempt token. The caller
// passes child as exec.Cmd.ExtraFiles (fd 3) and closes it after Start; parent is never inherited.
func NewStartupReasonChannel() (parent net.Conn, child *os.File, attempt string, err error) {
	parent, child, err = commandSocketPair()
	if err != nil {
		return nil, nil, "", err
	}
	var token [24]byte
	if _, err = rand.Read(token[:]); err != nil {
		_ = parent.Close()
		_ = child.Close()
		return nil, nil, "", err
	}
	return parent, child, hex.EncodeToString(token[:]), nil
}

// WriteStartupReasonFromEnv writes one framed refusal to the inherited fd. It never waits for a
// reader or writes a second record; callers should invoke it before declaring service readiness.
func WriteStartupReasonFromEnv(reason StartupReason) error {
	fdText := os.Getenv(StartupReasonFDEnv)
	attempt := os.Getenv(StartupReasonAttemptEnv)
	service := os.Getenv(StartupReasonServiceEnv)
	fd, err := strconv.Atoi(fdText)
	if err != nil || fd < 3 || attempt == "" || service == "" {
		return errors.New("host-service startup reason channel is not configured")
	}
	reason.Version, reason.Service, reason.Attempt = 1, service, attempt
	if !validStartupReasonClass(reason.Class) {
		return errors.New("invalid host-service startup reason class")
	}
	reason.Reason = safeStartupText(reason.Reason)
	reason.Remedy = safeStartupText(reason.Remedy)
	if reason.Reason == "" {
		return errors.New("host-service startup reason is empty")
	}
	body, err := json.Marshal(reason)
	if err != nil {
		return err
	}
	if len(body) > StartupReasonMaxBytes {
		return errors.New("host-service startup reason exceeds the size limit")
	}
	file := os.NewFile(uintptr(fd), "host-service-startup-reason")
	defer file.Close()
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := file.Write(prefix[:]); err != nil {
		return err
	}
	_, err = file.Write(body)
	return err
}

// ReadStartupReason reads one bounded record. It does not wait for EOF: a descendant retaining
// the descriptor cannot keep the caller blocked beyond deadline.
func ReadStartupReason(conn net.Conn, service, attempt string, deadline time.Time) (*StartupReason, error) {
	if conn == nil {
		return nil, errors.New("startup reason channel unavailable")
	}
	_ = conn.SetReadDeadline(deadline)
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > StartupReasonMaxBytes {
		return nil, errors.New("invalid startup reason frame size")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	var record StartupReason
	if err := dec.Decode(&record); err != nil {
		return nil, errors.New("malformed startup reason record")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, errors.New("startup reason contains trailing data")
	}
	if record.Version != 1 || record.Service != service || record.Attempt != attempt ||
		!validStartupReasonClass(record.Class) {
		return nil, errors.New("startup reason attribution or version mismatch")
	}
	record.Reason = safeStartupText(record.Reason)
	record.Remedy = safeStartupText(record.Remedy)
	if record.Reason == "" {
		return nil, errors.New("empty startup reason")
	}
	return &record, nil
}

func validStartupReasonClass(class string) bool {
	switch class {
	case "configuration", "dependency", "permission", "internal":
		return true
	default:
		return false
	}
}

func safeStartupText(text string) string {
	plain := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)), " ")
	var out strings.Builder
	n := 0
	for _, r := range plain {
		if n == startupReasonTextMax {
			break
		}
		out.WriteRune(r)
		n++
	}
	return out.String()
}
