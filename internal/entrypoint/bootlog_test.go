package entrypoint

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bootLogEnv is a jail Env whose workspace is a temp dir, so the log lands somewhere
// the test owns.
func bootLogEnv(t *testing.T, vars map[string]string) (*Env, string) {
	t.Helper()
	ws := t.TempDir()
	if vars == nil {
		vars = map[string]string{}
	}
	return &Env{Workspace: ws, Vars: vars}, ws
}

func readBootLog(t *testing.T, ws, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".yolo", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// The whole point: what the boot says has to still be there after the boot.
func TestBootLogCapturesWhatTheBootSaid(t *testing.T) {
	e, ws := bootLogEnv(t, map[string]string{
		"YOLO_VERSION":       "0.8.0+253.gd56b127",
		"YOLO_HOST_LOOPBACK": "requested",
	})

	var term bytes.Buffer
	blog := attachBootLog(e, &term)
	e.warn("Warning: something the reader needs tomorrow")
	blog.finish(nil)

	got := readBootLog(t, ws, bootLogName)
	for _, want := range []string{
		"Warning: something the reader needs tomorrow",
		"YOLO_VERSION=0.8.0+253.gd56b127",
		"YOLO_HOST_LOOPBACK=requested",
		"boot complete",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("boot log missing %q\n--- log ---\n%s", want, got)
		}
	}

	// The terminal must still get everything: the log is an ADDITION, and a user
	// watching a launch may not go looking for a file.
	if !strings.Contains(term.String(), "something the reader needs tomorrow") {
		t.Errorf("the warning did not reach the terminal:\n%s", term.String())
	}
}

// attachBootLog wires TWO sinks with different reach, and the difference is the
// feature: Stderr is terminal+file, LogOnly is file only. Nothing else in the tree
// tests that split at the wiring — the reachability tests set both fields
// themselves — so pointing LogOnly at the terminal here would silently put a line on
// every healthy launch, which is exactly what the split exists to prevent.
func TestBootLogKeepsTheLogOnlySinkOffTheTerminal(t *testing.T) {
	e, ws := bootLogEnv(t, nil)

	var term bytes.Buffer
	blog := attachBootLog(e, &term)
	e.warn("this belongs in BOTH")
	e.note("this belongs in the LOG ONLY")
	blog.finish(nil)

	got := readBootLog(t, ws, bootLogName)
	if !strings.Contains(got, "this belongs in the LOG ONLY") {
		t.Errorf("the log-only line never reached the log:\n%s", got)
	}
	if !strings.Contains(got, "this belongs in BOTH") {
		t.Errorf("the warning never reached the log:\n%s", got)
	}
	if strings.Contains(term.String(), "LOG ONLY") {
		t.Errorf("the log-only line LEAKED to the terminal:\n%s", term.String())
	}
	if !strings.Contains(term.String(), "this belongs in BOTH") {
		t.Errorf("the warning did not reach the terminal:\n%s", term.String())
	}
}

// A refused boot is the case the log exists for — there is no jail left to ask.
func TestBootLogRecordsARefusedBoot(t *testing.T) {
	e, ws := bootLogEnv(t, nil)

	blog := attachBootLog(e, &bytes.Buffer{})
	e.warn("host services unreachable from inside the jail: claude-oauth-broker")
	blog.finish(errors.New("refusing to start the jail: 1 config generator(s) failed"))

	got := readBootLog(t, ws, bootLogName)
	if !strings.Contains(got, "BOOT REFUSED") {
		t.Errorf("a refused boot must say so:\n%s", got)
	}
	if !strings.Contains(got, "refusing to start the jail") {
		t.Errorf("the refusal must carry its own reason:\n%s", got)
	}
	if strings.Contains(got, "boot complete") {
		t.Errorf("a refused boot must not claim completion:\n%s", got)
	}
}

// The natural response to a broken jail is to launch it again. Without rotation
// that second launch destroys the only record of the first.
func TestBootLogKeepsOnePriorBoot(t *testing.T) {
	e, ws := bootLogEnv(t, nil)

	blog := attachBootLog(e, &bytes.Buffer{})
	e.warn("FIRST BOOT, the one that broke")
	blog.finish(errors.New("boom"))

	blog2 := attachBootLog(e, &bytes.Buffer{})
	e.warn("SECOND BOOT, the retry")
	blog2.finish(nil)

	if cur := readBootLog(t, ws, bootLogName); !strings.Contains(cur, "SECOND BOOT") {
		t.Errorf("current log is not the current boot:\n%s", cur)
	}
	prev := readBootLog(t, ws, bootLogPrevName)
	if !strings.Contains(prev, "FIRST BOOT, the one that broke") {
		t.Errorf("the previous boot was lost — this is the evidence a retry destroys:\n%s", prev)
	}
	if !strings.Contains(prev, "boom") {
		t.Errorf("the previous boot's refusal was lost:\n%s", prev)
	}
}

// Absent facts are recorded as absent. "yolo decided nothing about host loopback"
// has consequences (it is the value that can never escalate a service failure), so
// a reader must be able to tell it from "not recorded".
func TestBootLogNamesTheFactsItDoesNotHave(t *testing.T) {
	e, ws := bootLogEnv(t, map[string]string{"YOLO_VERSION": "0.8.0"})

	blog := attachBootLog(e, &bytes.Buffer{})
	blog.finish(nil)

	got := readBootLog(t, ws, bootLogName)
	if !strings.Contains(got, "unset:") || !strings.Contains(got, "YOLO_HOST_LOOPBACK") {
		t.Errorf("an unset launch fact must be named as unset:\n%s", got)
	}
}

// A logger that can stop a boot is worse than the blindness it fixes.
//
// Every case here fails with ENOTDIR rather than EACCES, DELIBERATELY: the
// entrypoint runs as UID 0 and so does this suite, and root ignores permission bits.
// A read-only-directory case would SKIP in the only environment that runs it, which
// is the same as not having written it — and this is the one property in the file
// whose failure mode is "no jail at all".
func TestBootLogNeverCostsTheBoot(t *testing.T) {
	fileNotDir := filepath.Join(t.TempDir(), "i-am-a-file")
	if err := os.WriteFile(fileNotDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, ".yolo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		ws   string
	}{
		{"a workspace that is a file", fileNotDir},
		{"a .yolo that is a file", occupied},
		{"a workspace under a file", filepath.Join(fileNotDir, "nope", "deeper")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Env{Workspace: tc.ws, Vars: map[string]string{}}

			var term bytes.Buffer
			blog := attachBootLog(e, &term)
			if e.Stderr == nil {
				t.Fatal("attachBootLog left e.Stderr nil — the boot would panic writing to it")
			}
			if blog != nil {
				t.Fatalf("expected no log for %s, got one", tc.name)
			}
			// The nil *bootLog must be safe to use: Main calls finish unconditionally.
			e.warn("this still has to reach the terminal")
			blog.finish(errors.New("and this must not panic"))

			if !strings.Contains(term.String(), "this still has to reach the terminal") {
				t.Errorf("stderr lost its output when the log was unavailable:\n%s", term.String())
			}
		})
	}
}

// Main() cannot be called from a test — it ends in execBash, which replaces the
// process — so the one thing no runtime test here can reach is whether the boot
// path actually USES any of this. That gap is not theoretical: an earlier draft of
// this file passed in full against a boot.go that opened the log and never wired it,
// producing a correct-looking, permanently empty log.
//
// attachBootLog now installs e.Stderr itself, so "opened but not wired" is
// unrepresentable. What remains is "not called at all", and this pins it the way
// shippedclients_test.go pins the ship set: by reading the source that no test can
// execute. Brittle by construction, and cheaper than the failure it prevents — a
// jail whose boot log is silently missing exactly when someone needs it.
func TestBootPathActuallyWiresTheLog(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "entrypoint", "boot.go"))
	if err != nil {
		t.Fatalf("reading boot.go: %v", err)
	}
	got := string(src)

	for _, want := range []struct{ frag, why string }{
		{"attachBootLog(e, os.Stderr)",
			"the boot must install the tee; without this the log is never opened"},
		{"blog.finish(err)",
			"a REFUSED boot must be recorded — it is the case with no jail left to ask"},
		{"blog.finish(nil)",
			"a successful boot must close the log BEFORE execBash, which never returns"},
	} {
		if !strings.Contains(got, want.frag) {
			t.Errorf("boot.go no longer contains %q\n  why it matters: %s", want.frag, want.why)
		}
	}

	// Ordering: the close on the success path has to precede the exec, or it never
	// runs. Proximity is not the property, but "appears before" is necessary.
	fin := strings.Index(got, "blog.finish(nil)")
	exec := strings.Index(got, "return execBash(e, command)")
	if fin < 0 || exec < 0 || fin > exec {
		t.Error("blog.finish(nil) must precede the execBash that replaces this process")
	}
}
