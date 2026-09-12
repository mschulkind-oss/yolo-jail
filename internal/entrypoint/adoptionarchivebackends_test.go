package entrypoint

// adoptionarchivebackends_test.go measures the two things OQ-CO7's adoption archive
// (docs/design/config-ownership-and-promotion.md §6.3.3) claims about the JAIL notch that
// adoptionarchive_test.go's fixtures cannot state, because both are properties of the boot the
// container path and the macos-user path do NOT share:
//
//  1. THE ARCHIVE EXISTS ON macos-user TOO, and lands in the workspace. The two backends reach
//     per-workspace state by different primitives — the container binds a directory into the
//     home, macos-user has one account home for every workspace and symlinks out of it — so
//     "the jail's archive goes to <workspace>/.yolo/" is one sentence with two mechanisms
//     behind it. A net that exists on one backend and silently not the other is worse than one
//     that says it is unsupported, and nothing else in the suite runs the native bootstrap.
//
//  2. A FAILED ARCHIVE STOPS THE BOOT. At the host notch a refusal is a per-surface result and
//     the apply carries on; at the jail it is A12-fatal, because the alternative — adopting
//     with no copy — takes the one-way door without the thing that makes it survivable, and a
//     boot has no TTY to ask on. The dispositions differ, so pinning the host's says nothing
//     about the jail's: `configurePackSurface`'s rmw arm already downgrades exactly this error
//     type to a warning, and the stateful arm is one `asRMWRefusal` check away from doing the
//     same with the whole suite green.
//
// Both drive a REAL boot entry — RunDarwinBootstrap and ConfigurePackSurfaces — never the
// writer. Fixtures are local to this file rather than shared with adoptionarchive_test.go: the
// two tests here are about the boot ENVIRONMENT, so each states its own.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// backendArchiveSeed is a hand-written agent file in a shape yolo re-emits differently
// (compact, key order not yolo's), so an archive holding it is a statement about the
// PRE-RENDER bytes rather than about a file the render happened to leave alone.
const backendArchiveSeed = `{"apiKeyHelper":"/usr/local/bin/acme-key.sh","permissions":{"ask":["Bash(rm:*)"]}}` + "\n"

// backendArchivePack is a one-surface pack declaring no mode — i.e. `stateful`, the mechanism
// whose first render ADOPTS. Returned as a loaded pack for the jail entry, and staged on disk
// for the darwin entry, which loads its packs itself out of $YOLO_PACK_ROOT.
func backendArchivePack(t *testing.T) *packload.Pack {
	t.Helper()
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: backendArchiveSurfaces(t)}},
	}}
}

// backendArchiveSurfaces is the surface list both entries render: ~/.acme/settings.json, an
// object surface with one default and one managed key.
func backendArchiveSurfaces(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":     "~/.acme/settings.json",
		"defaults": map[string]any{"theme": "system"},
		"managed":  map[string]any{"permissions": map[string]any{"defaultMode": "default"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// stageBackendArchivePack writes that pack into a fresh $YOLO_PACK_ROOT tree and returns the
// root. The darwin bootstrap reads its packs off that variable exactly as the container
// entrypoint reads them off its /ctx/packs mount, so staging is how this test hands the native
// path a pack at all.
func stageBackendArchivePack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"name": "acme",
		"contributes": []any{map[string]any{
			"kind": "config", "config": json.RawMessage(backendArchiveSurfaces(t)),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// THE macos-user BOOT ARCHIVES INTO THE WORKSPACE — the second backend, through its own entry.
//
// ⚠ WHY THIS IS NOT THE SAME TEST AS THE CONTAINER ONE. The archive is anchored on the
// WORKSPACE, and the two backends learn what the workspace is by different routes: the
// container's Env defaults Workspace to the literal /workspace, while macos-user must
// TRANSLATE it out of $YOLO_DARWIN_WORKSPACE (DarwinEnvFrom). That translation has been wrong
// before — its own docstring records a darwin harness that left Workspace at the container
// default, after which "every generator writing a workspace sidecar failed on
// `mkdir /workspace: read-only file system`". With the archive in the picture that failure is
// no longer a sidecar's: archiveAdoption refuses the adoption when it has nowhere to put a
// copy, so a Workspace regression takes the whole native boot down. Nothing else in this suite
// runs RunDarwinBootstrap over an adopting surface, so nothing else would notice.
//
// Runs on Linux: the native bootstrap is pure Go over an *Env, which is what makes the
// macos-user generation path testable from in here at all.
func TestDarwinBootstrapArchivesTheAdoptedFileIntoTheWorkspace(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "account-home")
	ws := filepath.Join(base, "workspace")
	surface := filepath.Join(home, ".acme", "settings.json")
	if err := os.MkdirAll(filepath.Dir(surface), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface, []byte(backendArchiveSeed), 0o644); err != nil {
		t.Fatal(err)
	}

	// The launcher's env-var contract, translated by the ONE translation the real
	// `yolo internal darwin-bootstrap` uses — never an Env assembled here, which would be the
	// second implementation of that contract this test exists to guard.
	e := DarwinEnvFrom(map[string]string{
		"HOME":                  home,
		"JAIL_HOME":             home,
		"YOLO_HOST_DIR":         ws,
		"YOLO_BLOCK_CONFIG":     `[]`,
		"YOLO_MISE_TOOLS":       `{}`,
		"YOLO_PACK_ROOT":        stageBackendArchivePack(t),
		"YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv:    filepath.Join(ws, ".yolo", "home"),
		"MISE_DATA_DIR":         filepath.Join(home, ".yolo", "mise"),
	}, home)
	var log strings.Builder
	e.Stderr = &log
	// The bootstrap's own error is deliberately not fatal here, for darwinBootstrapHome's
	// reason: a temp home fails unrelated generators (no git, no node) and what this test
	// asserts is the ARCHIVE. It is reported in the failure message instead, because the
	// regression this test is about — no workspace to anchor on — arrives as one of those
	// failures rather than as a missing file.
	bootErr := RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	want := filepath.Join(ws, ".yolo", "archive", "config", "acme-settings", "settings.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the native bootstrap adopted a pre-existing surface and archived nothing at "+
			"%s: %v\n\nboot: %v\nstderr:\n%s\n\nmacos-user reaches per-workspace state by a "+
			"different primitive than the container — one account home for every workspace, "+
			"and $YOLO_DARWIN_WORKSPACE instead of the /workspace default — so the jail's half "+
			"of OQ-CO7 has to be measured on this entry too. If Workspace came back unset the "+
			"archive has nowhere to go and archiveAdoption refuses the adoption, which is why "+
			"the boot error above is worth reading first.", want, err, bootErr, log.String())
	}
	if string(got) != backendArchiveSeed {
		t.Errorf("the archive holds %q, want the file as yolo found it (%q)", got, backendArchiveSeed)
	}
	// And the copy is in the WORKSPACE, not in the one account home this backend shares
	// between every workspace — which is the whole reason the anchor was chosen.
	if _, err := os.Stat(filepath.Join(home, ".yolo", "archive")); err == nil {
		t.Errorf("macos-user put the adoption archive under the account home (%s) — that home "+
			"is shared by every workspace on the machine, so one workspace's original would be "+
			"keyed where another's can collide with it",
			filepath.Join(home, ".yolo", "archive"))
	}
}

// A FAILED ARCHIVE STOPS THE JAIL BOOT, and leaves the file exactly as the agent wrote it.
//
// ⚠ THE HOST'S REFUSAL TEST DOES NOT COVER THIS. The two notches carry the same refusal to
// different places: `RenderHostPack` turns an *rmwRefusedError into a per-surface
// HostRenderResult and renders the rest, while the boot loop hands it to genStep, where A12
// makes it fatal. Only the second is a claim about a boot, and the boot is the notch the ruling
// says the copy matters most at — there is no TTY here to ask the question the host prompt
// asks, so "adopt anyway and warn" would be a net that silently does not exist.
//
// The obstruction is a FILE where the archive root must be a directory, which fails MkdirAll as
// ENOTDIR. Chosen over a mode bit deliberately: this suite runs as root in the jail, where
// chmod stops nothing.
func TestJailAdoptionArchiveFailureStopsTheBoot(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	surface := filepath.Join(e.Home, ".acme", "settings.json")
	if err := os.MkdirAll(filepath.Dir(surface), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface, []byte(backendArchiveSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.Workspace, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	obstruction := filepath.Join(e.Workspace, ".yolo", "archive")
	if err := os.WriteFile(obstruction, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	ConfigurePackSurfaces(e, []*packload.Pack{backendArchivePack(t)})

	fails := e.GenFailures()
	if len(fails) == 0 {
		t.Fatalf("the boot adopted %s with no archive and reported no failure\n\nA12 makes a "+
			"generator failure fatal because boot must not hand the agent a half-configured "+
			"home; adopting without the copy is worse than that — it composes the whole file "+
			"out of what the file holds, with nothing left that records what it held, and "+
			"reports a successful render. If this went green because the stateful arm learned "+
			"to downgrade an rmw refusal the way the rmw arm does, that downgrade is the bug.",
			surface)
	}
	if joined := strings.Join(fails, "\n"); !strings.Contains(joined, obstruction) {
		t.Errorf("the boot failure does not name the archive path it could not write (%s):\n%s\n\n"+
			"A boot that refuses has to say which path to clear, or the jail is unstartable "+
			"with no way in to fix it.", obstruction, joined)
	}
	after, err := os.ReadFile(surface)
	if err != nil {
		t.Fatalf("read the surface back: %v", err)
	}
	if string(after) != backendArchiveSeed {
		t.Errorf("the surface was rewritten despite the refusal:\n got %q\nwant %q\n\n"+
			"The archive is written FIRST precisely so `the file is untouched` is true rather "+
			"than aspirational — if this went red, the archiveAdoption call moved below the "+
			"surface write.", after, backendArchiveSeed)
	}
}
