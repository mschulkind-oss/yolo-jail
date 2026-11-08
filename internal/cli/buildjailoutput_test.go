package cli

// buildjailoutput_test.go pins the build logs' remaining gaps (buildjaillogs.go, treepool.go): a
// host build's own jail output is kept in a file and named in the host launch's log, a tree build's
// log holds no escape sequences, and the stream a build jail's launch writes to keeps redraws
// transient and log-only writes in the log.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A HOST BUILD'S JAIL OUTPUT IS KEPT: the fork build `yolo host -- tool` runs in-process writes its
// jail's streams to this process's raw stderr, which no launch log tees, so its output was in no
// file. It is now kept, ANSI-free, in the build's log as it streams, and the launch's own lines —
// teed into host-launch.log — say where.
func TestAHostForkBuildsJailOutputIsKept(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	build := fx.buildJail(t)
	var workspace string
	withFakeCaptureJail(t, func(o run.Options) int {
		workspace = o.Workspace
		if o.JailStdout == nil || o.JailStderr == nil {
			t.Error("the host build's jail streams are the process's own, which nothing keeps")
		} else {
			fmt.Fprintln(o.JailStdout, "\x1b[1mcompiling the patched tool\x1b[0m")
			fmt.Fprintln(o.JailStderr, "a warning from the build")
		}
		return build(o)
	})
	rc, _, out := fx.hostLaunch(t)
	if rc != 0 || workspace == "" {
		t.Fatalf("rc=%d, no build ran?\n%s", rc, out)
	}
	data, err := os.ReadFile(buildJailLogPath(workspace))
	if err != nil {
		t.Fatalf("the build's output was not kept: %v", err)
	}
	kept := string(data)
	for _, w := range []string{"=== build jail output of forkpack/tool at ", "compiling the patched tool\n",
		"a warning from the build\n"} {
		if !strings.Contains(kept, w) {
			t.Errorf("the kept output lacks %q:\n%s", w, kept)
		}
	}
	if strings.Contains(kept, "\x1b[") {
		t.Errorf("the kept output holds escape sequences:\n%q", kept)
	}
	pointer := "its build jail's output is in " + buildJailLogPath(workspace)
	if !strings.Contains(out, pointer) {
		t.Errorf("the launch does not say where the build's output is (%q):\n%s", pointer, out)
	}
	if hostLog, err := os.ReadFile(run.HostLaunchLogPath()); err != nil || !strings.Contains(string(hostLog), pointer) {
		t.Errorf("host-launch.log does not name the build's output (%v):\n%s", err, hostLog)
	}
}

// A TREE BUILD'S LOG IS ANSI-FREE: the lane's lines arrive rendered for a color terminal, and the
// copy tree-build-<key>.log keeps is stripped as a launch log's is.
func TestATreeBuildsLogHoldsNoEscapeSequences(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	// writeHostConfig names the agent pack, so it must exist for the selection to resolve.
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, treeHostOwn)
	var errw syncBuffer
	advanceHostTrees(&errw, true, "", nil)
	if len(fx.builds) != 1 || !strings.Contains(errw.String(), "\x1b[") {
		t.Fatalf("builds=%d; the lines were not colored, so the cell proves nothing:\n%q", len(fx.builds), errw.String())
	}
	var tree packload.Fork
	for _, f := range packload.PatchedTrees(selectConfiguredHostPacks().packs) {
		tree = f
	}
	data, err := os.ReadFile(treeBuildLog(tree))
	if err != nil || !strings.Contains(string(data), ": build started ") {
		t.Fatalf("no tree build log (%v):\n%s", err, data)
	}
	if strings.Contains(string(data), "\x1b[") {
		t.Errorf("the tree build log holds escape sequences:\n%q", data)
	}
}

// fakeLaunchStream is a launch stream as run's tee is: a terminal half that takes redraws, and a
// log half that takes log-only writes.
type fakeLaunchStream struct{ term, transient, log bytes.Buffer }

func (f *fakeLaunchStream) Write(p []byte) (int, error)          { return f.term.Write(p) }
func (f *fakeLaunchStream) WriteTransient(p []byte) (int, error) { return f.transient.Write(p) }
func (f *fakeLaunchStream) WriteLog(p []byte) (int, error)       { return f.log.Write(p) }

// THE REWRITER IS A LAUNCH STREAM AS THE ONE IT WRAPS IS: a redraw stays a redraw, and a write for
// the launch log alone still reaches the log, rewritten.
func TestTheBuildJailStreamKeepsRedrawsAndLogOnlyWrites(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "fork-x")
	inner := &fakeLaunchStream{}
	w := newBuildJailLogRewriter(inner, workspace)
	tw, ok := w.(interface{ WriteTransient([]byte) (int, error) })
	if !ok {
		t.Fatal("the rewriter hides the stream's redraws")
	}
	_, _ = tw.WriteTransient([]byte("\r frame"))
	if inner.transient.String() != "\r frame" || inner.term.Len() != 0 {
		t.Errorf("a redraw became a line: transient=%q term=%q", inner.transient.String(), inner.term.String())
	}
	old := paths.WorkspaceLogPath(workspace, run.HostPerfLogName)
	_, _ = run.LaunchLogOnly(w).Write([]byte("recorded in " + old + "\n"))
	if want := "recorded in " + buildJailLogPath(workspace) + "\n"; inner.log.String() != want {
		t.Errorf("the log-only write = %q, want %q", inner.log.String(), want)
	}
}
