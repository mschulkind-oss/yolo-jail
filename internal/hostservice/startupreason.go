package hostservice

import (
	"context"
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
	"syscall"
	"time"
	"unicode"
)

// The attempt-reason channel: architecture and invariants in
// docs/reference/host-service-startup-diagnostics.md.

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

// startupReasonChannelFromEnv reads the inherited channel's three variables. When they name a
// usable fd it marks that fd close-on-exec, so nothing the daemon starts from here on (an `aws
// --version` probe, a credential helper) inherits the owner's channel.
func startupReasonChannelFromEnv() (fd int, attempt, service string, ok bool) {
	fd, err := strconv.Atoi(os.Getenv(StartupReasonFDEnv))
	attempt = os.Getenv(StartupReasonAttemptEnv)
	service = os.Getenv(StartupReasonServiceEnv)
	if err != nil || fd < 3 || attempt == "" || service == "" {
		return 0, "", "", false
	}
	syscall.CloseOnExec(fd)
	return fd, attempt, service, true
}

// forgetStartupReasonEnv removes the channel's variables from this process's environment, so a
// descendant neither inherits them nor reads them as naming an fd that is no longer the channel.
func forgetStartupReasonEnv() {
	for _, name := range []string{StartupReasonFDEnv, StartupReasonAttemptEnv, StartupReasonServiceEnv} {
		_ = os.Unsetenv(name)
	}
}

// ProtectStartupReason marks the inherited startup-reason fd close-on-exec without writing or
// closing it. A daemon calls it first, before it starts any child of its own, so the channel
// stays private to it until it writes a refusal (WriteStartupReasonFromEnv) or reports itself
// serving (ReleaseStartupReason). A no-op when no channel is configured.
func ProtectStartupReason() {
	_, _, _, _ = startupReasonChannelFromEnv()
}

// ReleaseStartupReason ends this process's use of the startup-reason channel without writing a
// record: it closes the inherited fd and unsets the three variables. A daemon calls it once it is
// serving. A no-op when no channel is configured, and safe to call twice, since the first call
// removes the variables that name the fd.
func ReleaseStartupReason() {
	fd, _, _, ok := startupReasonChannelFromEnv()
	if !ok {
		return
	}
	forgetStartupReasonEnv()
	_ = os.NewFile(uintptr(fd), "host-service-startup-reason").Close()
}

// WriteStartupReasonFromEnv writes one framed refusal to the inherited fd, then closes the fd and
// unsets the channel's variables (ReleaseStartupReason's effect), so a second call reports the
// channel unconfigured instead of writing a second record. It never waits for a reader; callers
// should invoke it before declaring service readiness.
func WriteStartupReasonFromEnv(reason StartupReason) error {
	fd, attempt, service, ok := startupReasonChannelFromEnv()
	if !ok {
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
	// Unset before the close (defers run last-in first-out), so nothing reading the variables
	// between the two can act on a descriptor number the close is about to free.
	defer file.Close()
	defer forgetStartupReasonEnv()
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := file.Write(prefix[:]); err != nil {
		return err
	}
	_, err = file.Write(body)
	return err
}

// ReadStartupReason is the compatibility adapter for callers that still need the historic pair.
// New owner code should use ReadStartupReasonOutcome so absence, cancellation and framing faults
// remain distinct without retaining raw connection errors.
func ReadStartupReason(conn net.Conn, service, attempt string, deadline time.Time) (*StartupReason, error) {
	outcome := ReadStartupReasonOutcome(context.Background(), conn, service, attempt, deadline)
	if outcome.Kind == StartupReasonReadRecord {
		return &StartupReason{Version: 1, Service: service, Attempt: attempt, Class: outcome.ReasonClass,
			Reason: outcome.Reason, Remedy: outcome.Remedy}, nil
	}
	return nil, startupReasonReadError(outcome)
}

// ReadStartupReasonOutcome reads one bounded record without waiting for EOF. ctx cancellation is
// reserved for an owner deliberately abandoning its optional reader after readiness/exit.
func ReadStartupReasonOutcome(ctx context.Context, conn net.Conn, service, attempt string, deadline time.Time) StartupReasonReadOutcome {
	if conn == nil {
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhaseUnknown,
			Fault: StartupReasonFaultUnavailable}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	if ctx.Err() != nil {
		return StartupReasonReadOutcome{Kind: StartupReasonReadCancelled, Phase: StartupReasonReadPhasePrefix}
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		_ = conn.Close()
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhasePrefix,
			Fault: StartupReasonFaultDeadlineInstall}
	}
	var prefix [4]byte
	n, err := io.ReadFull(conn, prefix[:])
	if err != nil {
		return classifyStartupReasonReadError(ctx, StartupReasonReadPhasePrefix, n, 4, err)
	}
	frameBytes := int(binary.BigEndian.Uint32(prefix[:]))
	if frameBytes == 0 || frameBytes > StartupReasonMaxBytes {
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhasePrefix,
			Fault: StartupReasonFaultFrameLength, Bytes: 4}
	}
	body := make([]byte, frameBytes)
	n, err = io.ReadFull(conn, body)
	if err != nil {
		outcome := classifyStartupReasonReadError(ctx, StartupReasonReadPhaseBody, n, frameBytes, err)
		outcome.Bytes += 4
		return outcome
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	var record StartupReason
	if err := dec.Decode(&record); err != nil {
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhaseDecode,
			Fault: StartupReasonFaultMalformedRecord, Bytes: 4 + frameBytes}
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhaseDecode,
			Fault: StartupReasonFaultMalformedRecord, Bytes: 4 + frameBytes}
	}
	if record.Version != 1 || record.Service != service || record.Attempt != attempt ||
		!validStartupReasonClass(record.Class) {
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhaseDecode,
			Fault: StartupReasonFaultAttribution, Bytes: 4 + frameBytes}
	}
	reason := safeStartupText(record.Reason)
	remedy := safeStartupText(record.Remedy)
	if reason == "" {
		return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: StartupReasonReadPhaseDecode,
			Fault: StartupReasonFaultEmptyReason, Bytes: 4 + frameBytes}
	}
	return StartupReasonReadOutcome{Kind: StartupReasonReadRecord, Phase: StartupReasonReadPhaseDecode,
		Bytes: 4 + frameBytes, ReasonClass: record.Class, Reason: reason, Remedy: remedy}
}

func classifyStartupReasonReadError(ctx context.Context, phase StartupReasonReadPhase, n, want int, err error) StartupReasonReadOutcome {
	// Owner cancellation means "nothing arrived before the owner stopped listening" only while no
	// byte of a frame has been seen. Once a prefix or body has started, the partial frame is a
	// channel fault whatever closed the read, so a teardown racing an expired deadline cannot
	// erase it (both owners cancel after a failed readiness wait too).
	if ctx.Err() != nil && n == 0 && phase == StartupReasonReadPhasePrefix {
		return StartupReasonReadOutcome{Kind: StartupReasonReadCancelled, Phase: phase}
	}
	if n == 0 && phase == StartupReasonReadPhasePrefix && errors.Is(err, io.EOF) {
		return StartupReasonReadOutcome{Kind: StartupReasonReadNoRecord, Phase: phase, Cause: StartupReasonReadCauseEOF}
	}
	var netErr net.Error
	deadline := errors.As(err, &netErr) && netErr.Timeout()
	if n == 0 && phase == StartupReasonReadPhasePrefix && deadline {
		return StartupReasonReadOutcome{Kind: StartupReasonReadNoRecord, Phase: phase, Cause: StartupReasonReadCauseDeadline}
	}
	fault := StartupReasonFaultRead
	if phase == StartupReasonReadPhasePrefix && n < want {
		fault = StartupReasonFaultPartialPrefix
	} else if phase == StartupReasonReadPhaseBody && n < want {
		fault = StartupReasonFaultPartialBody
	}
	cause := StartupReasonReadCauseNone
	if deadline {
		cause = StartupReasonReadCauseDeadline
	}
	return StartupReasonReadOutcome{Kind: StartupReasonReadChannelFault, Phase: phase, Fault: fault, Cause: cause, Bytes: n}
}

func startupReasonReadError(outcome StartupReasonReadOutcome) error {
	switch outcome.Kind {
	case StartupReasonReadNoRecord:
		if outcome.Cause == StartupReasonReadCauseEOF {
			return io.EOF
		}
		if outcome.Cause == StartupReasonReadCauseDeadline {
			return errors.New("startup reason record was not received before its deadline")
		}
		return errors.New("startup reason record was not received")
	case StartupReasonReadCancelled:
		return errors.New("startup reason read was cancelled by its owner")
	case StartupReasonReadChannelFault:
		return errors.New("startup reason channel read failed")
	default:
		return errors.New("startup reason channel returned no record")
	}
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
		// Format characters (Cf) too: a bidi override (U+202E) or a zero-width space (U+200B)
		// reorders or hides what a reader sees without being a control character.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
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
