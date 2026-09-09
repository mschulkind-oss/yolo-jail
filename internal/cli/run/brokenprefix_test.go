package run

// brokenprefix_test.go pins the attach post-mortem: the diagnosis a jail gets
// when its mounted yolo binaries were deleted out from under it.
//
// Three shapes, matching AGENTS.md's call-site rule:
//
//   - the DIAGNOSIS is behavioural, driven through the real Exec seam with a faked
//     `inspect --format {{json .Mounts}}`, including every case where it must stay
//     silent (that half is the one that keeps it from shouting over unrelated
//     failures);
//   - the two host VERDICTS are pinned on the pure text, because the difference
//     between "replaced" and "removed" names a different culprit;
//   - the CALL SITE is AST-pinned, because attachExisting's exec spawns a real
//     runtime and cannot be driven here — deleting the call passes every
//     behavioural test above and returns the raw runc error to the user.

import (
	"encoding/json"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mountsJSON renders a podman `inspect --format {{json .Mounts}}` answer with one
// entry per dest→src pair, in the real shape (the decoder walks `Destination` /
// `Source` on each object).
func mountsJSON(t *testing.T, pairs map[string]string) string {
	t.Helper()
	var mounts []map[string]any
	for dest, src := range pairs {
		mounts = append(mounts, map[string]any{
			"Type": "bind", "Destination": dest, "Source": src, "RW": false,
		})
	}
	b, err := json.Marshal(mounts)
	if err != nil {
		t.Fatalf("marshal mounts: %v", err)
	}
	return string(b)
}

// brokenPrefixOptions builds an Options whose runtime answers one inspect with
// `mounts`, or fails the inspect entirely when mounts is "".
func brokenPrefixOptions(mounts string) *Options {
	return &Options{
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) > 1 && argv[1] == "inspect" && mounts != "" {
				return ExecResult{Ran: true, RC: 0, Stdout: mounts}
			}
			return ExecResult{Ran: false}
		},
	}
}

// TestDiagnoseBrokenPrefixExplainsAReplacedBundle is the reported failure: the
// exec died at 127, the container mounts its prefix bin/ from the host, and a
// yolo-entrypoint sits at that host path RIGHT NOW — which is `just install`
// having restaged over it, since a bind mount follows the inode and not the path.
func TestDiagnoseBrokenPrefixExplainsAReplacedBundle(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "yolo-entrypoint"), []byte("elf"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := brokenPrefixOptions(mountsJSON(t, map[string]string{
		"/workspace":       "/home/matt/code/yolo-jail",
		JailPrefixBinDir:   src,
		JailPrefixShareDir: filepath.Dir(src),
	}))

	msg := o.diagnoseBrokenPrefix("podman", "yolo-ws-abcd1234", 127)
	if msg == "" {
		t.Fatal("a 127 against a container with a prefix mount must be diagnosed, not passed through")
	}
	for _, want := range []string{
		"deleted out from under it",
		src,
		"REPLACED",
		"just install",
		"yolo stop",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnosis must contain %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "store GC") {
		t.Errorf("a replaced bundle must not be blamed on a store GC:\n%s", msg)
	}
}

// TestDiagnoseBrokenPrefixExplainsARemovedPrefix is the other culprit: nothing is
// at the host path now, so the directory was taken away and not put back — a
// store GC of an unrooted install prefix, not a restage.
func TestDiagnoseBrokenPrefixExplainsARemovedPrefix(t *testing.T) {
	src := filepath.Join(t.TempDir(), "nix-store-path-gone", "bin")
	o := brokenPrefixOptions(mountsJSON(t, map[string]string{JailPrefixBinDir: src}))

	msg := o.diagnoseBrokenPrefix("podman", "yolo-ws-abcd1234", 127)
	if !strings.Contains(msg, "store GC") {
		t.Errorf("an absent host path must name the GC, not a restage:\n%s", msg)
	}
	if strings.Contains(msg, "REPLACED") {
		t.Errorf("nothing is at the path — it cannot have been replaced:\n%s", msg)
	}
}

// TestDiagnoseBrokenPrefixStaysSilent is the half that keeps this from being
// noise. Each case is a real state a user reaches, and in every one the runtime's
// own error is the whole truth — an explanation on top of it would be a confident
// wrong answer.
func TestDiagnoseBrokenPrefixStaysSilent(t *testing.T) {
	src := t.TempDir()
	good := mountsJSON(t, map[string]string{JailPrefixBinDir: src})

	cases := []struct {
		name   string
		rc     int
		mounts string
		why    string
	}{
		{"the command itself exited non-zero", 1, good,
			"rc 1 is the jailed command's own failure — pid1 ran fine"},
		{"a clean exit", 0, good,
			"nothing failed"},
		{"the inspect cannot run", 127, "",
			"the container may be gone; guessing would explain someone else's error"},
		{"a jail older than the mounted prefix", 127,
			mountsJSON(t, map[string]string{"/workspace": "/home/matt/code/yolo-jail"}),
			"no prefix mount means this failure is not that failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := brokenPrefixOptions(tc.mounts)
			if msg := o.diagnoseBrokenPrefix("podman", "yolo-ws-abcd1234", tc.rc); msg != "" {
				t.Errorf("must stay silent (%s), got:\n%s", tc.why, msg)
			}
		})
	}
}

// TestAttachExistingDiagnosesAFailedExec is the call-site pin. attachExisting's
// exec spawns a real runtime, so the behavioural tests above cannot reach through
// it; without this, deleting the one line that calls the diagnosis leaves every
// test in this file green and every user back on the bare runc error.
func TestAttachExistingDiagnosesAFailedExec(t *testing.T) {
	fn := methodDecl(t, "run.go", "attachExisting")
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "diagnoseBrokenPrefix" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("attachExisting no longer calls diagnoseBrokenPrefix. A jail whose mounted " +
			"binaries were deleted out from under it would then report only the runtime's " +
			"'stat /opt/yolo-jail/bin/yolo-entrypoint: no such file or directory', which names " +
			"a missing file and not the restart it needs. If the call moved, move this pin.")
	}
}
