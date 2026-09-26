package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// attachNoColorCname is the container the attach tests exec into.
const attachNoColorCname = "yolo-attach-nocolor"

// attachExecArgv drives the real attachExisting through to its exec and returns the argv
// the runtime received: the words after `podman`, as a fake `podman` first on PATH
// recorded them. invocation is this `yolo`'s environment (o.Getenv), and frozenEnv is
// what `inspect` reports as the running container's launch environment, one K=V per line.
//
// DRIVEN, not read: an earlier cut pinned the attach arm by finding the noColorEnvArgs
// call in attachExisting's source, which stayed green when the result was discarded or
// appended after the argv was built.
func attachExecArgv(t *testing.T, invocation map[string]string, frozenEnv string) []string {
	t.Helper()
	home := packHome(t)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	o.Getenv = func(k string) string { return invocation[k] }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			return ExecResult{Ran: true, RC: 0, Stdout: frozenEnv}
		}
		return ExecResult{Ran: false}
	}
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shquote.Quote(record) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	cfg := newConfig()
	channel := channelFor(t, o, cfg, nil, nil)
	if rc := o.attachExisting(attachNoColorCname, "podman", "true", cfg, stagedPacks{}, channel, false); rc != 0 {
		t.Fatalf("the attach arm did not run through to its exec (rc=%d):\n%s", rc, o.Stdout)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the fake runtime never ran, so this test proves nothing about the exec: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// execEnvBeforeContainer returns the `-e` values the exec argv sets before the container
// name: the environment the runtime gives the attached command. A pair after the name
// would be an argument to the entrypoint, not environment.
func execEnvBeforeContainer(t *testing.T, argv []string) []string {
	t.Helper()
	end := slices.Index(argv, attachNoColorCname)
	if end < 0 {
		t.Fatalf("the exec argv never names the container: %q", argv)
	}
	var env []string
	for i := 0; i+1 < end; i++ {
		if argv[i] == "-e" {
			env = append(env, argv[i+1])
		}
	}
	return env
}

// TestAttachExecCarriesNoColor: a running container's environment is the one it was
// launched with, so a NO_COLOR set when attaching must ride the exec itself, as
// `-e NO_COLOR=<value>` before the container name. The unset run is the control: an
// attach without NO_COLOR, to a jail launched without it, adds nothing.
func TestAttachExecCarriesNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor string
		want    []string
	}{{"1", []string{"NO_COLOR=1"}}, {"", nil}} {
		argv := attachExecArgv(t, map[string]string{"NO_COLOR": tc.noColor}, "YOLO_VERSION=9.9.9-test\n")
		var got []string
		for _, e := range execEnvBeforeContainer(t, argv) {
			if strings.HasPrefix(e, "NO_COLOR=") {
				got = append(got, e)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("NO_COLOR=%q at attach: the exec sets %q, want %q; argv: %q",
				tc.noColor, got, tc.want, argv)
		}
	}
}
