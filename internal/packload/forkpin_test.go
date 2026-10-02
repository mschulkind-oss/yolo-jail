package packload

// forkpin_test.go pins LoadForkPins, the reader of the fork lock FILE every caller that must not
// fetch shares (forked-programs-as-packs.md FP-D7): what each fork's pin is, and that a lock which
// cannot be read pins nothing for anyone; and PinForks' lines (FP-D18).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestLoadForkPinsReadsEachForksPinAndABrokenLockPinsNothing(t *testing.T) {
	pinned := Fork{Pack: "forkpack", Base: "base", Bin: "tool", Source: "git+https://example.invalid/tool?ref=main"}
	unpinned := Fork{Pack: "forkpack", Base: "base", Bin: "other", Source: "git+https://example.invalid/other?ref=main"}
	forks := []Fork{pinned, unpinned}
	path := filepath.Join(t.TempDir(), "forks.lock.json")
	commit := strings.Repeat("a", 40)

	if pins := LoadForkPins(nil, path); pins != nil {
		t.Errorf("no forks: %+v, want nil", pins)
	}
	// No lock file yet: every fork is unpinned and pinnable, and says the next launch pins it.
	for _, p := range LoadForkPins(forks, path) {
		if p.Commit != "" || !p.Pinnable || !strings.Contains(p.Reason, "the next launch pins it") {
			t.Errorf("no lock file: %+v", p)
		}
	}

	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: pinned.Key(), Source: pinned.Source, Ref: "main", Commit: commit})
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	pins := LoadForkPins(forks, path)
	if len(pins) != 2 || pins[0].Commit != commit || pins[0].Ref != "main" || pins[0].Reason != "" ||
		pins[0].Pinnable || pins[1].Commit != "" || pins[1].Reason == "" || !pins[1].Pinnable {
		t.Fatalf("a lock pinning one of two forks: %+v", pins)
	}

	// A LOCK THAT CANNOT BE READ PINS NOTHING, even the fork it named, and says why for each.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins = LoadForkPins(forks, path)
	if len(pins) != 2 {
		t.Fatalf("a broken lock: %+v, want one answer per fork", pins)
	}
	for i, p := range pins {
		if p.Fork.Key() != forks[i].Key() || p.Commit != "" || p.Pinnable ||
			!strings.HasPrefix(p.Reason, "the fork lock cannot be read (") || !strings.Contains(p.Reason, path) {
			t.Errorf("a broken lock: %+v, want no pin, nothing pinnable, and the read error as its reason", p)
		}
	}
}

// PinForks' answers as a caller prints them (FP-D18): the pin it made with its one disclosure line,
// and a pin it could not make with the failure and the next step, never a pack's next step.
func TestPinForksSaysWhatItPinnedAndWhyItCouldNot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "build.sh"), []byte("# first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	good := Fork{Pack: "forkpack", Base: "base", Bin: "tool", Source: "git+file://" + repo + "?ref=main"}
	bad := Fork{Pack: "forkpack", Base: "base", Bin: "other",
		Source: "git+file://" + filepath.Join(t.TempDir(), "absent") + "?ref=main"}
	lockPath := filepath.Join(t.TempDir(), "forks.lock.json")
	pins := PinForks([]Fork{good, bad}, lockPath, &packsrc.Store{Dir: t.TempDir()}, nil)
	if len(pins) != 2 || pins[0].Commit == "" || !pins[0].Pinned {
		t.Fatalf("pins = %+v, want the good fork pinned", pins)
	}
	if want := "pinned fork forkpack/tool at " + pins[0].Commit[:8] + " (" + good.Source +
		"); `yolo pack update` moves it"; pins[0].PinnedLine() != want {
		t.Errorf("PinnedLine() = %q, want %q", pins[0].PinnedLine(), want)
	}
	reason := pins[1].Reason
	if pins[1].Commit != "" || !strings.HasPrefix(reason, "it has no pin, and pinning it failed (") ||
		!strings.Contains(reason, "fix what that names and launch again") || strings.Contains(reason, "never been fetched") {
		t.Errorf("the unpinnable fork's reason = %q", reason)
	}
	// Pinned once: the second call finds it standing.
	if again := PinForks([]Fork{good}, lockPath, &packsrc.Store{Dir: t.TempDir()}, nil); again[0].Pinned ||
		again[0].Commit != pins[0].Commit {
		t.Errorf("a second PinForks = %+v, want the standing pin, not a new one", again[0])
	}
}
