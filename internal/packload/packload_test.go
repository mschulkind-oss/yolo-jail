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
		// Embedded packs carry yolo's own authority, so they may declare host access.
		if !p.MayAccessHost {
			t.Errorf("%s: an embedded pack must be allowed host access", p.Name)
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
	// The registry had 11 surfaces across the six agents; mise's is not an agent's, so
	// the packs carry 10. A drop here means a pack lost a surface in translation.
	if total != 10 {
		t.Errorf("official packs declare %d surfaces, want 10", total)
	}
}

// A FETCHED pack's host-file declaration must be REFUSED, and the refusal reported: a
// pack silently not getting what it asked for changes the jail's contents, so the user
// has to be told.
func TestFetchedPackHostFilesRefusedAndReported(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `{"contributes":[{"kind":"reads-host","host":".ssh/id_ed25519"}]}`)

	fetched, _ := LoadDir(root, "evil", false)
	granted, refused := fetched.HonoredHostFiles()
	if len(granted) != 0 {
		t.Errorf("a fetched pack must not be granted host files, got %v", granted)
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "id_ed25519") {
		t.Errorf("refusal must name the file: %v", refused)
	}

	// The same declaration from an embedded/local pack IS honored.
	local, _ := LoadDir(root, "mine", true)
	if g, r := local.HonoredHostFiles(); len(g) != 1 || len(r) != 0 {
		t.Errorf("a local pack should be granted: granted=%v refused=%v", g, r)
	}
}

// A curl-piped installer is gated like a host file: a fetched pack introducing one would
// let a git ref run arbitrary code in the jail. An npm package is NOT gated — that is
// the same trust as any dependency the user already installs.
func TestFetchedPackNativeInstallerRefusedButNpmAllowed(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `{"contributes":[{"kind":"program","bin":"x","via":"installer","url":"https://evil/sh"}]}`)
	fetched, _ := LoadDir(root, "evil", false)
	if in, refused := fetched.HonoredInstalls(); len(in) != 0 || len(refused) != 1 {
		t.Errorf("a fetched native installer must be refused: in=%v refused=%v", in, refused)
	}

	npmRoot := t.TempDir()
	writeManifest(t, npmRoot, `{"contributes":[{"kind":"program","bin":"x","via":"npm","package":"x"}]}`)
	npm, _ := LoadDir(npmRoot, "ok", false)
	if in, refused := npm.HonoredInstalls(); len(in) != 1 || len(refused) != 0 {
		t.Errorf("an npm install must be allowed even when fetched: in=%v refused=%v", in, refused)
	}
}

// The origin gate is PER CONTRIBUTION, which only became expressible once the accessor
// went plural. A fetched pack mixing an npm install with a curl-to-shell installer keeps
// the npm one and loses ONLY the installer — deciding once for the whole pack would
// either refuse the innocent npm install or, far worse, smuggle the installer URL through
// beside it.
func TestOriginGateIsPerInstallContribution(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `{"contributes":[
	  {"kind":"program","bin":"safe","via":"npm","package":"safe-pkg"},
	  {"kind":"program","bin":"sharp","via":"installer","url":"https://evil/sh"}]}`)

	fetched, _ := LoadDir(root, "mixed", false)
	granted, refused := fetched.HonoredInstalls()
	if len(granted) != 1 || granted[0].Bin != "safe" {
		t.Errorf("the npm install must survive a fetched origin: %+v", granted)
	}
	for _, in := range granted {
		if in.InstallerURL != "" {
			t.Errorf("a fetched pack must never be granted an installer URL: %+v", in)
		}
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "https://evil/sh") {
		t.Errorf("the refusal must name the installer URL: %v", refused)
	}

	// The same pair from a local pack: both honored.
	local, _ := LoadDir(root, "mixed", true)
	if granted, refused := local.HonoredInstalls(); len(granted) != 2 || len(refused) != 0 {
		t.Errorf("a local pack gets both installs: granted=%+v refused=%v", granted, refused)
	}
}

// A skills-only pack needs NO manifest. That is the common case and must stay
// zero-ceremony, so a missing pack.json is not an error.
func TestPackWithNoManifestIsValid(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, "", false)
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
