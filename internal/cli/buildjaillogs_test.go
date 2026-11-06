package cli

// buildjaillogs_test.go pins that a capture or build jail's own diagnostics outlive the scratch
// workspace they were written in (buildjaillogs.go; docs/reference/perf-logging.md, "Where a build
// jail's logs go"): every cleanup path keeps them in the host log directory first, the launch's
// "timings recorded in" line names where they are kept, the kept files are bounded, and a link the
// jail left is never followed out of its workspace.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// buildJailDiagnostics are the files a jail's launch leaves in its workspace, by path under it, and
// the body the fake launch writes to each.
var buildJailDiagnostics = map[string]string{
	".yolo/host-perf.log":                         "=== YOLO Host Perf the build's spans\n",
	".yolo/boot.log":                              "entrypoint: the build's boot\n",
	".yolo/launch.log":                            "the build's launch stream\n",
	".yolo/setups/podman-self/home/yolo-perf.log": "the build's jail half\n",
}

// THE JAIL HALF IS READ FROM THE HOME THE BUILD JAIL RAN WITH (named-setups.md §9): every built-in
// setup's home and the legacy one before the jail has made any, and a user-defined setup's home
// once the workspace holds it. Red if buildJailPerfLogSources stops naming a setup's home.
func TestTheBuildJailsPerfLogIsReadFromEveryHome(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(paths.SetupHomeState(ws, "lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	var rels []string
	seen := map[string]bool{}
	for _, src := range buildJailPerfLogSources(ws) {
		rel := filepath.ToSlash(src.rel)
		if seen[rel] {
			t.Errorf("the home of %s is read twice", rel)
		}
		seen[rel] = true
		rels = append(rels, rel)
	}
	for _, want := range []string{".yolo/home/yolo-perf.log", ".yolo/setups/podman-self/home/yolo-perf.log",
		".yolo/setups/apple-container-self/home/yolo-perf.log", ".yolo/setups/lab/home/yolo-perf.log"} {
		if !seen[want] {
			t.Errorf("the jail half is not read from %s: %v", want, rels)
		}
	}
}

// leaveBuildJailDiagnostics is a fake launch's part: it writes buildJailDiagnostics into o's
// workspace and says, on the launch's stderr, the line a launch that recorded timings ends with.
func leaveBuildJailDiagnostics(t *testing.T, o run.Options) {
	t.Helper()
	for rel, body := range buildJailDiagnostics {
		writeFile(t, filepath.Join(o.Workspace, filepath.FromSlash(rel)), body)
	}
	fmt.Fprintf(o.Stderr, "yolo: timings recorded in %s (--timing prints them)\n",
		filepath.Join(paths.WorkspaceStateDir(o.Workspace), run.HostPerfLogName))
}

// assertDiagnosticsKept fails unless workspace is gone and its build-jail log holds every body,
// each under the header naming its file.
func assertDiagnosticsKept(t *testing.T, workspace string) string {
	t.Helper()
	if _, err := os.Lstat(workspace); err == nil {
		t.Errorf("the workspace %s outlived its cleanup", workspace)
	}
	data, err := os.ReadFile(buildJailLogPath(workspace))
	if err != nil {
		t.Fatalf("the jail's diagnostics were not kept: %v", err)
	}
	kept := string(data)
	for _, body := range buildJailDiagnostics {
		if !strings.Contains(kept, body) {
			t.Errorf("the kept log lacks %q:\n%s", body, kept)
		}
	}
	for _, label := range []string{"=== host-perf.log of ", "=== boot.log of ", "=== launch.log of ",
		"=== yolo-perf.log (the jail half) of "} {
		if !strings.Contains(kept, label+workspace) {
			t.Errorf("the kept log lacks the header %q:\n%s", label+workspace, kept)
		}
	}
	return kept
}

// A PATCHED EXTENSION'S BUILD JAIL KEEPS ITS DIAGNOSTICS through the fork build's cleanup
// (cleanupForkBuildWorkspace), and its launch's last line names the file they are kept in rather
// than the workspace that is deleted.
func TestABuildJailsDiagnosticsOutliveItsWorkspace(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	// writeHostConfig names the agent pack, so it must exist for the selection to resolve.
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, treeHostOwn)
	build := fx.buildJail(t)
	withFakeCaptureJail(t, func(o run.Options) int {
		leaveBuildJailDiagnostics(t, o)
		return build(o)
	})
	var errw syncBuffer
	advanceHostTrees(&errw, false, "", nil)
	if len(fx.builds) != 1 || len(fx.seen) != 1 {
		t.Fatalf("the host advance built %d trees:\n%s", len(fx.builds), errw.String())
	}
	workspace := fx.seen[0].Workspace
	if !strings.HasPrefix(filepath.Base(workspace), "fork-") {
		t.Fatalf("the build ran in %s, not a build's staging workspace", workspace)
	}
	assertDiagnosticsKept(t, workspace)
	out := errw.String()
	if want := "yolo: timings recorded in " + buildJailLogPath(workspace); !strings.Contains(out, want) {
		t.Errorf("the build's line does not name the kept file (%q):\n%s", want, out)
	}
	if strings.Contains(out, filepath.Join(paths.WorkspaceStateDir(workspace), run.HostPerfLogName)) {
		t.Errorf("the build's line names the deleted workspace's file:\n%s", out)
	}
}

// AN INSTALLER'S CAPTURE KEEPS ITS JAIL'S DIAGNOSTICS through its deferred cleanup, and what an
// earlier capture's workspace left — one Stage clears before this capture starts — is kept too.
func TestACapturesDiagnosticsOutliveItsWorkspace(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	leftover := store.StagingDir("probetool")
	writeFile(t, filepath.Join(leftover, ".yolo", "boot.log"), "an earlier capture's boot\n")
	var seen run.Options
	fake := fakeCaptureJail(t, &seen, []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 64},
	})
	withFakeCaptureJail(t, func(o run.Options) int {
		leaveBuildJailDiagnostics(t, o)
		return fake(o)
	})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc = %d\n%s\n%s", rc, out.String(), errw.String())
	}
	kept := assertDiagnosticsKept(t, seen.Workspace)
	if !strings.Contains(kept, "an earlier capture's boot\n") {
		t.Errorf("the leftover workspace's boot log was not kept before Stage cleared it:\n%s", kept)
	}
	if want := "yolo: timings recorded in " + buildJailLogPath(seen.Workspace); !strings.Contains(errw.String(), want) {
		t.Errorf("the capture's line does not name the kept file (%q):\n%s", want, errw.String())
	}
}

// THE KEPT LOGS ARE BOUNDED, by the host log directory's two rules: a file past
// treeBuildLogRotateAt moves to .1 before the next append, and only the newest buildJailLogsKept
// files survive, each with its .1.
func TestKeptBuildJailLogsAreBounded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	logs := filepath.Join(paths.GlobalStorage(), "logs")
	old := time.Now().Add(-time.Hour)
	for i := range buildJailLogsKept {
		p := filepath.Join(logs, fmt.Sprintf("%sfork-%02d.log", buildJailLogPrefix, i))
		writeFile(t, p, "old\n")
		if err := os.Chtimes(p, old.Add(time.Duration(i)*time.Second), old.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(logs, buildJailLogPrefix+"fork-00.log.1"), "older\n")
	workspace := filepath.Join(t.TempDir(), "fork-big")
	writeFile(t, buildJailLogPath(workspace), strings.Repeat("x", treeBuildLogRotateAt+1))
	writeFile(t, filepath.Join(workspace, ".yolo", "boot.log"), "the newest build's boot\n")

	preserveBuildJailLogs(workspace)

	for _, gone := range []string{"fork-00.log", "fork-00.log.1"} {
		if _, err := os.Lstat(filepath.Join(logs, buildJailLogPrefix+gone)); err == nil {
			t.Errorf("%s survived past the newest %d", gone, buildJailLogsKept)
		}
	}
	if _, err := os.Lstat(filepath.Join(logs, buildJailLogPrefix+"fork-01.log")); err != nil {
		t.Errorf("a log among the newest %d was removed: %v", buildJailLogsKept, err)
	}
	data, err := os.ReadFile(buildJailLogPath(workspace))
	if err != nil || strings.Contains(string(data), "xxx") || !strings.Contains(string(data), "the newest build's boot") {
		t.Errorf("the oversized log was not rotated before the append (%v):\n%.200s", err, data)
	}
	if fi, err := os.Stat(buildJailLogPath(workspace) + ".1"); err != nil || fi.Size() != treeBuildLogRotateAt+1 {
		t.Errorf("the oversized log was not moved to .1: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".yolo", "boot.log")); err == nil {
		t.Error("a kept source was left in the workspace, so a second cleanup would keep it twice")
	}
}

// A LINK THE JAIL LEFT IS NEVER FOLLOWED OUT OF ITS WORKSPACE: the workspace is the jail's to
// write, and the log directory a jail may be shown must not receive a host file by way of it.
func TestKeepingABuildJailsLogsNeverFollowsALinkOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	secret := filepath.Join(t.TempDir(), "secret")
	writeFile(t, secret, "a host secret\n")
	workspace := filepath.Join(t.TempDir(), "fork-link")
	if err := os.MkdirAll(filepath.Join(workspace, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(workspace, ".yolo", "boot.log")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workspace, ".yolo", "launch.log"), "the launch\n")
	preserveBuildJailLogs(workspace)
	data, err := os.ReadFile(buildJailLogPath(workspace))
	if err != nil || !strings.Contains(string(data), "the launch") {
		t.Fatalf("the regular file was not kept (%v):\n%s", err, data)
	}
	if strings.Contains(string(data), "a host secret") {
		t.Errorf("a link out of the workspace was followed:\n%s", data)
	}
}

// THE REWRITE TOUCHES ONLY THE PATHS IT KEEPS: the launch's other lines pass through as written.
func TestTheBuildJailStreamRewritesOnlyTheKeptPaths(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "fork-x")
	var buf bytes.Buffer
	w := newBuildJailLogRewriter(&buf, workspace)
	line := "keeper: started " + workspace + "/src\n"
	if n, err := io.WriteString(w, line); err != nil || n != len(line) || buf.String() != line {
		t.Errorf("an unrelated line changed: n=%d err=%v %q", n, err, buf.String())
	}
	if newBuildJailLogRewriter(nil, workspace) != nil {
		t.Error("a nil stream became a non-nil one")
	}
}
