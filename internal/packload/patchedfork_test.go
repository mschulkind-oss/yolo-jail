package packload

// patchedfork_test.go pins the pack-set half of a PATCHED fork (docs/design/patched-forks.md): the
// fork carries its series and follow rule and its pack's root, the rewrite hands both to the base's
// program, the footprint names the series, and no pinner — the launch's, the host floor's or
// `yolo capture`'s, all through PinForks — ever pins one (PF-D16).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func patchedForkContribution() packdecl.Contribution {
	c := forkContribution("pi", "pi")
	c.Source, c.Patches, c.Follow = "git+https://example.invalid/upstream/pi?ref=main", "patches", "head"
	return c
}

func TestAPatchedForkCarriesItsSeriesThroughTheRewrite(t *testing.T) {
	base := claimPack(t, "pi",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "pi", Via: "npm", Package: "pi-coding-agent"})
	fork := claimPack(t, "pi-fork", patchedForkContribution())
	forks := Forks([]*Pack{base, fork})
	if len(forks) != 1 || !forks[0].Patched() || forks[0].Patches != "patches" || forks[0].Follow != "head" ||
		forks[0].Root != fork.Root {
		t.Fatalf("Forks = %+v, want the patched fork with its series, follow rule and pack root", forks)
	}
	out, err := ApplyForks([]*Pack{base, fork})
	if err != nil {
		t.Fatal(err)
	}
	in := out[0].Decl.InstallContributions()[0]
	if !in.IsPatchedFork() || in.Patches != "patches" || in.Follow != "head" || in.ForkedBy != "pi-fork" {
		t.Errorf("the rewritten base program = %+v, want it to carry the patched fork", in)
	}
}

// NO PINNER PINS A PATCHED FORK: PinForks returns its reason and writes no lock entry, even beside a
// plain fork it does pin — with a git that does not exist, so a patched fork reaching the resolver
// would fail rather than pass. ForkPins, the readers' half, ignores a plain-fork entry left under
// its key.
func TestNoPinnerPinsAPatchedFork(t *testing.T) {
	patched := Fork{Pack: "pi-fork", Base: "pi", Bin: "pi", Source: "git+https://example.invalid/pi?ref=main",
		Patches: "patches"}
	lockPath := filepath.Join(t.TempDir(), packsrc.ForkLockName)
	pins := PinForks([]Fork{patched}, lockPath, &packsrc.Store{Dir: t.TempDir(), Git: "/nonexistent/git"},
		func() (func(string), func()) {
			t.Error("PinForks began a pin for a patched fork")
			return nil, func() {}
		})
	if len(pins) != 1 || pins[0].Commit != "" || pins[0].Pinned || pins[0].Pinnable ||
		pins[0].Reason != PatchedForkPinReason {
		t.Errorf("PinForks of a patched fork = %+v, want no pin and its reason", pins)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("PinForks wrote the fork lock for a patched fork (err %v)", err)
	}

	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: patched.Key(), Source: patched.Source, Ref: "main", Commit: strings.Repeat("a", 40)})
	for _, p := range ForkPins([]Fork{patched}, l) {
		if p.Commit != "" || p.Pinnable || p.Reason != PatchedForkPinReason {
			t.Errorf("a plain-fork entry under a patched fork's key read as %+v, want it ignored", p)
		}
	}
}

// THE FOOTPRINT NAMES THE SERIES (§7): its directory, its follow rule, and the patch count and
// digest when it reads, and its sentence says it follows the upstream rather than a pin.
func TestAPatchedForkFootprintNamesTheSeries(t *testing.T) {
	fork := claimPack(t, "pi-fork", patchedForkContribution())
	dir := filepath.Join(fork.Root, "patches")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	patch := "From " + strings.Repeat("1", 40) + " Mon Sep 17 00:00:00 2001\nSubject: x\n\n---\n" +
		"diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-a\n+b\n\nbase-commit: " + strings.Repeat("2", 40) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "0001-x.patch"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	var claim *Claim
	fp := FootprintOf(fork)
	for i := range fp.Claims {
		if fp.Claims[i].Kind == packdecl.KindProgram {
			claim = &fp.Claims[i]
		}
	}
	if claim == nil {
		t.Fatal("no program claim")
	}
	for _, w := range []string{"1 patch in patches (series ", "following head", "example.invalid/upstream/pi"} {
		if !strings.Contains(claim.Detail, w) {
			t.Errorf("the claim's detail lacks %q: %s", w, claim.Detail)
		}
	}
	if s := claim.DisclosureSentence(); !strings.Contains(s, "checked at most hourly") || strings.Contains(s, "pinned") {
		t.Errorf("the disclosure says a patched fork is pinned, or not that it follows: %s", s)
	}
}
