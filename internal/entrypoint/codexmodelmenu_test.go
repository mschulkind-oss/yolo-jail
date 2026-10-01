package entrypoint

// codexmodelmenu_test.go pins codex's model menu (docs/design/model-lists-and-pickers.md MM-D9,
// MM-D22) from the shipped declaration to the argv codex is exec'd with. A boot render writes
// yolo's openai-codex list to the path packs/codex's `model_menu` reads it from; the launcher
// generated from the shipped codex program runs `yolo internal model-menu` (this test binary,
// re-executed) against a stub codex that prints a catalog for `debug models --bundled`; and the
// stub records the argv it was finally run with. Deleting the derive, the surface, the template's
// call, the splice or the declaration fails one of these. No codex runs.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// renderCodexModelList runs one boot render of the codex pack's surfaces into home, with the
// shipped provider table and profiles, and returns the model-list file it wrote.
func renderCodexModelList(t *testing.T, home, use string) map[string]any {
	t.Helper()
	packs := testPacksForAgent(t, "codex", "openrouter")
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e := &Env{Home: home, Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, table),
		"YOLO_USE_PROFILES": use,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	withCtxRoot(t, t.TempDir(), "codex")
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	menu := shippedCodexInstall(t).ModelMenu
	raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(menu.List)))
	if err != nil {
		t.Fatalf("the boot wrote no list where codex's model_menu reads it (%s): %v", menu.List, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the list is not JSON: %v\n%s", err, raw)
	}
	return doc
}

func listIDs(doc map[string]any) []any {
	models, _ := doc["models"].([]any)
	var ids []any
	for _, m := range models {
		ids = append(ids, m.(map[string]any)["id"])
	}
	return ids
}

// THE LIST IS THE SUBSCRIPTION'S, AND ONLY ON openai-codex (MM-D9): its ids in the declared order,
// named, with no [1m] rows, which codex's catalog has none of. Any other selection is no menu.
func TestCodexModelListIsTheSubscriptionsListOnly(t *testing.T) {
	doc := renderCodexModelList(t, t.TempDir(), `{"codex":"codex"}`)
	if got, want := listIDs(doc), []any{"gpt-6.1-sol", "gpt-6-astra", "gpt-6-luna"}; !reflect.DeepEqual(got, want) {
		t.Errorf("codex's list on -p codex = %v, want %v, packs/openai-auth's ids in order", got, want)
	}
	models, _ := doc["models"].([]any)
	if len(models) > 0 {
		if first := models[0].(map[string]any); first["name"] != "GPT-6.1 Sol" {
			t.Errorf("first entry = %v, want the declared name GPT-6.1 Sol", first)
		}
	}
	other := renderCodexModelList(t, t.TempDir(), `{"codex":"openrouter"}`)
	if ids := listIDs(other); len(ids) != 0 {
		t.Errorf("codex's list on -p openrouter = %v, want none: codex's catalog carries only the "+
			"subscription's ids", ids)
	}
}

// codexMenuCatalog is a catalog of the shape codex 0.159.2's `debug models --bundled` prints: the
// subscription's ids but gpt-6-luna, each with prompt text, one with an availability notice and
// one with an upgrade the menu must clear.
const codexMenuCatalog = `{"models":[
 {"slug":"gpt-6-astra","priority":2,"display_name":"gpt-6-astra","visibility":"list",
  "model_messages":{"instructions_template":"astra"},"upgrade":{"model":"x"}},
 {"slug":"gpt-6.1-sol","priority":1,"display_name":"gpt-6.1-sol","visibility":"list",
  "model_messages":{"instructions_template":"sol"},"availability_nux":{"message":"new"}},
 {"slug":"gpt-5.5","priority":13,"display_name":"GPT-5.5","visibility":"list",
  "model_messages":{"instructions_template":"old"}}]}`

// codexMenuLaunch is one run of the generated codex launcher in home: the stub codex it execs,
// the fake yolo that is this test binary, and what each recorded.
type codexMenuLaunch struct {
	out            string
	rc             int
	argv           []string
	catalogReads   int
	menuPath, home string
}

func runCodexMenuLauncher(t *testing.T, home string, extraEnv []string, args ...string) codexMenuLaunch {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(home, "fakebin")
	realBin := filepath.Join(home, ".local", "bin", "codex")
	for _, d := range []string{fakeBin, filepath.Dir(realBin)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argvLog, reads := filepath.Join(home, "codex.argv"), filepath.Join(home, "catalog.reads")
	catalogFile := filepath.Join(home, "catalog.json")
	if err := os.WriteFile(catalogFile, []byte(codexMenuCatalog), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/bash\n" +
		"if [ \"$*\" = 'debug models --bundled' ]; then echo read >> " + shellSingleQuote(reads) +
		"; cat " + shellSingleQuote(catalogFile) + "; exit 0; fi\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shellSingleQuote(argvLog) + "; done\necho LAUNCHED\n"
	// Written once per home: a second launch must meet the same program, since a changed one is
	// a new menu (modelmenu's cache key).
	if _, err := os.Stat(realBin); os.IsNotExist(err) {
		if err := os.WriteFile(realBin, []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	yolo := "#!/bin/sh\n" + modelMenuHelperEnv + "=1 exec " + shellSingleQuote(self) +
		" -test.run='^TestModelMenuHelper$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "yolo"), []byte(yolo), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(argvLog)
	body := nativeAgentLauncher("probe", shippedCodexInstall(t), filepath.Join(home, "stamps"),
		filepath.Join(home, "receipts.jsonl"), "", false, launcherServers{}, nil)
	script := filepath.Join(home, "codex-launcher")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, args...)
	cmd.Dir = home
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH")}, extraEnv...)
	out, err := cmd.CombinedOutput()
	l := codexMenuLaunch{out: string(out), home: home,
		menuPath: filepath.Join(home, filepath.FromSlash(shippedCodexInstall(t).ModelMenu.Into))}
	if ee, ok := err.(*exec.ExitError); ok {
		l.rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the launcher could not run: %v\n%s", err, out)
	}
	l.argv = logLines(t, argvLog)
	l.catalogReads = len(logLines(t, reads))
	return l
}

// ON -p codex THE LAUNCHER HANDS CODEX ITS MENU: codex is exec'd with the declared flag naming
// the menu, ahead of the user's own argv, and the menu holds the listed ids codex's catalog has,
// in the list's order, named from the list, with the upgrade and the notice cleared and the prompt
// text kept. The id the catalog lacks is left out, and the launch says so.
func TestCodexLauncherHandsCodexItsModelMenu(t *testing.T) {
	home := t.TempDir()
	renderCodexModelList(t, home, `{"codex":"codex"}`)
	l := runCodexMenuLauncher(t, home, nil, "exec", "hello")
	if l.rc != 0 || !strings.Contains(l.out, "LAUNCHED") {
		t.Fatalf("the launcher did not exec codex (rc=%d):\n%s", l.rc, l.out)
	}
	want := []string{"-c", "model_catalog_json=" + l.menuPath, "exec", "hello"}
	if !reflect.DeepEqual(l.argv, want) {
		t.Errorf("codex was exec'd with %q, want %q", l.argv, want)
	}
	raw, err := os.ReadFile(l.menuPath)
	if err != nil {
		t.Fatalf("no menu at %s: %v", l.menuPath, err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var slugs []any
	for _, m := range doc.Models {
		slugs = append(slugs, m["slug"])
		if _, has := m["upgrade"]; has {
			t.Errorf("%s keeps its upgrade, which would steer a user off the list", m["slug"])
		}
		if _, has := m["availability_nux"]; has {
			t.Errorf("%s keeps its availability notice", m["slug"])
		}
		if msgs, _ := m["model_messages"].(map[string]any); msgs["instructions_template"] == nil {
			t.Errorf("%s lost its prompt text", m["slug"])
		}
	}
	if !reflect.DeepEqual(slugs, []any{"gpt-6.1-sol", "gpt-6-astra"}) {
		t.Errorf("menu = %v, want gpt-6.1-sol then gpt-6-astra", slugs)
	}
	if len(doc.Models) == 2 && (doc.Models[0]["display_name"] != "GPT-6.1 Sol" || doc.Models[0]["priority"] != float64(0)) {
		t.Errorf("first entry = %v, want the list's name and priority 0", doc.Models[0])
	}
	if !strings.Contains(l.out, "has no gpt-6-luna") {
		t.Errorf("the launch did not say gpt-6-luna is missing from codex's catalog:\n%s", l.out)
	}

	// The next launch, with nothing changed, reuses the menu: codex's catalog is not read again.
	again := runCodexMenuLauncher(t, home, nil, "exec", "again")
	if again.catalogReads != 1 {
		t.Errorf("codex's catalog was read %d times over two launches with nothing changed, want once",
			again.catalogReads)
	}
	if len(again.argv) < 2 || again.argv[0] != "-c" {
		t.Errorf("the second launch exec'd codex with %q, want the menu's flag again", again.argv)
	}
	// The reused menu still leaves gpt-6-luna out, and that launch says so too.
	if !strings.Contains(again.out, "has no gpt-6-luna") {
		t.Errorf("the second launch did not say gpt-6-luna is missing from the menu it reused:\n%s", again.out)
	}
}

// WITH NO MENU TO NAME, CODEX RUNS AS TYPED: on a provider whose ids codex's catalog does not
// carry the list is empty, so neither the catalog is read nor a flag added; and
// YOLO_NO_LAUNCH_FLAGS=1 skips the menu with the launch flags.
func TestCodexLauncherAddsNoMenuWhereThereIsNone(t *testing.T) {
	home := t.TempDir()
	renderCodexModelList(t, home, `{"codex":"openrouter"}`)
	l := runCodexMenuLauncher(t, home, nil, "exec", "hi")
	if !reflect.DeepEqual(l.argv, []string{"exec", "hi"}) || l.catalogReads != 0 {
		t.Errorf("on -p openrouter codex got %q after %d catalog reads, want its own argv and none",
			l.argv, l.catalogReads)
	}
	home2 := t.TempDir()
	renderCodexModelList(t, home2, `{"codex":"codex"}`)
	off := runCodexMenuLauncher(t, home2, []string{NoLaunchFlagsEnv + "=1"}, "exec", "hi")
	if !reflect.DeepEqual(off.argv, []string{"exec", "hi"}) {
		t.Errorf("with %s=1 codex got %q, want its own argv alone", NoLaunchFlagsEnv, off.argv)
	}
}

// THE LIST AND THE DECLARATION AGREE ON ONE PATH: packs/codex's model_menu reads its list where the
// codex/model-list surface writes it, so moving one without the other is caught here rather than
// by a codex that silently keeps its own menu.
func TestCodexModelMenuReadsTheListItsSurfaceWrites(t *testing.T) {
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatal(err)
	}
	surfaces, _ := codex.SurfacesFor(false)
	menu := shippedCodexInstall(t).ModelMenu
	if menu == nil {
		t.Fatal("the codex program declares no model_menu")
	}
	for _, s := range surfaces {
		if s.Agent == "codex" && s.Name == "model-list" {
			if s.Path != "~/"+menu.List {
				t.Errorf("codex/model-list is written to %s, but model_menu reads ~/%s", s.Path, menu.List)
			}
			return
		}
	}
	t.Fatal("packs/codex declares no codex/model-list surface")
}
