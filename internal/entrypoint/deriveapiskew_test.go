package entrypoint

// deriveapiskew_test.go pins the BOOT half of the `yolo.*` version boundary: not "the VM
// can tolerate an unknown API" (luahook/deriveapiskew_test.go pins that) but "a jail BOOTS
// with such a derive.lua staged, and says so on the way past".
//
// Same split, and for the same reason, as packskew_test.go makes for the manifest
// vocabulary: the tolerance is a decision of the CALL SITE (deriveComputedLayer passing
// UnknownAPI), so a test one layer down passes with the wiring deleted — the VM would
// still be capable of tolerance that nothing asks for, and every jail on an older image
// would refuse to start again with the suite green.
//
// THE INCIDENT this reproduces, as observed on a 0.8.0+881 host attaching to a 0.8.0+691
// image:
//
//	Error: configure_claude_config: surface claude/config: derive: lua transform error:
//	    <string>:51: attempt to call a non-function object
//	Error: configure_claude_settings: surface claude/settings: derive: lua transform error:
//	    <string>:51: attempt to call a non-function object
//	yolo-entrypoint: refusing to start the jail: 2 config generator(s) failed
//
// Line 51 was packs/claude/derive.lua's `yolo.env("claude", …)`, an API added after that
// image was baked. Note WHICH surfaces failed: config and settings, whose producers are
// registered at lines 4 and 26. The whole script is executed to register, so one unknown
// call at the bottom took down every surface above it — which is why the fixture below
// registers its producer BEFORE the unknown call, and why the assertion is on the rendered
// content and not merely on the absence of an error.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// newerAPIDeriveLua is the incident's geometry: a surface producer registered first, a
// call to an API this build does not have after it. `api_from_a_newer_yolo` stands in for
// what `yolo.env` was to a pre-f55f2109 image — a literal `yolo.env` would prove nothing,
// since this build knows it.
const newerAPIDeriveLua = `yolo.derive("acme", "settings", function(ctx)
  return { rendered = "yes" }
end)

yolo.api_from_a_newer_yolo("acme", function(ctx) return {} end)
`

// newerAPIPack owns ONE computed surface, so the boot loop renders it and the derive's
// return is the file's whole content.
func newerAPIPack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	   "path":"~/.acme/settings.json","mode":"computed"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"), newerAPIDeriveLua)
	p, problems := packload.LoadDir(dir, "acme", false)
	if p == nil {
		t.Fatalf("the acme fixture did not load: %v", problems)
	}
	return p
}

// bootWithNewerAPIDerive runs the real boot loop over the fixture and hands back the
// generator failures, the boot's stderr, and the Home the surface was rendered into.
func bootWithNewerAPIDerive(t *testing.T) (fails []string, stderr string, home string) {
	t.Helper()
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")
	ConfigurePackSurfaces(e, []*packload.Pack{newerAPIPack(t)})
	return e.GenFailures(), errw.String(), e.Home
}

// THE REGRESSION. A derive.lua calling an API this build does not have must not produce a
// generator failure — a generator failure is literally the "refusing to start the jail"
// the incident was.
//
// The rendered content is asserted too, and that is the part a weaker test would miss: the
// producer sits ABOVE the unknown call in the script, so tolerance that aborted the script
// at the unknown line would leave no producer registered, the surface would render its
// declared layers only, and the boot would be green while the derive silently stopped
// working.
func TestBootRendersSurfacesWhenADeriveCallsAnAPIThisBuildLacks(t *testing.T) {
	fails, stderr, home := bootWithNewerAPIDerive(t)
	if len(fails) != 0 {
		t.Fatalf("a derive.lua calling an unknown yolo.* API must not fail the boot — "+
			"this is exactly the refusal the incident was: %v\n%s", fails, stderr)
	}
	data, err := os.ReadFile(filepath.Join(home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("the surface must still be rendered: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	if got["rendered"] != "yes" {
		t.Errorf("the producer registered BEFORE the unknown call must still run: %v", got)
	}
}

// The skip must be AUDIBLE, and it must say the thing the Lua type error could not: which
// API, that this is version skew, and what to do about it. "attempt to call a non-function
// object" is unactionable, and the remedy — restart the jail so its image is rebuilt — is
// not something a reader can derive from the code.
func TestBootNamesTheUnknownDeriveAPIAndItsRemedy(t *testing.T) {
	_, stderr, _ := bootWithNewerAPIDerive(t)
	for _, want := range []string{
		"yolo.api_from_a_newer_yolo", // which API
		"acme",                       // whose derive.lua
		"version skew",               // what class of problem
		"restart the jail",           // the remedy, which no other skew note has to give
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the boot warning must contain %q; stderr was:\n%s", want, stderr)
		}
	}
}

// One unknown API is one finding, however many surfaces the pack declares. A pack's
// derive.lua is executed ONCE PER SURFACE (claude has two, and the incident printed its
// Lua error twice for exactly that reason), so an un-deduped note would repeat per surface
// and, across the boot's repeated pack reads, per read — the repetition warnOnce exists
// for. Keying the note by agent rather than by surface is what makes the dedupe possible;
// a note that named the surface could not be deduped at the sink.
func TestUnknownDeriveAPIIsStatedOncePerAgentAcrossSurfaces(t *testing.T) {
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	   "path":"~/.acme/settings.json","mode":"computed"}]},
	  {"kind":"config","config":[{"agent":"acme","name":"config","codec":"json",
	   "path":"~/.acme/config.json","mode":"computed"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"), newerAPIDeriveLua+
		"\nyolo.derive(\"acme\", \"config\", function(ctx) return { rendered = \"yes\" } end)\n")
	p, problems := packload.LoadDir(dir, "acme", false)
	if p == nil {
		t.Fatalf("fixture did not load: %v", problems)
	}

	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")
	ConfigurePackSurfaces(e, []*packload.Pack{p})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot failed: %v\n%s", fails, errw.String())
	}
	if n := strings.Count(errw.String(), "yolo.api_from_a_newer_yolo"); n != 1 {
		t.Errorf("the note was printed %d times; two surfaces sharing one derive.lua is "+
			"one finding:\n%s", n, errw.String())
	}
}

// The warn must be driven by REAL skew and not fire on every boot: an unconditional notice
// is one readers learn to skim, which costs exactly the boot this whole mechanism exists
// to explain.
func TestBootSaysNothingWhenEveryDeriveAPIIsKnown(t *testing.T) {
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	   "path":"~/.acme/settings.json","mode":"computed"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"),
		"yolo.derive(\"acme\", \"settings\", function(ctx) return { rendered = \"yes\" } end)\n"+
			"yolo.env(\"acme\", function(ctx) return {} end)\n")
	p, problems := packload.LoadDir(dir, "acme", false)
	if p == nil {
		t.Fatalf("fixture did not load: %v", problems)
	}

	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")
	ConfigurePackSurfaces(e, []*packload.Pack{p})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot failed: %v\n%s", fails, errw.String())
	}
	if strings.Contains(errw.String(), "unknown API") {
		t.Errorf("every yolo.* this script calls is registered by this build, so the boot "+
			"must not report skew:\n%s", errw.String())
	}
}

// EVERY SHIPPED derive.lua must execute cleanly on THIS build's VM — the guard tolerates
// an unknown API, it does not license shipping one.
//
// The trap is specific and already documented in packload/deriveenv.go: the entrypoint
// never INVOKES the env producer (that composition is host-side only), so `yolo.env` is
// inert in the jail — but the registration still has to be CALLABLE, because registration
// happens when the script is loaded. An API surface the entrypoint must expose but never
// uses is one nothing else in the suite exercises, and it is the exact shape that broke.
func TestEveryShippedDeriveScriptLoadsOnThisBuildsVM(t *testing.T) {
	checked := 0
	for _, p := range packload.Embedded() {
		script := packload.DeriveScript(p)
		if script == "" {
			continue
		}
		checked++
		// STRICT, deliberately (no UnknownAPI): this is the AUTHORING assertion, so a
		// shipped script naming an API this build lacks fails here rather than degrading
		// quietly in every jail — the same asymmetry packdecl.Decode keeps against
		// DecodeTolerant.
		//
		// The surface is one the pack does not declare, so no producer is invoked: what
		// is under test is the LOAD, which is where the incident's failure happened and
		// where an inert-but-unregistered API bites.
		_, err := (luahook.GopherLuaVM{}).Derive(script, &luahook.DeriveCtx{
			Agent: p.Name, Surface: "__load_probe__",
		})
		if err != nil {
			t.Errorf("packs/%s/derive.lua does not load on this build's VM — every "+
				"yolo.* it calls must be registered, including the ones the entrypoint "+
				"never invokes: %v", p.Name, err)
		}
	}
	if checked == 0 {
		t.Skip("no shipped derive.lua found")
	}
}
