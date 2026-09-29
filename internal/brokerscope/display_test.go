package brokerscope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// What the reader returns for display carries no terminal sequence. A worktree pointer can
// name a common directory the agent created under any name, and host yolo prints that path
// in the scope prompt, the no-terminal refusal and the launch's warning line.
func TestReadRemotesReturnsNothingATerminalActsOn(t *testing.T) {
	// A worktree pointer, inside the workspace, into a common dir whose name clears a line,
	// moves the cursor up, retitles the window and hides what follows. The pointer passes the
	// back-pointer and commondir checks.
	ws := resolvedTempDir(t)
	evil := "x\x1b[2K\x1b[1A\x1b]0;owned\a\x1b[8m"
	g := filepath.Join(ws, evil, "worktrees", "w")
	writeFile(t, filepath.Join(g, "gitdir"), "../../../.git\n")
	writeFile(t, filepath.Join(g, "commondir"), "../..\n")
	writeFile(t, filepath.Join(ws, evil, "config"),
		"[remote \"origin\"]\n\turl = git@github.com:victim/secret.git\n")
	writeFile(t, filepath.Join(ws, ".git"), "gitdir: "+evil+"/worktrees/w\n")
	r := ReadRemotes(ws, "github.com")
	if len(r.Remotes) != 0 || !strings.Contains(r.Problem, "control character") {
		t.Fatalf("a common dir named with control characters was followed: %+v", r)
	}
	if termsafe.HasUnsafe(r.Problem) || termsafe.HasUnsafe(r.GitConfig) {
		t.Fatalf("the read carries a control character: %q / %q", r.GitConfig, r.Problem)
	}

	// And any path a problem names is escaped, not only the ones the pointer check sees.
	odd := filepath.Join(resolvedTempDir(t), "a\x1b]52;c;cGF3bmVk\ab")
	if err := os.MkdirAll(filepath.Join(odd, ".git", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	r = ReadRemotes(odd, "github.com")
	if r.Problem == "" || termsafe.HasUnsafe(r.Problem) || termsafe.HasUnsafe(r.GitConfig) {
		t.Fatalf("read %q / %q", r.GitConfig, r.Problem)
	}
	if !strings.Contains(r.Problem, `a\x1b]52;c;cGF3bmVk\ab`) {
		t.Fatalf("the problem does not show the escaped path: %q", r.Problem)
	}
}
