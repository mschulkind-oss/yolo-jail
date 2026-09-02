package run

// profilesoptions_test.go pins the YOLO_PROFILES half of the composed channel at BOTH
// notches that consume it, and the OQ-CS6 refusal as the launch pipeline applies it.
//
// The call-site discipline this package states elsewhere (providerpreflight_test.go's
// header, the assemble golden) applies double here, because the table crosses a boundary:
// a runner test that never called composePackChannel would stay green if the channel
// stopped resolving profiles, and the jail would then read an empty ctx.profile while
// every launch line still printed a selection. So the emission tests drive the REAL
// assembly (assembleRunCmd for the container argv, launchEnv for the macos-user plan
// env), and the refusal tests call the composer itself — the function run.go turns into
// "Refusing to launch".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// optionsZaiPack is a staged-shape pack (LoadDir) whose provider DECLARES options —
// `model` with a default and `thinking` declared with none — plus the profile that
// selects it. No shipped pack declares options yet, so the merge this exercises has no
// shipped fixture to borrow; the shape is what packs/zai grows into (§5.2's example).
func optionsZaiPack(t *testing.T) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), "zai")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"zai","contributes":[
	  {"kind":"provider","name":"zai",
	   "endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}},
	   "options":{"model":"default","thinking":null}},
	  {"kind":"profile","name":"zai","provider":"zai"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "zai", false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

// writeProfilesAtHome writes the user config's `profiles` key into whatever HOME is
// CURRENTLY set to — USER scope, the only scope the key reads (OQ-CS5), which is why
// these tests write the file rather than the merged cfg the assembly is handed. The
// two-arg shape exists because the assembly establishes its own temp HOME
// (assembleWithProfiles) and the file has to land in THAT one.
func writeProfilesAtHome(t *testing.T, profilesJSON string) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"profiles": `+profilesJSON+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeProfilesConfig is writeProfilesAtHome with a fresh HOME pointed at it first — the
// form the channel-level tests use, which compose without the assembly's own HOME.
func writeProfilesConfig(t *testing.T, profilesJSON string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	writeProfilesAtHome(t, profilesJSON)
	return home
}

// The container argv carries the RESOLVED table: the user's entry with the provider's
// declared default composed under it. The default's presence is the assertion — a table
// that relayed the user's entry verbatim would pass a membership check and deliver a
// derive a half-composed profile.
func TestAssembleEmitsTheResolvedProfilesTable(t *testing.T) {
	packs := []*packload.Pack{optionsZaiPack(t)}
	// Written inside the hook, so the file lands in the HOME the assembly itself chose.
	argv := assembleWithProfiles(t, newConfig(), packs, func(o *Options) {
		writeProfilesAtHome(t, `{"zai-fast": {"provider": "zai", "model": "fast"}}`)
	})
	got := envArgValues(argv, "YOLO_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_PROFILES emitted %q, want exactly one", got)
	}
	// `model` is the user's value over the declared default; `thinking` is declared with
	// none and the profile does not set it, so it composes nothing (OQ-CS7's null). The
	// fixture's OWN profile (`zai`, no user entry) is in the table too, with the declared
	// default — the table is the whole DECLARED set, not just what this launch activates,
	// and it is sorted by name.
	want := `YOLO_PROFILES={"zai": {"provider": "zai", "model": "default"}, ` +
		`"zai-fast": {"provider": "zai", "model": "fast"}}`
	if got[0] != want {
		t.Errorf("the env block should emit the resolved table %s, got %s", want, got[0])
	}
}

// The macos-user arm layers the same table into its plan env — the second emitter the
// contract names, and the one that silently delivered nothing before the channel was
// composed above the backend dispatch. Same launch, same JSON: the two backends cannot
// answer differently about what a profile resolves to.
func TestLaunchEnvCarriesTheResolvedProfilesTable(t *testing.T) {
	home := writeProfilesConfig(t, `{"zai-fast": {"provider": "zai", "model": "fast"}}`)
	o := goldenOptions("/ws", home)
	c := channelFor(t, o, newConfig(), []*packload.Pack{optionsZaiPack(t)}, emptyEnv())
	env := c.launchEnv()
	v, ok := env.Get("YOLO_PROFILES")
	if !ok {
		t.Fatalf("launchEnv carries no YOLO_PROFILES; keys=%v", env.Keys())
	}
	want := `{"zai": {"provider": "zai", "model": "default"}, ` +
		`"zai-fast": {"provider": "zai", "model": "fast"}}`
	if s, isStr := v.(string); !isStr || s != want {
		t.Errorf("launchEnv YOLO_PROFILES = %#v, want %s", v, want)
	}
}

// A launch line that names a profile nothing declares refuses, naming the name, the
// agent it was selected for and what IS declared — the OQ-CS6 reversal, applied where
// the old behaviour silently keyed a dead string onto every bin. Composed DIRECTLY
// rather than through assembleRunCmd because the assembly panics on a refused channel;
// run.go is the caller that renders this as "Refusing to launch".
func TestComposeRefusesAProfileNameNothingDeclares(t *testing.T) {
	writeProfilesConfig(t, `{}`)
	o := goldenOptions("/ws", t.TempDir())
	o.ProfileName = "dev"
	_, err := o.composePackChannel(newConfig(), packsFixture(t, "claude", "pi"), emptyEnv())
	if err == nil {
		t.Fatal("a -p name no pack or config entry declares must refuse the launch, not key silently")
	}
	for _, want := range []string{`profile "dev" selected for claude`, `declared: bedrock`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %s, got: %v", want, err)
		}
	}
}

// The census reaches the launch too: an option the resolved provider does not declare
// refuses through the same channel, with the message the lowering composes — so the
// provider's schema is enforced on every path to a derive, not only in the function
// that defines it.
func TestComposeRefusesAnUndeclaredOptionThroughTheChannel(t *testing.T) {
	writeProfilesConfig(t, `{"zai-fast": {"provider": "zai", "model": "fast", "temperature": "0.2"}}`)
	o := goldenOptions("/ws", t.TempDir())
	_, err := o.composePackChannel(newConfig(), []*packload.Pack{optionsZaiPack(t)}, emptyEnv())
	if err == nil {
		t.Fatal("an option the provider does not declare must refuse the launch")
	}
	for _, want := range []string{`option "temperature"`, `provider "zai"`, `declared: model, thinking`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %s, got: %v", want, err)
		}
	}
}
