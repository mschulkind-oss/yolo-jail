package loopholedecl_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// launchCheckManifest is a host-wide socket daemon whose host_daemon carries extra.
func launchCheckManifest(extra map[string]any) map[string]any {
	hd := map[string]any{"cmd": []any{"d", "{socket}"}, "publishes": "socket", "scope": "host"}
	for k, v := range extra {
		hd[k] = v
	}
	return map[string]any{"name": "checked", "description": "x", "host_daemon": hd}
}

// TestLaunchCheckDecodesAndDefaultsOff: `host_daemon.launch_check` is how a daemon says it
// answers the launch check (internal/hostservice/launchcheck.go). Absent means no: a daemon
// answers only if it was written to.
func TestLaunchCheckDecodesAndDefaultsOff(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		{"absent", nil, false},
		{"true", map[string]any{"launch_check": true}, true},
		{"false", map[string]any{"launch_check": false}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := decodeMap(t, "checked", launchCheckManifest(tc.extra))
			if err != nil {
				t.Fatalf("strict decode: %v", err)
			}
			if m.HostDaemon.LaunchCheck != tc.want {
				t.Errorf("LaunchCheck = %v, want %v", m.HostDaemon.LaunchCheck, tc.want)
			}
		})
	}
}

// TestLaunchCheckValueMustBeABoolean: a quoted "false" is Truthy, so a coerced value would
// ask every launch to send a request the daemon never offered to answer. Refused at both
// strictnesses, like `preamble`.
func TestLaunchCheckValueMustBeABoolean(t *testing.T) {
	for _, val := range []any{"false", "true", 1, ""} {
		manifest := launchCheckManifest(map[string]any{"launch_check": val})
		_, err := decodeMap(t, "checked", manifest)
		if err == nil || !strings.Contains(err.Error(), "host_daemon.launch_check") ||
			!strings.Contains(err.Error(), "boolean") {
			t.Errorf("launch_check %#v: err = %v, want a refusal naming the key and the type", val, err)
		}
		if _, _, terr := loopholedecl.DecodeTolerant(manifestBytes(t, manifest),
			filepath.Join("/loopholes", "checked")); terr == nil {
			t.Errorf("launch_check %#v: the tolerant decode accepted a wrong type", val)
		}
	}
}

// TestLaunchCheckNeedsAFramedLoopbackDaemon: the launch check is one framed request sent
// through the endpoint the launch publishes, so a daemon whose requests end at EOF, or a
// loophole with no loopback-tls endpoint, could never answer it. Each pairing is refused at
// load, naming what to change, instead of failing at every launch as a daemon fault.
func TestLaunchCheckNeedsAFramedLoopbackDaemon(t *testing.T) {
	_, err := decodeMap(t, "checked", launchCheckManifest(map[string]any{
		"launch_check": true, "request_end": "eof"}))
	if err == nil || !strings.Contains(err.Error(), "request_end") {
		t.Errorf("launch_check beside request_end eof: err = %v, want a refusal naming request_end", err)
	}

	noTransport := launchCheckManifest(map[string]any{"launch_check": true})
	noTransport["transport"] = "none"
	_, err = decodeMap(t, "checked", noTransport)
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Errorf("launch_check on transport none: err = %v, want a refusal naming the transport", err)
	}

	// The same daemon without the check still decodes: the refusal is about the pairing.
	if _, err := decodeMap(t, "checked", launchCheckManifest(map[string]any{"request_end": "eof"})); err != nil {
		t.Errorf("request_end eof without launch_check was refused: %v", err)
	}
}

// TestLaunchCheckTypoIsUnknown: the key is in hostDaemonKeys, so a misspelling is loud in a
// strict decode and a skew note in a tolerant one, never a silently absent check.
func TestLaunchCheckTypoIsUnknown(t *testing.T) {
	_, err := decodeMap(t, "checked", launchCheckManifest(map[string]any{"launch_chek": true}))
	if err == nil || !strings.Contains(err.Error(), "host_daemon.launch_chek") {
		t.Errorf("strict decode of a misspelled launch_check: err = %v", err)
	}
	if _, err := decodeMap(t, "checked", launchCheckManifest(map[string]any{"launch_check": true})); err != nil {
		t.Errorf("the correctly spelled key is reported unknown: %v", err)
	}
}
