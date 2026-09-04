package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// The six official packs must be EMBEDDED, materialize to real directories, and decode
// cleanly. If this fails the binary ships without them, which is the whole point of
// embedding — and the goSrc fileset trap makes that failure mode silent in the image.
func TestEmbeddedOfficialPacksMaterialize(t *testing.T) {
	dest := t.TempDir()
	got, problems := MaterializeEmbedded(packs.FS, dest)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	names := map[string]bool{}
	for _, p := range got {
		names[p.Name] = true
		if _, err := os.Stat(filepath.Join(p.Root, "pack.json")); err != nil {
			t.Errorf("%s: manifest not materialized: %v", p.Name, err)
		}
	}
	for _, want := range []string{"claude", "copilot", "opencode", "pi", "codex", "agy"} {
		if !names[want] {
			t.Errorf("official pack %q missing from the embed (extend packs/embed.go)", want)
		}
	}
}

// Every official pack's surfaces must decode through the SAME validator the Go literals
// used. A pack surface being data must not make it less checked.
func TestEmbeddedPackSurfacesDecode(t *testing.T) {
	dest := t.TempDir()
	got, _ := MaterializeEmbedded(packs.FS, dest)
	total := 0
	for _, p := range got {
		surfaces, problems := p.Surfaces()
		if len(problems) != 0 {
			t.Errorf("%s: surface problems: %v", p.Name, problems)
		}
		total += len(surfaces)
	}
	// The official packs carry 11 surfaces across the agents.
	// A drop here means a pack lost a surface in translation.
	if total != 11 {
		t.Errorf("official packs declare %d surfaces, want 11", total)
	}
}

// A pack's host-file declaration is HONORED whoever shipped it, and nothing is refused.
//
// THREE TESTS USED TO LIVE HERE, all asserting the opposite for a fetched pack:
// TestFetchedPackHostFilesRefusedAndReported, TestFetchedPackNativeInstallerRefusedButNpmAllowed
// and TestOriginGateIsPerInstallContribution. OQ-TP9 (docs/design/trust-paths.md,
// 2026-09-04) deleted the gate all three pinned — it refused an actor who had already
// passed a stronger one, since naming the pack means editing the user config as the host
// user — so they are replaced by their inverse, which is what goes red if a gate returns.
//
// It asserts through the packload accessors, which is the CALLEE. The call-site half is
// internal/cli/run/packnohostgate_test.go, where a genuinely fetched pack goes through
// stagePacks with no lockfile at all.
func TestHostFilesAndInstallersAreHonoredWithNoOriginGate(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `{"contributes":[
	  {"kind":"reads-host","host":".ssh/id_ed25519"},
	  {"kind":"program","bin":"safe","via":"npm","package":"safe-pkg"},
	  {"kind":"program","bin":"sharp","via":"installer","url":"https://acme.test/i.sh"}]}`)

	p, probs := LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}

	granted, refused := p.HonoredHostFiles()
	if len(granted) != 1 || granted[0].From != ".ssh/id_ed25519" {
		t.Errorf("the host file was not granted: %v", granted)
	}
	if len(refused) != 0 {
		t.Errorf("a host file was REFUSED: %v\nThe origin gate is deleted; a refusal here is "+
			"a gate that came back without a ruling", refused)
	}

	installs, refused := p.HonoredInstalls()
	if len(installs) != 2 {
		t.Fatalf("want both installs honored, got %d: %+v", len(installs), installs)
	}
	var sawInstaller bool
	for _, in := range installs {
		if in.InstallerURL == "https://acme.test/i.sh" {
			sawInstaller = true
		}
	}
	if !sawInstaller {
		t.Errorf("the curl-piped installer was dropped: %+v\nIt was refused for a fetched "+
			"pack until OQ-TP9, on the ground that a git ref must not execute arbitrary code "+
			"in the jail — refuted in-house because `npm install -g` from the same tree runs "+
			"postinstall, ungated (pack-execution-trust.md §2)", installs)
	}
	if len(refused) != 0 {
		t.Errorf("an install was REFUSED: %v", refused)
	}
}

// A skills-only pack needs NO manifest. That is the common case and must stay
// zero-ceremony, so a missing pack.json is not an error.
func TestPackWithNoManifestIsValid(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, "")
	if len(problems) != 0 {
		t.Fatalf("a manifest-less pack must be valid: %v", problems)
	}
	if p.Decl == nil {
		t.Fatal("Decl must never be nil")
	}
	// The name falls back to the directory.
	if p.Name != filepath.Base(root) {
		t.Errorf("name = %q, want the dir name", p.Name)
	}
}

// Unions must dedupe across packs: two packs declaring the same writable dir must
// produce one mount, not a duplicate the runtime would reject.
func TestUnionsDedupeAcrossPacks(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[{"kind":"state","at":".shared","scope":"workspace"},{"kind":"state","at":".a","scope":"workspace"},{"kind":"state","at":".creds","scope":"machine","because":"x"}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[{"kind":"state","at":".shared","scope":"workspace"},{"kind":"state","at":".b","scope":"workspace"},{"kind":"state","at":".creds","scope":"machine","because":"x"}]}`)}
	if got := WritableDirs([]*Pack{a, b}); len(got) != 3 {
		t.Errorf("WritableDirs = %v, want 3 deduped", got)
	}
	if got := SharedDirs([]*Pack{a, b}); len(got) != 1 {
		t.Errorf("SharedDirs = %v, want 1 deduped", got)
	}
}

// Launch-flag injection must preserve declared order and skip a flag the user already
// passed — including via a declared ALIAS, so `-y` suppresses `--yolo`.
func TestInjectLaunchFlags(t *testing.T) {
	p := &Pack{Name: "p", Decl: declFrom(t,
		`{"contributes":[{"kind":"launch","bin":"tool","flags":["--yolo","--no-update"],"aliases":{"--yolo":["-y"]}}]}`)}
	loaded := []*Pack{p}

	got := InjectLaunchFlags(loaded, []string{"tool", "sub"})
	want := []string{"tool", "--yolo", "--no-update", "sub"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
	// Alias suppression.
	got = InjectLaunchFlags(loaded, []string{"tool", "-y"})
	if strings.Contains(strings.Join(got, " "), "--yolo") {
		t.Errorf("-y must suppress --yolo: %v", got)
	}
	// A binary no pack declares is untouched.
	if got := InjectLaunchFlags(loaded, []string{"ls", "-la"}); len(got) != 2 {
		t.Errorf("undeclared binary must be untouched: %v", got)
	}
}

// declFrom parses a manifest body for the union/injection tests.
func declFrom(t *testing.T, body string) *packdecl.Manifest {
	t.Helper()
	m, problems := packdecl.Decode([]byte(body))
	if len(problems) != 0 {
		t.Fatalf("fixture manifest invalid: %v", problems)
	}
	return m
}

func writeManifest(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirSupportsPackJSONC(t *testing.T) {
	dir := t.TempDir()
	jsoncContent := `{
		// A pack manifest using jsonc
		"name": "my-jsonc-pack",
		"description": "testing pack.jsonc discovery",
		"contributes": [
			{
				"kind": "skills",
				"into": ".custom/skills",
			},
		],
	}`
	if err := os.WriteFile(filepath.Join(dir, "pack.jsonc"), []byte(jsoncContent), 0o644); err != nil {
		t.Fatal(err)
	}

	p, problems := LoadDir(dir, "my-jsonc-pack")
	if len(problems) != 0 {
		t.Fatalf("LoadDir failed on pack.jsonc: %v", problems)
	}
	if p.Name != "my-jsonc-pack" {
		t.Errorf("p.Name = %q, want my-jsonc-pack", p.Name)
	}
	if len(p.Decl.Contributes) != 1 || p.Decl.Contributes[0].Into != ".custom/skills" {
		t.Errorf("contributions = %+v, want 1 skills contribution", p.Decl.Contributes)
	}
}
