package run

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestParsePortForwards(t *testing.T) {
	entries := []any{
		jsonx.IntValue(8000), // int -> (8000,8000)
		"9000",               // plain string -> (9000,9000)
		"1234:5678",          // mapped -> (1234,5678)
		[]any{"nope"},        // invalid -> warned + skipped
	}
	var warnings []string
	got, err := ParsePortForwards(entries, func(s string) { warnings = append(warnings, s) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []PortForward{{LocalPort: 8000, HostPort: 8000}, {LocalPort: 9000, HostPort: 9000}, {LocalPort: 1234, HostPort: 5678}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed = %v, want %v", got, want)
	}
	if len(warnings) != 1 || warnings[0] != "Warning: invalid port forward entry: [nope]" {
		t.Errorf("warnings = %v", warnings)
	}
	// Non-numeric string aborts with an error.
	if _, err := ParsePortForwards([]any{"abc"}, nil); err == nil {
		t.Error("non-numeric string should error")
	}
	// split-once: "1:2:3" -> a="1", b="2:3" which is non-numeric -> error.
	if _, err := ParsePortForwards([]any{"1:2:3"}, nil); err == nil {
		t.Error("'1:2:3' should error on the non-numeric second half")
	}
}

func TestSocatArgv(t *testing.T) {
	got := SocatArgv("/tmp/yolo-fwd/port-8000.sock", 8000)
	want := []string{
		"socat",
		"UNIX-LISTEN:/tmp/yolo-fwd/port-8000.sock,fork,mode=777",
		"TCP:127.0.0.1:8000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
	}
}

func TestSocketPath(t *testing.T) {
	if got := SocketPath("/tmp/yolo-fwd", 8000); got != "/tmp/yolo-fwd/port-8000.sock" {
		t.Errorf("sock path = %q", got)
	}
}

func TestSocketNotReadyWarning(t *testing.T) {
	want := "Warning: socat socket(s) not ready after 2.0s: /a.sock, /b.sock"
	if got := SocketNotReadyWarning([]string{"/a.sock", "/b.sock"}); got != want {
		t.Errorf("warning = %q", got)
	}
}

// A FORWARDED PORT'S SOCKET FITS AF_UNIX FOR THE LONGEST JAIL NAME (review m1): a setup-qualified
// jail name can run to 87 bytes, and Apple Container publishes each port's host socket beneath the
// per-jail directory, whose path on macOS (/private/tmp/...) must stay under the 104-byte limit.
// Pinned through the launch's own argv builder (forwardHostPortsArgs).
func TestAForwardedPortsSocketFitsTheUnixLimitForALongJailName(t *testing.T) {
	cname := "yolo-" + strings.Repeat("w", 40) + "-0123abcd-" + strings.Repeat("s", 32)
	o := &Options{IsMacOS: true}
	args := o.forwardHostPortsArgs("container", cname, []any{"65535"})
	i := slices.Index(args, "--publish-socket")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("no --publish-socket in %v", args)
	}
	host, _, _ := strings.Cut(args[i+1], ":")
	if len(host) >= 104 {
		t.Errorf("the forwarded port's host socket %s is %d bytes, past the macOS AF_UNIX limit of 104", host, len(host))
	}
}
