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
	ch := Channel{Kind: KindHomebrew, Version: "0.10.0"}
	want := State{CheckedAt: fixedNow(), Identity: ch.Identity(), Kind: KindHomebrew, Current: "0.10.0",
		Latest: "0.11.0", Available: true}
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
			!strings.Contains(err.Error(), "just build-dev-bundle") ||
			!strings.Contains(err.Error(), "just install") {
			t.Errorf("%s: want a coordinated-update refusal, got %v", k, err)
		}
	}
	for _, k := range []Kind{KindArchive, KindUnknown} {
		if _, err := Plan(Channel{Kind: k}); err == nil || !strings.Contains(err.Error(), ReleasesPage) {
			t.Errorf("%s: want an error pointing at the releases page, got %v", k, err)
		}
	}
	// A snapshot has no channel to update through: its plan is the two steps its owner takes,
	// never the releases page or "cannot tell how this yolo was installed".
	_, err := Plan(Channel{Kind: KindSnapshot})
	if err == nil || !strings.Contains(err.Error(), "`just install` in your yolo-jail checkout") ||
		!strings.Contains(err.Error(), "yolo update --from <checkout>") ||
		strings.Contains(err.Error(), ReleasesPage) || strings.Contains(err.Error(), "cannot tell") {
		t.Errorf("snapshot: want the two next steps, got %v", err)
	}
}

// `yolo update --from` deploys the named checkout as it stands, beside the running binary.
func TestPlanCheckout(t *testing.T) {
	var got []string
	for _, s := range PlanCheckout("/home/u/.local/bin/yolo", "/src/yolo-jail") {
		got = append(got, s.String())
	}
	if want := "GOBIN=/home/u/.local/bin just deploy   (in /src/yolo-jail)"; strings.Join(got, "\n") != want {
		t.Errorf("checkout plan:\n got %q\nwant %q", got, want)
	}
}

// A launcher deployed under another name redeploys under that name, not over the `yolo` beside it.
func TestPlanCheckoutKeepsTheLauncherName(t *testing.T) {
	var got []string
	for _, s := range PlanCheckout("/home/u/.local/bin/yolo-stable", "/src/yolo-jail") {
		got = append(got, s.String())
	}
	if want := "GOBIN=/home/u/.local/bin just deploy yolo-stable   (in /src/yolo-jail)"; strings.Join(got, "\n") != want {
		t.Errorf("checkout plan:\n got %q\nwant %q", got, want)
	}
}

func applyGit(dirty string) *fakeGit {
	return &fakeGit{answers: map[string]string{"status --porcelain": dirty}}
}

const (
	testExe      = "/home/u/.local/bin/yolo"
	testCheckout = "/src/yolo-jail"
)

func deploy(opts CheckoutOptions, g *fakeGit, r *recorder) error {
	return DeployCheckout(context.Background(), testExe, testCheckout, opts, g.run, r.run, io.Discard, io.Discard)
}

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

func TestDeployCheckoutRefusesADirtyCheckoutWithoutAutostash(t *testing.T) {
	r := &recorder{}
	err := deploy(CheckoutOptions{}, applyGit(" M internal/cli/cli.go"), r)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") || !strings.Contains(err.Error(), "--autostash") ||
		!strings.Contains(err.Error(), "just install") {
		t.Errorf("want an uncommitted-changes refusal naming --autostash and just install, got %v", err)
	}
	if len(r.ran) != 0 {
		t.Errorf("ran %v against a dirty checkout", r.ran)
	}
}

// Naming the checkout names what to deploy: no pull, no branch check, only `just deploy`.
func TestDeployCheckoutRunsOnlyTheDeployAndStopsAtAFailure(t *testing.T) {
	g, r := applyGit(""), &recorder{}
	if err := deploy(CheckoutOptions{}, g, r); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.ran, ",") != "just deploy" {
		t.Errorf("ran %v, want only the deploy", r.ran)
	}
	if strings.Join(g.calls, ",") != testCheckout+": status --porcelain" {
		t.Errorf("read the checkout with %v, want only its status", g.calls)
	}

	r = &recorder{fail: map[string]bool{"just deploy": true}}
	if err := deploy(CheckoutOptions{}, applyGit(""), r); err == nil {
		t.Error("a failing deploy must fail the update")
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

func TestDeployCheckoutAutostashDeploysACleanBuildAndRestoresTheEdits(t *testing.T) {
	stash := "git stash push --include-untracked -m " + AutostashMessage
	r := &recorder{}
	if err := deploy(CheckoutOptions{Autostash: true}, applyGit(" M x.go"), r); err != nil {
		t.Fatal(err)
	}
	want := stash + ",just deploy,git stash pop"
	if strings.Join(r.ran, ",") != want {
		t.Errorf("ran %v\nwant %s", r.ran, want)
	}

	// A clean checkout makes no stash, so there is nothing to pop.
	r = &recorder{}
	_ = deploy(CheckoutOptions{Autostash: true}, applyGit(""), r)
	if slices.Contains(r.ran, "git stash pop") {
		t.Errorf("popped a stash it never made: %v", r.ran)
	}

	// A failed deploy still gives the edits back.
	r = &recorder{fail: map[string]bool{"just deploy": true}}
	err := deploy(CheckoutOptions{Autostash: true}, applyGit(" M x.go"), r)
	if err == nil || r.ran[len(r.ran)-1] != "git stash pop" {
		t.Errorf("err=%v ran=%v, want the failure AND a final pop", err, r.ran)
	}

	// A pop that fails says where the edits are.
	r = &recorder{fail: map[string]bool{"git stash pop": true}}
	err = deploy(CheckoutOptions{Autostash: true}, applyGit(" M x.go"), r)
	if err == nil || !strings.Contains(err.Error(), "git stash list") {
		t.Errorf("want the stash named in the error, got %v", err)
	}
}

func TestApplyRunsTheChannelsPlan(t *testing.T) {
	r := &recorder{}
	if err := Apply(context.Background(), Channel{Kind: KindHomebrew}, r.run, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.ran, ",") != "brew upgrade yolo-jail" {
		t.Errorf("ran %v, want the brew upgrade", r.ran)
	}
	r = &recorder{}
	if err := Apply(context.Background(), Channel{Kind: KindSnapshot}, r.run, io.Discard, io.Discard); err == nil || len(r.ran) != 0 {
		t.Errorf("a snapshot install was applied: err=%v ran=%v", err, r.ran)
	}
}

func TestNotice(t *testing.T) {
	cases := []struct {
		st   State
		want string
	}{
		{State{Kind: KindHomebrew, Latest: "0.11.0", Current: "0.10.0"},
			"⬆ yolo-jail 0.11.0 is available (this is 0.10.0) — run `yolo update`"},
		{State{Kind: KindGoInstall, Latest: "0.11.0", Current: "0.10.0"},
			"⬆ yolo-jail 0.11.0 is available (this is 0.10.0) — this install cannot update itself; reinstall from Homebrew or a release archive: " + ReleasesPage},
		{State{Kind: KindArchive, Latest: "0.11.0", Current: "0.10.0"},
			"⬆ yolo-jail 0.11.0 is available (this is 0.10.0) — download it from " + ReleasesPage},
	}
	for _, c := range cases {
		if got := Notice(c.st); got != c.want {
			t.Errorf("Notice(%+v)\n got %q\nwant %q", c.st, got, c.want)
		}
	}
}
