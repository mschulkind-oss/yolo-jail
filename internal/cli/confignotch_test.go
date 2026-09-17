package cli

// confignotch_test.go pins WHICH TARGET the `yolo config` verbs compose at, now that they
// compose through render.Target rather than through a hand-copy of the boot render
// (docs/design/host-render-target.md §8 step 3).
//
// The notch is not a detail these commands may get approximately right. It decides two
// paths at once — which home "~" resolves against, and what "${workspace}" binds to — and
// getting either wrong is silent: a preview that bound the placeholder to the host
// checkout would print a plausible file full of keys the jail will never see, and one that
// bound it nowhere would print the literal. Both read as working output.
//
// TestConfigRenderExplain already covers the home half from the other side (it writes the
// host file under a scratch $HOME and expects `host` attribution), so what is left is the
// workspace half and the statement that the two travel together on one Target.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestConfigRenderPreviewsTheJailsWorkspaceNotTheHosts runs the command end to end on the
// shipped codex/config surface, whose managed layer is keyed on ${workspace}, and requires
// the preview to show the CONTAINER workspace.
//
// Through configRunW rather than through renderSurface, deliberately: a test that called
// the renderer directly would pass with the command wired to any target at all, which is
// the callee-pinned shape this repo has shipped five times. Point localTarget at a
// different notch and this fails; delete the substitution entirely and it fails with the
// literal placeholder in the output.
func TestConfigRenderPreviewsTheJailsWorkspaceNotTheHosts(t *testing.T) {
	_, repo := withHomeAndCwd(t)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"render", "codex/config"}, &out, &errw); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, containerWorkspace) {
		t.Fatalf("preview does not bind ${workspace} to the container workspace %q:\n%s",
			containerWorkspace, got)
	}
	if strings.Contains(got, agentcfg.WorkspacePlaceholder) {
		t.Fatalf("preview emitted the unsubstituted placeholder:\n%s", got)
	}
	// The host checkout is the wrong answer and the easiest wrong answer to reach — it is
	// what the process is actually sitting in, so a target built from the cwd would look
	// right in a manual check and be wrong in every jail.
	if strings.Contains(got, repo) {
		t.Fatalf("preview bound ${workspace} to the host cwd %q, not the jail's:\n%s", repo, got)
	}
}

// TestLocalTargetResolvesTheProcessHome states the other half of localTarget explicitly,
// because the two halves answer to different things and a reader who has just read the
// workspace test would reasonably expect this one to be the host checkout too.
//
// "~" is the PROCESS home: in-jail the jail's, host-side the invoking human's. That is
// what makes the host-side writers among these verbs dangerous enough to need
// refuseHostSideWrite, so pinning it here is also pinning the premise that refusal rests
// on.
func TestLocalTargetResolvesTheProcessHome(t *testing.T) {
	home, _ := withHomeAndCwd(t)
	if got, want := localTarget().ExpandHome("~/.codex/config.toml"),
		paths.Home()+"/.codex/config.toml"; got != want {
		t.Fatalf("localTarget().ExpandHome = %q, want %q", got, want)
	}
	if !strings.HasPrefix(localTarget().ExpandHome("~/x"), home) {
		t.Fatalf("localTarget() did not follow $HOME to %q: %q", home, localTarget().ExpandHome("~/x"))
	}
}

// TestExpandHomeIsTheTargetsResolver is the collapse's CLI-side assertion: the package's
// own expandHome is not a second implementation of "~", it is the Target's. It was one —
// a filepath.Join against paths.Home() beside the one in internal/entrypoint that read an
// Env — and the two agreeing was maintenance rather than construction.
func TestExpandHomeIsTheTargetsResolver(t *testing.T) {
	withHomeAndCwd(t)
	for _, p := range []string{"~", "~/.claude/settings.json", "/etc/absolute", "relative/path"} {
		if got, want := expandHome(p), localTarget().ExpandHome(p); got != want {
			t.Errorf("expandHome(%q) = %q, want the Target's %q", p, got, want)
		}
	}
}
