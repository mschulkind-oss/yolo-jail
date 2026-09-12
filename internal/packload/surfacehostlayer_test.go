package packload

// surfacehostlayer_test.go pins the restructure OQ-CO10 rules
// (docs/design/config-ownership-and-promotion.md §5.1.1): a config surface's host layer is
// declared ON THE SURFACE, and the binding between the declaration, the mount the launcher
// emits and the path the jail reads is one expression rather than a string match.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// writePack writes a manifest into a fresh staged-looking directory and loads it the way
// both halves do.
func writePack(t *testing.T, dir, name, manifestJSON string) *Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("LoadDir(%s): %v", name, problems)
	}
	return p
}

// THE BINDING IS THE DECLARATION, and this is the fixture the retired one got wrong.
//
// Until 2026-09-12 a surface bound to a `reads-host` grant when the two paths shared a
// FINAL COMPONENT (packload.hostSourceFor's path.Base match), which had three unchosen
// consequences: the directory was ignored, two same-named surfaces both took the first
// matching grant, and a basename that stopped matching un-bound the surface SILENTLY.
// This pack is built so the old rule and the new one disagree on every surface: the grant's
// basename matches BOTH surfaces and neither of them is what it names.
func TestSurfaceHostLayerBindsToTheDeclarationNotABasename(t *testing.T) {
	p := writePack(t, "acme", "acme", `{"name":"acme","contributes":[
	  {"kind":"reads-host","host":".elsewhere/settings.json","into":"host-acme/elsewhere.json"},
	  {"kind":"config","config":[
	    {"agent":"acme","name":"settings","codec":"json","path":"~/.acme/settings.json",
	     "readsHost":true,"managed":{"x":1}},
	    {"agent":"acme","name":"other","codec":"json","path":"~/.other/settings.json",
	     "managed":{"y":1}}]}]}`)

	surfaces, problems := p.Surfaces()
	if len(problems) != 0 {
		t.Fatalf("Surfaces: %v", problems)
	}
	by := map[string]manifest.Surface{}
	for _, s := range surfaces {
		by[s.Name] = s
	}
	declared, quiet := by["settings"], by["other"]

	if want := "/ctx/host-acme/settings.json"; declared.HostSource != want {
		t.Errorf("the declaring surface reads %q, want %q — the /ctx path must be derived "+
			"from the surface's own path under the pack's staged dir",
			declared.HostSource, want)
	}
	if !declared.HasHostLayer() {
		t.Error("HasHostLayer is false for a surface that declares readsHost")
	}
	// The one the basename match would have bound, and the reason it must not: the pack
	// granted `.elsewhere/settings.json`, which is not this surface's file. Composing it
	// here would put one file's bytes into another file's layer stack.
	if quiet.HostSource != "" || quiet.HasHostLayer() {
		t.Errorf("acme/other has a host layer (%q) it never declared — a grant that merely "+
			"shares a basename must not bind to a surface", quiet.HostSource)
	}
}

// The DECLARED grant and the SURFACE-DERIVED one both reach the mount, through one
// accessor. The launcher emits a `-v` per entry of HonoredHostFiles and nothing else, so a
// surface whose grant did not appear here would declare a host layer the jail then refuses
// to compose without — the fail-closed read turning a missing mount into a refused launch.
func TestHonoredHostFilesCarriesBothDeclarations(t *testing.T) {
	p := writePack(t, "acme", "acme", `{"name":"acme","contributes":[
	  {"kind":"reads-host","host":".elsewhere/creds.json","into":"host-acme/creds.json"},
	  {"kind":"config","config":[
	    {"agent":"acme","name":"settings","codec":"json","path":"~/.acme/settings.json",
	     "readsHost":true,"managed":{"x":1}}]}]}`)

	granted, refused := p.HonoredHostFiles()
	if len(refused) != 0 {
		t.Errorf("refused = %v, want none (OQ-TP9 deleted that gate)", refused)
	}
	var froms []string
	for _, hf := range granted {
		froms = append(froms, hf.From)
	}
	want := []string{".elsewhere/creds.json", ".acme/settings.json"}
	if strings.Join(froms, ",") != strings.Join(want, ",") {
		t.Errorf("HonoredHostFiles = %v, want %v — the contribution and the surface are two "+
			"declarations of one boundary, and every consumer reads it through this one call",
			froms, want)
	}
}

// A `readsHost` surface is DISCLOSED exactly as the contribution was: same kind, same
// target, same review flag. The launch banner is the only place a user learns that a file
// leaves their home (run.disclosedClaims filters FootprintOf's ReviewWorthy claims), so the
// declaration moving must not take the disclosure with it.
func TestSurfaceHostLayerIsDisclosedAsAReadsHostClaim(t *testing.T) {
	for _, p := range Embedded() {
		if p.Name != "claude" {
			continue
		}
		var found bool
		for _, c := range FootprintOf(p).Claims {
			if c.Kind == "reads-host" && c.Target == ".claude/settings.json" {
				found = true
				if !c.ReviewWorthy {
					t.Error("the claim is not ReviewWorthy, so the launch banner drops it — " +
						"a host read that nothing discloses is the state OQ-TP9 left " +
						"disclosure as the only protection against")
				}
			}
		}
		if !found {
			t.Error("packs/claude reads ~/.claude/settings.json and its footprint does not " +
				"say so — FootprintOf walks contributions, and this declaration is on the " +
				"surface now")
		}
		return
	}
	t.Fatal("no embedded claude pack")
}

// COVERAGE IS A VISIBLE PER-SURFACE YES/NO, which is the third thing OQ-CO10 buys. Two
// surfaces have a host layer and every other shipped surface is a deliberate NO — including
// core's mise/config, which is yolo's own file and has no user twin to read.
func TestExactlyTwoShippedSurfacesDeclareAHostLayer(t *testing.T) {
	want := map[string]string{
		"claude/settings": "/ctx/host-claude/settings.json",
		"pi/settings":     "/ctx/host-pi/settings.json",
	}
	got := map[string]string{}
	for _, p := range Embedded() {
		surfaces, problems := p.Surfaces()
		if len(problems) != 0 {
			t.Fatalf("pack %s: %v", p.Name, problems)
		}
		for _, s := range surfaces {
			if s.HasHostLayer() {
				got[s.Key().String()] = s.HostSource
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("surfaces with a host layer = %v, want %v — the set is short on purpose; a "+
			"new one carries a file out of the user's home and must be a decision", got, want)
	}
	for id, src := range want {
		if got[id] != src {
			t.Errorf("%s reads %q, want %q", id, got[id], src)
		}
	}
}

// The MIGRATION refusal, and the boundary it respects.
//
// A pack that still declares its own config surface's file as a `reads-host` contribution
// gets the old silent un-binding otherwise: the grant no longer binds, the surface composes
// without the user's file, and nothing says so. It is refused where an author can act on it
// — the strict decode, which is `yolo pack lint` and every host-side load — and TOLERATED
// across the version boundary, where the jail must boot against a manifest some other build
// wrote (packdecl.retiredFieldProblems states that rule and the incident that set it).
func TestReadsHostForThePacksOwnSurfaceIsRefusedWhenAuthoringAndToleratedInTheJail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"name":"acme","contributes":[
	  {"kind":"reads-host","host":".acme/settings.json","into":"host-acme/settings.json"},
	  {"kind":"config","config":[
	    {"agent":"acme","name":"settings","codec":"json","path":"~/.acme/settings.json",
	     "managed":{"x":1}}]}]}`
	if err := os.WriteFile(filepath.Join(src, "pack.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	_, problems := LoadDir(src, "acme")
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want the one migration refusal", problems)
	}
	for _, want := range []string{"readsHost", "acme/settings", ".acme/settings.json"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("the refusal does not name %q, so the fix is not in the message:\n%s",
				want, problems[0])
		}
	}

	// The jail's half: tolerant, because this is a version-skew fact.
	restore := tolerateUnknownFields
	tolerateUnknownFields = true
	t.Cleanup(func() { tolerateUnknownFields = restore })
	if _, problems := LoadDir(src, "acme"); len(problems) != 0 {
		t.Errorf("the tolerant load refused: %v\nA jail that cannot start is worse than a "+
			"surface rendered without its host layer, and the host half already said so",
			problems)
	}
}

// Two surfaces of one pack may not derive the same /ctx destination. The destination is
// keyed on the file's basename, so this pair would land on one mount and the second surface
// would compose the first's bytes.
func TestTwoSurfacesMayNotClaimOneHostFile(t *testing.T) {
	p := writePack(t, "acme", "acme", `{"name":"acme","contributes":[
	  {"kind":"config","config":[
	    {"agent":"acme","name":"a","codec":"json","path":"~/.a/settings.json",
	     "readsHost":true,"managed":{"x":1}},
	    {"agent":"acme","name":"b","codec":"json","path":"~/.b/settings.json",
	     "readsHost":true,"managed":{"y":1}}]}]}`)

	_, problems := p.Surfaces()
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want the one collision", problems)
	}
	for _, want := range []string{"acme/a", "acme/b", "/ctx/host-acme/settings.json"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("the refusal does not name %q:\n%s", want, problems[0])
		}
	}
}

// `readsHost` on a path that is not in the user's home names nothing, and is refused at
// DECODE rather than resolved to an empty HostSource — an empty derived path is exactly the
// silent un-binding this restructure removed.
func TestReadsHostNeedsAHomeRelativePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"acme","contributes":[
	  {"kind":"config","config":[
	    {"agent":"acme","name":"settings","codec":"json","path":"/etc/acme/settings.json",
	     "readsHost":true,"managed":{"x":1}}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, "acme")
	if len(problems) != 0 {
		t.Fatalf("LoadDir: %v", problems)
	}
	_, probs := p.Surfaces()
	if len(probs) != 1 || !strings.Contains(probs[0], "readsHost") {
		t.Fatalf("problems = %v, want one naming readsHost", probs)
	}
}
