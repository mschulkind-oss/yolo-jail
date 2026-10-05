package cli

// packlintseries_test.go pins `yolo pack lint` reading a pack's PATCH SERIES
// (packlintseries.go; docs/design/patched-forks.md PF-D60, PF-D61; patched-extensions.md PPX-D34):
// a series a launch cannot read fails lint with the read's own remedy, for a patched fork and a
// patched extension alike, read in the pack's own directory as every reader of a series reads it;
// `--online` checks each upstream in a scratch mirror it deletes; and a list that would load a
// patched extension twice is warned about.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const lintBase = "0123456789abcdef0123456789abcdef01234567"

// lintMember is a mail-format series member with a diff, carrying a base-commit line when base is
// set.
func lintMember(base string) string {
	m := "From " + lintBase + " Mon Sep 17 00:00:00 2001\nFrom: t <t@e>\nSubject: [PATCH] x\n\n---\n" +
		" f.txt | 2 +-\n\ndiff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-a\n+b\n"
	if base != "" {
		m += "\nbase-commit: " + base + "\n"
	}
	return m + "-- \n2.40.0\n"
}

// lintSeriesPack writes a pack named name declaring one patched fork (extension false) or one
// patched extension, whose series directory is "patches", populated by fill.
func lintSeriesPack(t *testing.T, name string, extension bool, fill func(dir string)) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if fill != nil {
		fill(filepath.Join(dir, "patches"))
	}
	contribution := `{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",` +
		`"source":"git+https://example.invalid/up/tool?ref=main","patches":"patches",` +
		`"build":"sh build.sh","produces":[".local/bin/tool"]}`
	if extension {
		contribution = `{"kind":"files","into":".pi/agent/yolo-patched/pi-foo",` +
			`"source":"git+https://example.invalid/up/pi-foo?ref=main","patches":"patches"},` +
			`{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["~/.pi/agent/yolo-patched/pi-foo"]}`
	}
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"`+name+`","contributes":[`+contribution+`]}`)
	return dir
}

func lintArgs(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := packMain(append([]string{"lint"}, args...), &out, &errw, false)
	return rc, out.String() + errw.String()
}

// EVERY SERIES A LAUNCH CANNOT READ FAILS LINT, naming the remedy the read names (request 3): a
// series exported without --base, a plain diff named .patch, an empty folder and a missing one.
func TestPackLintFailsASeriesALaunchCannotRead(t *testing.T) {
	cases := []struct {
		name string
		fill func(dir string)
		want []string
	}{
		{"exported without --base", func(dir string) {
			writeFile(t, filepath.Join(dir, "0001-x.patch"), lintMember(""))
		}, []string{"names no base commit", "re-export the series with `git format-patch --base="}},
		{"a plain diff named .patch", func(dir string) {
			writeFile(t, filepath.Join(dir, "0001-x.patch"), "--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-a\n+b\n")
		}, []string{"is not a mail-format patch", "export the commit with `git format-patch`"}},
		{"an empty folder", func(dir string) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}, []string{"holds no .patch file", "git format-patch --base="}},
		{"a missing folder", nil, []string{"the directory does not exist", `correct the fork's "patches"`}},
	}
	for _, c := range cases {
		for _, ext := range []bool{false, true} {
			label := "fork forkpack/tool"
			if ext {
				label = "extension forkpack/pi-foo"
			}
			rc, out := lintArgs(t, lintSeriesPack(t, "forkpack", ext, c.fill))
			if rc == 0 {
				t.Errorf("%s (%s): lint passed\n%s", c.name, label, out)
			}
			for _, w := range append([]string{"✗ " + label + ": patch series patches"}, c.want...) {
				if !strings.Contains(out, w) {
					t.Errorf("%s (%s): lint lacks %q:\n%s", c.name, label, w, out)
				}
			}
		}
	}
}

// A `patches` VALUE THE MANIFEST REFUSES is said once, by the manifest's problem, not again by the
// read.
func TestPackLintSaysABadPatchesValueOnce(t *testing.T) {
	dir := lintSeriesPack(t, "forkpack", false, nil)
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"forkpack","contributes":[{"kind":"program","bin":"tool",`+
		`"via":"source","fork_of":"basepack","source":"git+https://example.invalid/up/tool?ref=main",`+
		`"patches":"../outside","build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
	rc, out := lintArgs(t, dir)
	if rc == 0 || strings.Count(out, "../outside") != 1 {
		t.Errorf("lint of patches \"../outside\": rc %d, want one line naming it\n%s", rc, out)
	}
}

// A READABLE SERIES PASSES, and the footprint counts it.
func TestPackLintPassesAReadableSeries(t *testing.T) {
	dir := lintSeriesPack(t, "forkpack", false, func(dir string) {
		writeFile(t, filepath.Join(dir, "0001-x.patch"), lintMember(lintBase))
	})
	rc, out := lintArgs(t, dir)
	if rc != 0 || !strings.Contains(out, "1 patch in patches (series") {
		t.Errorf("lint of a readable series: rc %d\n%s", rc, out)
	}
	if strings.Contains(out, "online") {
		t.Errorf("lint without --online says something about the upstream:\n%s", out)
	}
}

// THE SERIES IS READ IN THE PACK ITSELF, as a launch reads it, never in lint's staged copy, which
// resolves an in-pack link into a plain file: a member that is a link fails lint as it fails a
// launch.
func TestPackLintReadsTheSeriesInThePackItself(t *testing.T) {
	dir := lintSeriesPack(t, "forkpack", false, func(dir string) {
		writeFile(t, filepath.Join(filepath.Dir(dir), "kept", "0001-x.patch"), lintMember(lintBase))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "kept", "0001-x.patch"), filepath.Join(dir, "0001-x.patch")); err != nil {
			t.Fatal(err)
		}
	})
	rc, out := lintArgs(t, dir)
	if rc == 0 || !strings.Contains(out, "patches/0001-x.patch: is a symbolic link") {
		t.Errorf("lint of a linked member: rc %d, want the read's refusal\n%s", rc, out)
	}
}

// AN UNKNOWN FLAG is refused, naming the one lint takes, rather than read as the pack's directory.
func TestPackLintRefusesAnUnknownFlag(t *testing.T) {
	rc, out := lintArgs(t, "--onlin", t.TempDir())
	if rc != 2 || !strings.Contains(out, "--onlin") || !strings.Contains(out, "--online") {
		t.Errorf("lint --onlin: rc %d\n%s", rc, out)
	}
}

// lintOnlineHome is newPatchedFixture with a private TMPDIR, so a test can see what lint leaves.
func lintOnlineHome(t *testing.T) (*patchedFixture, string) {
	t.Helper()
	f := newPatchedFixture(t, "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return f, tmp
}

// mustBeEmpty fails when dir holds anything: lint deletes its scratch mirror and its staged copy.
func mustBeEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("lint left %s behind in %s", e.Name(), dir)
	}
}

// --ONLINE CHECKS THE UPSTREAM, in a scratch mirror it deletes, writing nothing into the pack store:
// the ref, what the follow rule finds, and the series replayed at its base.
func TestPackLintOnlineChecksTheUpstreamInAScratchMirror(t *testing.T) {
	f, tmp := lintOnlineHome(t)
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	rc, out := lintArgs(t, "--online", f.forkDir)
	if rc != 0 {
		t.Fatalf("lint --online rc %d\n%s", rc, out)
	}
	for _, w := range []string{
		"online: fork forkpack/tool: ?ref=main is a branch, and `follow: \"release\"` finds v1.1.0 (" + shortSHA(v11) + ")",
		"online: fork forkpack/tool: the series (2 patches, series ",
		"replays at its base " + shortSHA(f.base),
	} {
		if !strings.Contains(out, w) {
			t.Errorf("lint --online lacks %q:\n%s", w, out)
		}
	}
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "mirrors")); err == nil {
		t.Errorf("lint --online fetched into the pack store %s", paths.PacksDir())
	}
	mustBeEmpty(t, tmp)
	// The flag may follow the directory.
	if rc, out := lintArgs(t, f.forkDir, "--online"); rc != 0 || !strings.Contains(out, "online: fork forkpack/tool") {
		t.Errorf("lint <dir> --online: rc %d\n%s", rc, out)
	}
}

// --ONLINE FAILS A REF THE UPSTREAM DOES NOT HAVE, naming it.
func TestPackLintOnlineFailsAMissingRef(t *testing.T) {
	f, tmp := lintOnlineHome(t)
	f.writeManifest(t, "v9.9.9", "")
	rc, out := lintArgs(t, "--online", f.forkDir)
	if rc == 0 || !strings.Contains(out, "✗ online: fork forkpack/tool: ?ref=v9.9.9 names no branch, tag or commit") {
		t.Errorf("lint --online of ?ref=v9.9.9: rc %d\n%s", rc, out)
	}
	mustBeEmpty(t, tmp)
}

// --ONLINE REPORTS A TAGLESS UPSTREAM under the default release rule, naming `follow: "head"`.
// Whether that fails lint is the check's verdict (onlineVerdict): this build's check records it as a
// problem, so lint fails.
func TestPackLintOnlineReportsATaglessUpstream(t *testing.T) {
	f, _ := lintOnlineHome(t)
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	f.commitMsg(t, "untagged", "", map[int]string{14: "fourteen"})
	_, out := lintArgs(t, "--online", f.forkDir)
	if !strings.Contains(out, "online: fork forkpack/tool: ?ref=main of ") ||
		!strings.Contains(out, "carries no version tag") || !strings.Contains(out, "`follow: \"head\"`") {
		t.Errorf("lint --online of a tagless upstream does not name it:\n%s", out)
	}
}

// --ONLINE FAILS A SERIES THAT DOES NOT APPLY AT ITS OWN BASE: the replay's base error, naming the
// re-export.
func TestPackLintOnlineFailsASeriesThatDoesNotApplyAtItsBase(t *testing.T) {
	f, _ := lintOnlineHome(t)
	moved := f.commit(t, "v1.1.0", map[int]string{10: "TEN"})
	dir := filepath.Join(f.forkDir, "patches")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, strings.ReplaceAll(string(data), f.base, moved))
	}
	rc, out := lintArgs(t, "--online", f.forkDir)
	if rc == 0 || !strings.Contains(out, "✗ online: fork forkpack/tool: 0001-") ||
		!strings.Contains(out, "does not apply at the series' base "+shortSHA(moved)) {
		t.Errorf("lint --online of a series off its base: rc %d\n%s", rc, out)
	}
}

// AN UPSTREAM --ONLINE CANNOT FETCH fails, naming lint as the act to run again; without --online the
// same pack lints clean, touching no network.
func TestPackLintOnlineFailsAnUpstreamItCannotFetch(t *testing.T) {
	f, tmp := lintOnlineHome(t)
	if err := os.RemoveAll(f.repo); err != nil {
		t.Fatal(err)
	}
	rc, out := lintArgs(t, "--online", f.forkDir)
	if rc == 0 || !strings.Contains(out, "✗ online: fork forkpack/tool: could not fetch git+file://") ||
		!strings.Contains(out, "run `yolo pack lint --online` again") {
		t.Errorf("lint --online of a gone upstream: rc %d\n%s", rc, out)
	}
	mustBeEmpty(t, tmp)
	if rc, out := lintArgs(t, f.forkDir); rc != 0 || strings.Contains(out, "online") {
		t.Errorf("lint without --online reached the upstream: rc %d\n%s", rc, out)
	}
}

// A PATCHED EXTENSION is checked online as a patched fork is, by its key.
func TestPackLintOnlineChecksAPatchedExtension(t *testing.T) {
	f, _ := lintOnlineHome(t)
	ext := filepath.Join(t.TempDir(), "matt")
	if err := os.CopyFS(filepath.Join(ext, "patches"), os.DirFS(filepath.Join(f.forkDir, "patches"))); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ext, "pack.json"), `{"name":"matt","contributes":[`+
		`{"kind":"files","into":".pi/agent/yolo-patched/pi-tool","source":"git+file://`+f.repo+`?ref=main","patches":"patches"},`+
		`{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["~/.pi/agent/yolo-patched/pi-tool"]}]}`)
	rc, out := lintArgs(t, "--online", ext)
	if rc != 0 || !strings.Contains(out, "online: extension matt/pi-tool: the series (2 patches") {
		t.Errorf("lint --online of a patched extension: rc %d\n%s", rc, out)
	}
}

// A GIT TOO OLD FOR THE REPLAY names updating git and lint as the next steps (PF-D58's rule).
func TestPackLintOnlineUnderAnOldGitNamesUpdatingGit(t *testing.T) {
	f, _ := lintOnlineHome(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git"), "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"git version 2.39.5\"; exit 0; fi\n"+
		"exec "+shquote.Quote(realGit)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := lintOnlineStore
	lintOnlineStore = func(dir string) *packsrc.Store { s := prev(dir); s.Git = filepath.Join(bin, "git"); return s }
	t.Cleanup(func() { lintOnlineStore = prev })
	rc, out := lintArgs(t, "--online", f.forkDir)
	if rc == 0 || !strings.Contains(out, "and this machine's git is 2.39.5 — update git, then run `yolo pack lint --online` again") {
		t.Errorf("lint --online under git 2.39.5: rc %d\n%s", rc, out)
	}
}

// A CTRL-C ends the online checks, says so, and still deletes the scratch mirror.
func TestPackLintOnlineInterruptedDeletesItsScratchMirror(t *testing.T) {
	f, tmp := lintOnlineHome(t)
	prev := lintOnlineInterrupt
	lintOnlineInterrupt = func() (context.Context, func()) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	t.Cleanup(func() { lintOnlineInterrupt = prev })
	rc, out := lintArgs(t, "--online", f.forkDir)
	if rc == 0 || !strings.Contains(out, "✗ online: interrupted") {
		t.Errorf("an interrupted lint --online: rc %d\n%s", rc, out)
	}
	mustBeEmpty(t, tmp)
}

// THE ONLINE CHECKS' GIT RUNS UNDER THE INTERRUPT, so a Ctrl-C ends a fetch in flight rather than
// waiting it out: the scratch store carries the interrupt's context.
func TestPackLintOnlineRunsItsGitUnderTheInterrupt(t *testing.T) {
	f, _ := lintOnlineHome(t)
	type key struct{}
	prevInt, prevStore := lintOnlineInterrupt, lintOnlineStore
	lintOnlineInterrupt = func() (context.Context, func()) {
		return context.WithValue(context.Background(), key{}, "the interrupt"), func() {}
	}
	var handed *packsrc.Store
	lintOnlineStore = func(dir string) *packsrc.Store { handed = prevStore(dir); return handed }
	t.Cleanup(func() { lintOnlineInterrupt, lintOnlineStore = prevInt, prevStore })
	if rc, out := lintArgs(t, "--online", f.forkDir); rc != 0 {
		t.Fatalf("lint --online rc %d\n%s", rc, out)
	}
	if handed == nil || handed.Ctx == nil || handed.Ctx.Value(key{}) != "the interrupt" {
		t.Errorf("the scratch store's git does not run under the interrupt's context: %+v", handed)
	}
}

// THE VERDICT FOLLOWS THE CHECK: a tagless branch the check records as a problem fails once, with
// the check's words; one it builds at the base is a warning naming `follow: "head"`.
func TestTheOnlineVerdictOnATaglessBranchFollowsTheCheck(t *testing.T) {
	f, series := onlineVerdictFixture()
	asProblem := packsrc.SeriesProbe{Tagless: true, Replayed: true,
		Found: packsrc.CheckFound{RefKind: "branch", Problem: "?ref=main of x carries no version tag"}}
	problems, warnings, _ := onlineVerdict(f, series, asProblem)
	if len(problems) != 1 || !strings.Contains(problems[0], "carries no version tag") || len(warnings) != 0 {
		t.Errorf("a tagless problem: problems %q, warnings %q, want the check's problem alone", problems, warnings)
	}
	atBase := packsrc.SeriesProbe{Tagless: true, Replayed: true,
		Found: packsrc.CheckFound{RefKind: "branch", BaseOnBranch: true}}
	problems, warnings, _ = onlineVerdict(f, series, atBase)
	if len(problems) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "carries no version tag") ||
		!strings.Contains(warnings[0], "`follow: \"head\"`") {
		t.Errorf("a tagless branch built at its base: problems %q, warnings %q, want one warning naming follow head",
			problems, warnings)
	}
}

// A BRANCH THAT DOES NOT CONTAIN THE BASE, with nothing on the list, is a failure, as `yolo pack
// update` says; versions that all predate a base the branch holds build the base, a note.
func TestTheOnlineVerdictOnAnEmptyList(t *testing.T) {
	f, series := onlineVerdictFixture()
	problems, _, _ := onlineVerdict(f, series, packsrc.SeriesProbe{Replayed: true,
		Found: packsrc.CheckFound{RefKind: "branch"}})
	if len(problems) != 1 || !strings.Contains(problems[0], "does not contain the series' base") {
		t.Errorf("a branch without the base: problems %q", problems)
	}
	problems, warnings, notes := onlineVerdict(f, series, packsrc.SeriesProbe{Replayed: true,
		Found: packsrc.CheckFound{RefKind: "branch", BaseOnBranch: true}})
	if len(problems) != 0 || len(warnings) != 0 || !strings.Contains(strings.Join(notes, "\n"), "builds the series' base") {
		t.Errorf("versions before the base: problems %q, warnings %q, notes %q", problems, warnings, notes)
	}
}

// onlineVerdictFixture is a patched fork and its series, for onlineVerdict's cases.
func onlineVerdictFixture() (packload.Fork, *packsrc.Series) {
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Patches: "patches",
		Source: "git+https://example.invalid/up/tool?ref=main"}
	return f, &packsrc.Series{Dir: "patches", Base: lintBase, Digest: strings.Repeat("d", 64),
		Members: []packsrc.SeriesMember{{Name: "0001-x.patch"}}}
}

// A PACK THAT WOULD LOAD AN EXTENSION TWICE is warned about at lint, which still passes (request 3).
func TestPackLintWarnsOfAnExtensionLoadedTwice(t *testing.T) {
	dir := lintSeriesPack(t, "matt", true, func(dir string) {
		writeFile(t, filepath.Join(dir, "0001-x.patch"), lintMember(lintBase))
	})
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"matt","contributes":[`+
		`{"kind":"files","into":".pi/agent/yolo-patched/pi-foo","source":"git+https://example.invalid/x/pi-foo?ref=main","patches":"patches"},`+
		`{"kind":"config-list","surface":"pi/settings","path":"/packages",`+
		`"add":["git:github.com/x/pi-foo","~/.pi/agent/yolo-patched/pi-foo"]}]}`)
	rc, out := lintArgs(t, dir)
	if rc != 0 || !strings.Contains(out, `"git:github.com/x/pi-foo", the same package by its name, so the agent loads it twice`) {
		t.Errorf("lint of a list loading pi-foo twice: rc %d\n%s", rc, out)
	}
}
