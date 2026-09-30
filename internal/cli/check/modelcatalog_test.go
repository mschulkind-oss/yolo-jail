package check

// modelcatalog_test.go pins MM-D16 (docs/design/model-lists-and-pickers.md §9): `yolo check` warns
// about a model id a composed list names that no installed agent's own catalog knows, and says it
// could not ask when no catalog could be read. Every cell drives sectionPacks, the section the
// report is printed from, so deleting the call in packs.go turns each one red; the catalog is a
// fixture package on disk, never a real agent, and no agent program runs.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// catalogPack is a local pack declaring a provider whose list names alpha-1, ghost-9 and a
// provider reached only at localhost, beside an npm program declaring its catalog files.
func catalogPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
  "name": "catalogpack",
  "contributes": [
    {"kind": "provider", "name": "gw",
     "endpoints": {"openai": {"base_url": "https://gw.example.test/v1"}},
     "models": {"default": "alpha-1", "alpha-1": "alpha-1", "ghost-9": "ghost-9"}},
    {"kind": "provider", "name": "mine",
     "endpoints": {"openai": {"base_url": "http://localhost:8080/v1"}},
     "models": {"default": "llama-local"}},
    {"kind": "program", "bin": "agentx", "via": "npm", "package": "@test/agentx",
     "model_catalog": ["node_modules/@test/ai/data/*.json"]}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// installAgentx writes a fixture install of agentx's package under prefix's node_modules, its
// catalog naming ids.
func installAgentx(t *testing.T, pkgDir string, ids ...string) {
	t.Helper()
	data := filepath.Join(pkgDir, "node_modules", "@test", "ai", "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"),
		[]byte(`{"name": "@test/agentx", "version": "1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	models := map[string]any{}
	for _, id := range ids {
		models["chat:"+id] = map[string]any{"id": id, "name": id}
	}
	b, _ := json.Marshal(map[string]any{"openai-completions": models})
	if err := os.WriteFile(filepath.Join(data, "gw.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCatalogCheck runs sectionPacks over the catalog pack in a jail whose npm prefix is prefix.
func runCatalogCheck(t *testing.T, prefix string) (string, *reporter) {
	t.Helper()
	packsFixture(t, `{"packs": ["file://`+catalogPack(t)+`"]}`)
	env := map[string]string{"YOLO_VERSION": "test", "NPM_CONFIG_PREFIX": prefix}
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: func(k string) string { return env[k] }}).sectionPacks(r, jsonx.NewOrderedMap())
	return buf.String(), r
}

// An id the installed agent's catalog does not name is a WARNING naming the provider and the id,
// and saying which catalog was read; an id it does name is not; a provider reached only at a
// local address is not checked at all (MM-D20).
func TestCheckWarnsAboutAListedIDNoInstalledCatalogKnows(t *testing.T) {
	prefix := t.TempDir()
	installAgentx(t, filepath.Join(prefix, "lib", "node_modules", "@test", "agentx"), "alpha-1")
	out, r := runCatalogCheck(t, prefix)
	want := `Model list: provider "gw" lists "ghost-9", which no installed agent's catalog knows`
	if !strings.Contains(out, want) {
		t.Fatalf("the check should say %q:\n%s", want, out)
	}
	if !strings.Contains(out, "checked against agentx 1.0.0's catalog") {
		t.Errorf("the warning must name the catalog it read:\n%s", out)
	}
	for _, absent := range []string{`"alpha-1", which`, "llama-local"} {
		if strings.Contains(out, absent) {
			t.Errorf("the check must not warn about %s:\n%s", absent, out)
		}
	}
	if r.failed != 0 {
		t.Errorf("an unknown id must WARN, never fail: the launch does not refuse it:\n%s", out)
	}
}

// With no agent installed the check says it COULD NOT ASK, as a skip, naming why for the agent
// whose pack declares a catalog. "Not found" and "could not ask" are different answers.
func TestCheckSaysItCouldNotAskWhenNoCatalogIsInstalled(t *testing.T) {
	out, r := runCatalogCheck(t, t.TempDir())
	want := "Model list: 2 listed model ids not checked against an agent's own catalog — no " +
		"installed agent's catalog could be read"
	if !strings.Contains(out, want) {
		t.Fatalf("the check should say %q:\n%s", want, out)
	}
	if !strings.Contains(out, "agentx: not installed in this jail yet") {
		t.Errorf("the skip must say why agentx's catalog was not read:\n%s", out)
	}
	if strings.Contains(out, "which no installed agent's catalog knows") || r.skipped == 0 {
		t.Errorf("with no catalog read nothing is \"not found\"; it is a skip:\n%s", out)
	}
}

// A catalog that names every listed id passes, naming what it read.
func TestCheckPassesWhenEveryListedIDIsKnown(t *testing.T) {
	prefix := t.TempDir()
	installAgentx(t, filepath.Join(prefix, "lib", "node_modules", "@test", "agentx"), "alpha-1", "ghost-9")
	out, _ := runCatalogCheck(t, prefix)
	if want := "Model list: every listed model id is in an installed agent's catalog (agentx 1.0.0)"; !strings.Contains(out, want) {
		t.Errorf("the check should say %q:\n%s", want, out)
	}
}

// AT THE HOST the catalog is read from yolo's own floor copy of the agent, the one `yolo host --`
// runs: the record's npm package directory.
func TestCheckReadsTheHostFloorsCopyAtTheHost(t *testing.T) {
	floorDir := t.TempDir()
	provisionAgentx(t, floorDir, "alpha-1")
	out, _ := runHostCatalogCheck(t, &hostfloor.Floor{Dir: floorDir, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if want := `provider "gw" lists "ghost-9", which no installed agent's catalog knows`; !strings.Contains(out, want) {
		t.Errorf("the host check should say %q from the floor's copy:\n%s", want, out)
	}
}

// A FLOOR COPY `yolo host` DOES NOT RUN IS NOT READ. With `host_floor` leaving the pack out, the
// program has no floor entry and `yolo host -- agentx` runs the copy on its PATH (OQ-HE11), so the
// copy the floor still holds from before is one no launch runs: its catalog is not the installed
// agent's, and the check says it could not ask, naming why, rather than checking against it or
// telling the user that a launch would install one.
func TestCheckDoesNotReadAFloorCopyYoloHostDoesNotRun(t *testing.T) {
	floorDir := t.TempDir()
	provisionAgentx(t, floorDir, "alpha-1")
	out, r := runHostCatalogCheck(t, &hostfloor.Floor{Dir: floorDir, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Include: func(string) bool { return false }})
	if strings.Contains(out, "which no installed agent's catalog knows") || strings.Contains(out, "checked against agentx") {
		t.Errorf("the check read the catalog of a floor copy `yolo host` does not run:\n%s", out)
	}
	if !strings.Contains(out, "no installed agent's catalog could be read") || r.skipped == 0 {
		t.Errorf("with no copy `yolo host` runs readable, the check must say it could not ask:\n%s", out)
	}
	if !strings.Contains(out, "agentx: no floor entry: the user config's `host_floor` leaves pack") ||
		!strings.Contains(out, "runs the one on the PATH") {
		t.Errorf("the skip must say why the floor holds no entry and what `yolo host` runs instead:\n%s", out)
	}
	if strings.Contains(out, "installs one") || strings.Contains(out, "installs it") {
		t.Errorf("no launch installs a program the floor holds no entry for:\n%s", out)
	}
}

// provisionAgentx makes agentx a PROVISIONED floor entry under floorDir, the copy `yolo host --
// agentx` runs: a record whose Exec and launcher exist, over a fixture install whose catalog
// names ids.
func provisionAgentx(t *testing.T, floorDir string, ids ...string) {
	t.Helper()
	installDir := filepath.Join(floorDir, "programs", "agentx", "1")
	rec := &hostfloor.Record{Schema: 1, Bin: "agentx", Pack: "catalogpack", Via: "npm",
		Declared: "@test/agentx@latest", Dir: installDir}
	pkg := rec.NpmPackageDir("@test/agentx")
	installAgentx(t, pkg, ids...)
	entry := filepath.Join(pkg, "bin", "cli")
	launcher := filepath.Join(floorDir, "bin", "agentx")
	for _, f := range []string{entry, launcher} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rec.Entry, rec.Exec = entry, []string{entry}
	b, _ := json.Marshal(rec)
	if err := os.MkdirAll(filepath.Join(floorDir, "records"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(floorDir, "records", "agentx.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runHostCatalogCheck runs sectionPacks over the catalog pack at the host, reading floor.
func runHostCatalogCheck(t *testing.T, floor *hostfloor.Floor) (string, *reporter) {
	t.Helper()
	packsFixture(t, `{"packs": ["file://`+catalogPack(t)+`"]}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	o := &Options{
		Getenv:    func(string) string { return "" },
		HostFloor: func([]hostfloor.Program) *hostfloor.Floor { return floor },
	}
	o.sectionPacks(r, jsonx.NewOrderedMap())
	return buf.String(), r
}

// IN A JAIL WHOSE ENVIRONMENT NAMES NO NPM PREFIX the catalog is read where the jail's launchers
// install the agent: $NPM_CONFIG_PREFIX, else $HOME/.npm-global, the one rule the entrypoint's Env
// and every generated launcher resolve the prefix by. A process whose environment carries HOME and
// no prefix (the macos-user sandbox's closed environment list names none) must not be told there
// is nowhere to look while the agent sits at the default prefix.
func TestCheckReadsTheJailsDefaultNpmPrefix(t *testing.T) {
	home := t.TempDir()
	installAgentx(t, filepath.Join(home, ".npm-global", "lib", "node_modules", "@test", "agentx"), "alpha-1")
	packsFixture(t, `{"packs": ["file://`+catalogPack(t)+`"]}`)
	env := map[string]string{"YOLO_VERSION": "test", "HOME": home}
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: func(k string) string { return env[k] }}).sectionPacks(r, jsonx.NewOrderedMap())
	out := buf.String()
	if want := `provider "gw" lists "ghost-9", which no installed agent's catalog knows`; !strings.Contains(out, want) {
		t.Errorf("the check should read the catalog at $HOME/.npm-global and say %q:\n%s", want, out)
	}
}

// THE SHIPPED DECLARATION READS pi 0.99.1's LAYOUT: its catalog is one JSON file per provider under
// pi-ai's dist/providers/data, {"<api>": {"chat:<id>": {"id": "<id>", …}}} (MEASURED 2026-09-30 on
// the installed 0.99.1). A fixture install in that shape, under a jail's npm prefix, is read by the
// check through packs/pi's own `model_catalog`, so a declaration edited away from that layout fails
// here rather than leaving every check a skip.
func TestCheckReadsPisCatalogThroughTheShippedDeclaration(t *testing.T) {
	prefix := t.TempDir()
	pkg := filepath.Join(prefix, "lib", "node_modules", "@earendil-works", "pi-coding-agent")
	data := filepath.Join(pkg, "node_modules", "@earendil-works", "pi-ai", "dist", "providers", "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"),
		[]byte(`{"name": "@earendil-works/pi-coding-agent", "version": "0.99.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "openai-codex.json"), []byte(`{"openai-codex-responses": `+
		`{"chat:gpt-6-astra": {"id": "gpt-6-astra", "provider": "openai-codex", "compat": {}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": ["pi"]}`)
	env := map[string]string{"YOLO_VERSION": "test", "NPM_CONFIG_PREFIX": prefix}
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: func(k string) string { return env[k] }}).sectionPacks(r, jsonx.NewOrderedMap())
	out := buf.String()
	if !strings.Contains(out, "pi 0.99.1") || strings.Contains(out, "catalog could be read") {
		t.Errorf("the check must read pi's catalog through packs/pi's declaration:\n%s", out)
	}
	if strings.Contains(out, `"gpt-6-astra"`) {
		t.Errorf("an id the fixture catalog names must not be warned about:\n%s", out)
	}
}
