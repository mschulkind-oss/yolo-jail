package selfupdate

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStateFresh(t *testing.T) {
	ch := Channel{Kind: KindHomebrew, Version: "0.10.0"}
	now := fixedNow()
	ok := State{Identity: ch.Identity(), CheckedAt: now.Add(-time.Hour)}
	if !ok.Fresh(ch, now) {
		t.Error("an hour-old successful check must be fresh")
	}
	if (State{Identity: ch.Identity(), CheckedAt: now.Add(-25 * time.Hour)}).Fresh(ch, now) {
		t.Error("a check older than Interval must be stale")
	}
	if !(State{Identity: ch.Identity(), CheckedAt: now.Add(-2 * time.Hour), Error: "offline"}).Fresh(ch, now) {
		t.Error("a failed check must still be fresh within the once-daily interval")
	}
	if (State{Identity: ch.Identity(), CheckedAt: now.Add(-25 * time.Hour), Error: "offline"}).Fresh(ch, now) {
		t.Error("a failed check must go stale after the once-daily interval")
	}
	if (State{Identity: "homebrew:0.9.0", CheckedAt: now}).Fresh(ch, now) {
		t.Error("a check for a different binary is never fresh")
	}
	if (State{}).Fresh(ch, now) {
		t.Error("the zero State is never fresh")
	}
}

func TestStateUpdateFor(t *testing.T) {
	ch := Channel{Kind: KindHomebrew, Version: "0.11.0"}
	// The cache said "0.11.0 is available" to the 0.10.0 binary; the binary is
	// now 0.11.0, so the notice must not survive the update.
	old := State{Identity: "homebrew:0.10.0", Available: true, Latest: "0.11.0"}
	if old.UpdateFor(ch) {
		t.Error("a cached update for the previous binary must not show after updating")
	}
	if !(State{Identity: ch.Identity(), Available: true}).UpdateFor(ch) {
		t.Error("an update cached for this binary must show")
	}
}

func TestShouldPrompt(t *testing.T) {
	if (State{Available: true, Latest: "0.11.0", DeclinedFor: "0.11.0"}).ShouldPrompt() {
		t.Error("a declined version must not be offered again")
	}
	if !(State{Available: true, Latest: "0.12.0", DeclinedFor: "0.11.0"}).ShouldPrompt() {
		t.Error("something newer than the declined version must be offered")
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "update-check.json")
	if got := LoadState(path); got != (State{}) {
		t.Errorf("a missing file must load as the zero State, got %+v", got)
	}
	ch := Channel{Kind: KindSource, SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"}
	want := State{CheckedAt: fixedNow(), Identity: ch.Identity(), Kind: KindSource, Current: "cfefa8bf",
		Latest: "abc1234", Upstream: "origin/main", Behind: 12, Available: true}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}

	if got := LoadState(path); got != want {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(path); got != (State{}) {
		t.Errorf("a corrupt file must load as the zero State, got %+v", got)
	}
}

func TestInvalidateStatePreservesDisclosureOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := SaveState(path, State{
		CheckedAt: fixedNow(), Identity: "homebrew:0.10.0",
		Available: true, Latest: "0.11.0", Disclosed: true, DeclinedFor: "0.11.0",
	}); err != nil {
		t.Fatal(err)
	}
	if err := InvalidateState(path); err != nil {
		t.Fatal(err)
	}
	if got, want := LoadState(path), (State{Disclosed: true}); got != want {
		t.Errorf("InvalidateState = %+v, want %+v", got, want)
	}
}

func TestAcquireLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "update-check.json.lock")
	now := time.Now()
	if !AcquireLock(lock, now) {
		t.Fatal("the first acquire must succeed")
	}
	if AcquireLock(lock, now) {
		t.Error("a held lock must refuse a second check")
	}
	if !AcquireLock(lock, now.Add(lockTTL+time.Minute)) {
		t.Error("a lock older than lockTTL belongs to a dead check and must be taken over")
	}
}

func TestPlan(t *testing.T) {
	cases := []struct {
		ch   Channel
		want []string
	}{
		{Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail"}, []string{
			"git pull --ff-only   (in /src/yolo-jail)",
			// YOLO_INSTALL_KEEP_TREE: the deploy builds upstream's tree as pulled and never
			// re-pins an official pack program into it, so the checkout stays clean and an
			// autostash pops back onto it (docs/design/broker-as-a-pack.md BP-D26).
			"GOBIN=/home/u/.local/bin YOLO_INSTALL_KEEP_TREE=1 just deploy   (in /src/yolo-jail)",
		}},
		// Without HOMEBREW_NO_INSTALL_CLEANUP=1, brew deletes the old keg that
		// running jails bind-mount their binaries from.
		{Channel{Kind: KindHomebrew, Exe: "/opt/homebrew/Cellar/yolo-jail/0.10.0/bin/yolo"}, []string{"HOMEBREW_NO_INSTALL_CLEANUP=1 brew upgrade yolo-jail"}},
	}
	for _, c := range cases {
		steps, err := Plan(c.ch)
		if err != nil {
			t.Errorf("%s: %v", c.ch.Kind, err)
			continue
		}
		var got []string
		for _, s := range steps {
			got = append(got, s.String())
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s plan:\n got %q\nwant %q", c.ch.Kind, got, c.want)
		}
	}
	for _, k := range []Kind{KindGoInstall, KindPipx, KindUV} {
		if _, err := Plan(Channel{Kind: k}); err == nil ||
			!strings.Contains(err.Error(), "YOLO_REPO_ROOT") ||
			!strings.Contains(err.Error(), "version skew") {
			t.Errorf("%s: want a coordinated-update refusal, got %v", k, err)
		}
	}
	for _, k := range []Kind{KindArchive, KindUnknown} {
		if _, err := Plan(Channel{Kind: k}); err == nil || !strings.Contains(err.Error(), ReleasesPage) {
			t.Errorf("%s: want an error pointing at the releases page, got %v", k, err)
		}
	}
}

func applyGit(dirty string) *fakeGit {
	return &fakeGit{answers: map[string]string{
		"symbolic-ref --short -q HEAD": "main",
		"status --porcelain":           dirty,
	}}
}

var sourceChannel = Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main"}

// recorder is a Runner that records each step and fails the ones named in fail.
type recorder struct {
	ran  []string
	fail map[string]bool
}

func (r *recorder) run(_ context.Context, s Step, _, _ io.Writer) error {
	key := strings.Join(s.Argv, " ")
	r.ran = append(r.ran, key)
	if r.fail[key] {
		return os.ErrPermission
	}
	return nil
}

func TestApplyRefusesADirtyCheckoutWithoutAutostash(t *testing.T) {
	r := &recorder{}
	err := Apply(context.Background(), sourceChannel, ApplyOptions{}, applyGit(" M internal/cli/cli.go").run, r.run, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") || !strings.Contains(err.Error(), "--autostash") {
		t.Errorf("want an uncommitted-changes refusal naming --autostash, got %v", err)
	}
	if len(r.ran) != 0 {
		t.Errorf("ran %v against a dirty checkout", r.ran)
	}
}

func TestApplyRefusesASwitchedBranch(t *testing.T) {
	g := applyGit("")
	g.answers["symbolic-ref --short -q HEAD"] = "feat/x"
	r := &recorder{}
	err := Apply(context.Background(), sourceChannel, ApplyOptions{}, g.run, r.run, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `on "feat/x"`) {
		t.Errorf("want a branch refusal, got %v", err)
	}
	if len(r.ran) != 0 {
		t.Errorf("ran %v on the wrong branch", r.ran)
	}
}

func TestApplyRunsEveryStepInOrderAndStopsAtAFailure(t *testing.T) {
	r := &recorder{}
	if err := Apply(context.Background(), sourceChannel, ApplyOptions{}, applyGit("").run, r.run, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	if strings.Join(r.ran, ",") != "git pull --ff-only,just deploy" {
		t.Errorf("ran %v, want pull then deploy", r.ran)
	}

	r = &recorder{fail: map[string]bool{"git pull --ff-only": true}}
	if err := Apply(context.Background(), sourceChannel, ApplyOptions{}, applyGit("").run, r.run, io.Discard, io.Discard); err == nil {
		t.Error("a failing step must fail the update")
	}
	if len(r.ran) != 1 {
		t.Errorf("ran %v after a failure, want only the first step", r.ran)
	}

	r = &recorder{}
	if err := Apply(context.Background(), sourceChannel, ApplyOptions{SkipPull: true}, applyGit("").run, r.run, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.ran, ",") != "just deploy" {
		t.Errorf("skip-pull ran %v, want only deploy", r.ran)
	}
}

func TestRunStepDropsInheritedGitState(t *testing.T) {
	t.Setenv("GIT_DIR", "/tmp/not-the-source-repo")
	var stdout strings.Builder
	step := Step{Argv: []string{"sh", "-c", `printf %s "${GIT_DIR-unset}"`}}
	if err := RunStep(context.Background(), step, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "unset" {
		t.Errorf("GIT_DIR reached the update command as %q", got)
	}
}

func TestApplyAutostashDeploysACleanBuildAndRestoresTheEdits(t *testing.T) {
	stash := "git stash push --include-untracked -m " + AutostashMessage
	r := &recorder{}
	if err := Apply(context.Background(), sourceChannel, ApplyOptions{Autostash: true}, applyGit(" M x.go").run, r.run, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := stash + ",git pull --ff-only,just deploy,git stash pop"
	if strings.Join(r.ran, ",") != want {
		t.Errorf("ran %v\nwant %s", r.ran, want)
	}

	// A clean checkout makes no stash, so there is nothing to pop.
	r = &recorder{}
	_ = Apply(context.Background(), sourceChannel, ApplyOptions{Autostash: true}, applyGit("").run, r.run, io.Discard, io.Discard)
	if slices.Contains(r.ran, "git stash pop") {
		t.Errorf("popped a stash it never made: %v", r.ran)
	}

	// A failed update still gives the edits back.
	r = &recorder{fail: map[string]bool{"just deploy": true}}
	err := Apply(context.Background(), sourceChannel, ApplyOptions{Autostash: true}, applyGit(" M x.go").run, r.run, io.Discard, io.Discard)
	if err == nil || r.ran[len(r.ran)-1] != "git stash pop" {
		t.Errorf("err=%v ran=%v, want the failure AND a final pop", err, r.ran)
	}

	// A pop that fails says where the edits are.
	r = &recorder{fail: map[string]bool{"git stash pop": true}}
	err = Apply(context.Background(), sourceChannel, ApplyOptions{Autostash: true}, applyGit(" M x.go").run, r.run, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "git stash list") {
		t.Errorf("want the stash named in the error, got %v", err)
	}
}

func TestNotice(t *testing.T) {
	cases := []struct {
		st   State
		want string
	}{
		{State{Kind: KindSource, Behind: 12, Upstream: "origin/main"},
			"⬆ yolo-jail: 12 new commits on origin/main since this build — run `yolo update`"},
		{State{Kind: KindSource, Behind: 1, Upstream: "origin/main"},
			"⬆ yolo-jail: 1 new commit on origin/main since this build — run `yolo update`"},
		{State{Kind: KindHomebrew, Latest: "0.11.0", Current: "0.10.0"},
			"⬆ yolo-jail 0.11.0 is available (this is 0.10.0) — run `yolo update`"},
		{State{Kind: KindGoInstall, Latest: "0.11.0", Current: "0.10.0"},
			"⬆ yolo-jail 0.11.0 is available (this is 0.10.0) — update the host binary and its separate jail source together"},
		{State{Kind: KindArchive, Latest: "0.11.0", Current: "0.10.0"},
			"⬆ yolo-jail 0.11.0 is available (this is 0.10.0) — download it from " + ReleasesPage},
	}
	for _, c := range cases {
		if got := Notice(c.st); got != c.want {
			t.Errorf("Notice(%+v)\n got %q\nwant %q", c.st, got, c.want)
		}
	}
}
