package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// crossingFixture writes a TOML user config and a legacy JSONC workspace config, and returns the
// launch's Options for that workspace with HOME pointed at the user config's home.
func crossingFixture(t *testing.T, userTOML, wsJSONC string, extra map[string]string) *Options {
	t.Helper()
	emptyLoopholeDirs(t)
	home, _ := inheritHome(t, "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(userTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range extra {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	if wsJSONC != "" {
		if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(wsJSONC), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := goldenOptions(ws, home)
	o.Stdout, o.Stderr = &strings.Builder{}, &strings.Builder{}
	return o
}

// jailReadsCrossed writes what crossed into a jail home of its own and reads it as the jail does:
// the user config there, the same workspace, YOLO_VERSION set. It returns the jail's merged
// config, failing the test when the jail's reader refuses it.
func jailReadsCrossed(t *testing.T, o *Options, files []inheritScopeFile) {
	t.Helper()
	host := os.Getenv("HOME")
	jailHome := t.TempDir()
	for _, f := range files {
		p := filepath.Join(jailHome, f.rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", jailHome)
	t.Setenv("YOLO_VERSION", "0.0.0-crossing")
	defer func() {
		t.Setenv("HOME", host)
		t.Setenv("YOLO_VERSION", "")
	}()
	cfg, err := config.LoadConfig(o.Workspace, true, func(string) {})
	if err != nil {
		t.Fatalf("the jail does not read what crossed: %v", err)
	}
	if errs, _ := config.ValidateConfig(cfg, o.Workspace, loopholeResolver()); len(errs) > 0 {
		t.Fatalf("the jail refuses what crossed: %v", errs)
	}
}

// The owner's launch (LC-D4, LC-D5, LC-D6): a TOML user config naming a personal pack through a
// pack source handle, beside an unmigrated legacy JSONC workspace whose loopholes block switches
// host-processes (with visible and fields) and journal on. v0.15.1 launched it; the crossing then
// refused it as a scope that "mixes a TOML file and a legacy JSONC file". It crosses as TOML:
// the TOML handle exactly as written (never read as a relative path), and the legacy loopholes
// block as nothing, silently, since its readable reading is `packs`, `allow` and `requests`,
// none of which a jail inherits. Fails if the crossing refuses the scope again, respells the
// handle, or carries the block (or an `allow` table made from it) into the jail.
func TestTheOwnersMixedScopeCrossesAsTOML(t *testing.T) {
	o := crossingFixture(t, `# A migrated user config.
packs = [
  "sample-packs/house-rules",
  "pi",
  "journal",
  "host-processes",
]

[pack_sources.sample-packs]
path = "~/sample-dotfiles/sample-packs"
bare = true
`, `{
  "loopholes": {
    "host-processes": {"enabled": true, "settings": {"visible": ["postgres"], "fields": ["pid", "comm"]}},
    "journal": {"enabled": true}
  },
  "packages": ["jq"]
}`, map[string]string{"sample-dotfiles/sample-packs/house-rules/pack.toml": "name = \"house-rules\"\n"})
	files, err := o.inheritedScopeWithSelector("podman", false, nil)
	if err != nil {
		t.Fatalf("the crossing refused a config the host accepted: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want the preflight and nested files, got %d", len(files))
	}
	for _, f := range files {
		if f.format != configdoc.TOML {
			t.Errorf("%s crossed as %v, want TOML (a TOML-written pack handle has no JSONC route)", f.scope, f.format)
		}
		if !strings.Contains(f.body, `"sample-packs/house-rules"`) {
			t.Errorf("%s: the TOML pack handle did not cross as written:\n%s", f.scope, f.body)
		}
		doc, err := configdoc.Parse([]byte(f.body), configdoc.TOML)
		if err != nil {
			t.Fatalf("%s: %v", f.scope, err)
		}
		for _, absent := range []string{"loopholes", "allow", "requests"} {
			if _, present := doc.Values.Get(absent); present {
				t.Errorf("%s: the legacy loopholes block crossed (as %q):\n%s", f.scope, absent, f.body)
			}
		}
	}
	if out := o.Stderr.(*strings.Builder).String(); strings.Contains(out, "loopholes") {
		t.Errorf("a block that crosses as nothing is left out silently (LC-D6), but the launch said:\n%s", out)
	}
	jailReadsCrossed(t, o, files)
}

// LC-D6's one disclosure for a switch: `enabled: false` beside a selected pack has no spelling in
// the TOML user scope the jail reads, so a launch inside the jail would run the loophole. The
// crossing still delivers, and says so once (not once per scope file), naming the host file that
// wrote the switch. Fails if the switch is refused again, or dropped without a word.
func TestASelectedPacksLegacyOffSwitchIsDisclosedOnce(t *testing.T) {
	o := crossingFixture(t, "packs = [\"sample-packs/house-rules\", \"aws-auth\"]\n\n"+
		"[pack_sources.sample-packs]\npath = \"~/sample-dotfiles/sample-packs\"\nbare = true\n",
		`{"loopholes": {"aws-auth": {"enabled": false}}}`,
		map[string]string{"sample-dotfiles/sample-packs/house-rules/pack.toml": "name = \"house-rules\"\n"})
	files, err := o.inheritedScopeWithSelector("podman", false, nil)
	if err != nil {
		t.Fatalf("the crossing refused a config the host accepted: %v", err)
	}
	out := o.Stderr.(*strings.Builder).String()
	if n := strings.Count(out, "config.loopholes.aws-auth.enabled"); n != 1 {
		t.Fatalf("want the off switch disclosed once, got %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "yolo-jail.jsonc:") || !strings.Contains(out, "runs that loophole") {
		t.Errorf("the disclosure does not name the file and what the jail does without it:\n%s", out)
	}
	jailReadsCrossed(t, o, files)
}

// LC-D5: the crossing respells only what a legacy file wrote. A TOML-written handle crosses as
// written, and a legacy include's path entry in the same merged `packs` list is respelled in its
// place, as `yolo config migrate` respells it, with the `pack_sources` entry it needs. Fails if a
// TOML handle is read as a legacy path again (the crossing then left it out), or if the legacy
// path crosses as a path the jail's TOML reader refuses.
func TestAMixedPacksListRespellsOnlyItsLegacyEntries(t *testing.T) {
	o := crossingFixture(t, "include_if_found = [\"extra.jsonc\"]\npacks = [\"sample-packs/house-rules\"]\n\n"+
		"[pack_sources.sample-packs]\npath = \"~/sample-dotfiles/sample-packs\"\nbare = true\n",
		`{"loopholes": {"journal": {"enabled": true}}}`,
		map[string]string{
			"sample-dotfiles/sample-packs/house-rules/pack.toml": "name = \"house-rules\"\n",
			"other-packs/lab-tools/pack.toml":                    "name = \"lab-tools\"\n",
			".config/yolo-jail/extra.jsonc":                      `{"packs": ["~/other-packs/lab-tools"]}`,
		})
	files, err := o.inheritedScopeWithSelector("podman", false, nil)
	if err != nil {
		t.Fatalf("the crossing refused a config the host accepted: %v", err)
	}
	for _, f := range files {
		doc, err := configdoc.Parse([]byte(f.body), configdoc.TOML)
		if err != nil {
			t.Fatalf("%s: %v", f.scope, err)
		}
		list, _ := doc.Values.Get("packs")
		got := fmt.Sprint(list)
		if !strings.Contains(got, "sample-packs/house-rules") || !strings.Contains(got, "other-packs/lab-tools") {
			t.Errorf("%s: packs = %s, want the TOML handle as written and the legacy path respelled", f.scope, got)
		}
		if !strings.Contains(f.body, "~/other-packs") {
			t.Errorf("%s: the respelled entry's pack source did not cross:\n%s", f.scope, f.body)
		}
	}
	if out := o.Stderr.(*strings.Builder).String(); strings.Contains(out, "leaves out") {
		t.Errorf("nothing here lacks a TOML spelling, but the launch disclosed an omission:\n%s", out)
	}
	jailReadsCrossed(t, o, files)
}
