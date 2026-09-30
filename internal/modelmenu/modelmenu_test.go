package modelmenu

// modelmenu_test.go pins the projection and the run the generated launcher calls
// (docs/design/model-lists-and-pickers.md MM-D9, MM-D22): the catalog's entries for the listed
// ids, in the list's order, with the declared keys renumbered, renamed, cleared and set and every
// other field kept; the flag printed only when a menu was written; a menu reused until the
// program, the list or the declaration changes; and no flag, only a warning, whenever there is
// nothing to name. The program is a shell stub, so no agent runs.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// spec is the shape packs/codex declares, spelled here so these tests read the package alone.
var spec = packdecl.ModelMenu{
	Catalog: []string{"debug", "models", "--bundled"},
	List:    ".app/list.json",
	Into:    ".app/menu.json",
	Flag:    []string{"-c", "model_catalog_json={into}"},
	Entries: "models",
	ID:      "slug",
	Order:   "priority",
	Name:    "display_name",
	Clear:   []string{"upgrade", "availability_nux"},
	Set:     map[string]string{"visibility": "list"},
}

// catalog is a catalog of codex's shape: three entries, each with prompt text the menu must
// keep, one hidden, one with an upgrade that would steer a user off the list.
const catalog = `{"models":[
  {"slug":"gpt-6-astra","priority":2,"display_name":"GPT-6 Astra (catalog)","visibility":"list",
   "model_messages":{"instructions_template":"astra prompt"},"upgrade":null,"availability_nux":null},
  {"slug":"gpt-6.1-sol","priority":1,"display_name":"GPT-6.1 Sol (catalog)","visibility":"list",
   "model_messages":{"instructions_template":"sol prompt"},"availability_nux":{"message":"new"}},
  {"slug":"gpt-5.5","priority":13,"display_name":"GPT-5.5","visibility":"hide",
   "model_messages":{"instructions_template":"old prompt"},"upgrade":{"model":"gpt-6-sol"}}
]}`

func decodeMenu(t *testing.T, menu []byte) []map[string]any {
	t.Helper()
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(menu, &doc); err != nil {
		t.Fatalf("the menu is not JSON: %v\n%s", err, menu)
	}
	return doc.Models
}

func TestProjectKeepsTheListedEntriesInTheListsOrder(t *testing.T) {
	list := []ListEntry{{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol"}, {ID: "gpt-5.5"}, {ID: "gpt-6-luna"},
		{ID: "gpt-6.1-sol"}}
	res, err := Project([]byte(catalog), list, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Kept, []string{"gpt-6.1-sol", "gpt-5.5"}) {
		t.Errorf("kept = %v, want the listed ids the catalog has, in list order, once", res.Kept)
	}
	if !reflect.DeepEqual(res.Missing, []string{"gpt-6-luna"}) {
		t.Errorf("missing = %v, want the listed id the catalog lacks", res.Missing)
	}
	models := decodeMenu(t, res.Menu)
	if len(models) != 2 {
		t.Fatalf("menu = %v, want two entries", models)
	}
	sol, old := models[0], models[1]
	if sol["slug"] != "gpt-6.1-sol" || sol["priority"] != float64(0) || sol["display_name"] != "GPT-6.1 Sol" {
		t.Errorf("first entry = %v, want gpt-6.1-sol renumbered 0 and named from the list", sol)
	}
	if old["slug"] != "gpt-5.5" || old["priority"] != float64(1) || old["display_name"] != "GPT-5.5" {
		t.Errorf("second entry = %v, want gpt-5.5 renumbered 1, keeping the catalog's name when the "+
			"list gives none", old)
	}
	for _, m := range models {
		for _, k := range []string{"upgrade", "availability_nux"} {
			if _, has := m[k]; has {
				t.Errorf("%s keeps %q, which the declaration clears", m["slug"], k)
			}
		}
		if m["visibility"] != "list" {
			t.Errorf("%s visibility = %v, want the declared \"list\"", m["slug"], m["visibility"])
		}
		msgs, _ := m["model_messages"].(map[string]any)
		if msgs["instructions_template"] == nil {
			t.Errorf("%s lost its prompt text: the program would run with empty instructions", m["slug"])
		}
	}
}

func TestProjectRefusesACatalogOfAnotherShape(t *testing.T) {
	for name, doc := range map[string]string{
		"not JSON":          `models: []`,
		"no entries key":    `{"data":[]}`,
		"entries not array": `{"models":{"slug":"x"}}`,
	} {
		if _, err := Project([]byte(doc), []ListEntry{{ID: "x"}}, spec); err == nil {
			t.Errorf("%s: Project accepted %s", name, doc)
		}
	}
	res, err := Project([]byte(catalog), []ListEntry{{ID: "nothing-like-it"}}, spec)
	if err != nil || res.Menu != nil {
		t.Errorf("a list the catalog shares no id with gave menu %s, err %v; want none and no error", res.Menu, err)
	}
}

// fixture is one run's home: yolo's list, a stub program that prints the catalog for the
// declared argv and records every run, and the declaration as the launcher bakes it.
type fixture struct {
	home, program, runs string
	specJSON            string
}

func newFixture(t *testing.T, list, catalogOut string, exit int) *fixture {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	f := &fixture{home: t.TempDir()}
	f.runs = filepath.Join(f.home, "runs")
	f.program = filepath.Join(f.home, "bin", "app")
	if err := os.MkdirAll(filepath.Dir(f.program), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "catalog.json"), []byte(catalogOut), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/bash\necho \"$*\" >> '" + f.runs + "'\n" +
		"[ \"$*\" = 'debug models --bundled' ] || exit 9\n" +
		"cat '" + filepath.Join(f.home, "catalog.json") + "'\nexit " + string(rune('0'+exit)) + "\n"
	if err := os.WriteFile(f.program, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.writeList(t, list)
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	f.specJSON = string(b)
	return f
}

func (f *fixture) writeList(t *testing.T, list string) {
	t.Helper()
	p := filepath.Join(f.home, ".app", "list.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) run(t *testing.T) (words []string, stderr string, rc int) {
	t.Helper()
	var out, errw bytes.Buffer
	rc = Run([]string{"--bin=app", "--spec=" + f.specJSON, "--", f.program}, f.home, &out, &errw)
	if s := out.String(); s != "" {
		words = strings.Split(strings.TrimSuffix(s, "\x00"), "\x00")
	}
	return words, errw.String(), rc
}

func (f *fixture) runCount(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(f.runs)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func (f *fixture) menuPath() string { return filepath.Join(f.home, ".app", "menu.json") }

const twoListed = `{"models":[{"id":"gpt-6.1-sol","name":"GPT-6.1 Sol"},{"id":"gpt-6-astra","name":"GPT-6 Astra"}]}`

func TestRunWritesTheMenuAndPrintsTheFlagNamingIt(t *testing.T) {
	f := newFixture(t, twoListed, catalog, 0)
	words, stderr, rc := f.run(t)
	if rc != 0 {
		t.Fatalf("rc = %d, stderr %s", rc, stderr)
	}
	want := []string{"-c", "model_catalog_json=" + f.menuPath()}
	if !reflect.DeepEqual(words, want) {
		t.Errorf("printed flag = %q, want %q", words, want)
	}
	data, err := os.ReadFile(f.menuPath())
	if err != nil {
		t.Fatal(err)
	}
	var slugs []any
	for _, m := range decodeMenu(t, data) {
		slugs = append(slugs, m["slug"])
	}
	if !reflect.DeepEqual(slugs, []any{"gpt-6.1-sol", "gpt-6-astra"}) {
		t.Errorf("menu slugs = %v, want the list's two, in its order", slugs)
	}
	if fi, err := os.Stat(f.menuPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("menu mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing when every listed id is in the catalog", stderr)
	}
}

func TestRunReusesTheMenuUntilTheListOrTheProgramChanges(t *testing.T) {
	f := newFixture(t, twoListed, catalog, 0)
	for i := 0; i < 2; i++ {
		if words, stderr, _ := f.run(t); len(words) != 2 {
			t.Fatalf("run %d printed %q (%s), want the flag", i, words, stderr)
		}
	}
	if n := f.runCount(t); n != 1 {
		t.Errorf("the program ran %d times for two launches with nothing changed, want once", n)
	}
	f.writeList(t, `{"models":[{"id":"gpt-6-astra"}]}`)
	if words, _, _ := f.run(t); len(words) != 2 {
		t.Fatalf("after a list change the flag is %q", words)
	}
	if n := f.runCount(t); n != 2 {
		t.Errorf("the program ran %d times, want a second run after the list changed", n)
	}
	data, _ := os.ReadFile(f.menuPath())
	if models := decodeMenu(t, data); len(models) != 1 || models[0]["slug"] != "gpt-6-astra" {
		t.Errorf("menu after the list changed = %v, want gpt-6-astra alone", models)
	}
	// An update replaces the program: a new modification time is a new program.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(f.program, later, later); err != nil {
		t.Fatal(err)
	}
	if words, _, _ := f.run(t); len(words) != 2 {
		t.Fatalf("after a program change the flag is %q", words)
	}
	if n := f.runCount(t); n != 3 {
		t.Errorf("the program ran %d times, want a third run after it changed", n)
	}
}

func TestRunNamesNoMenuWithoutAList(t *testing.T) {
	f := newFixture(t, twoListed, catalog, 0)
	if words, _, _ := f.run(t); len(words) != 2 {
		t.Fatalf("setup: no menu written (%q)", words)
	}
	f.writeList(t, `{}`)
	words, stderr, rc := f.run(t)
	if rc != 0 || len(words) != 0 || stderr != "" {
		t.Errorf("an empty list printed %q, stderr %q, rc %d; want nothing at all", words, stderr, rc)
	}
	if _, err := os.Stat(f.menuPath()); !os.IsNotExist(err) {
		t.Errorf("the earlier menu survived a launch with no list (%v); a later launch could reuse it", err)
	}
	if n := f.runCount(t); n != 1 {
		t.Errorf("the program ran %d times, want no run for a launch with no list", n)
	}
}

func TestRunSaysWhenTheCatalogCannotBeRead(t *testing.T) {
	f := newFixture(t, twoListed, "not json", 3)
	words, stderr, rc := f.run(t)
	if rc != 0 || len(words) != 0 {
		t.Errorf("a failing catalog printed %q, rc %d; want no flag and exit 0", words, rc)
	}
	if !strings.Contains(stderr, "could not read app's own model catalog") {
		t.Errorf("stderr = %q, want it to say the catalog could not be read", stderr)
	}
	f2 := newFixture(t, twoListed, `{"other":[]}`, 0)
	if words, stderr, _ := f2.run(t); len(words) != 0 || !strings.Contains(stderr, "not the shape") {
		t.Errorf("a catalog of another shape printed %q, stderr %q; want no flag and a warning", words, stderr)
	}
}

func TestRunLeavesOutAListedIDTheCatalogLacks(t *testing.T) {
	f := newFixture(t, `{"models":[{"id":"gpt-6.1-sol"},{"id":"gpt-6-luna"}]}`, catalog, 0)
	words, stderr, _ := f.run(t)
	if len(words) != 2 {
		t.Errorf("flag = %q, want it for the one id the catalog has", words)
	}
	if !strings.Contains(stderr, "has no gpt-6-luna") || !strings.Contains(stderr, "updating app") {
		t.Errorf("stderr = %q, want the missing id named and the update named as the fix", stderr)
	}
	g := newFixture(t, `{"models":[{"id":"gpt-6-luna"}]}`, catalog, 0)
	if words, stderr, _ := g.run(t); len(words) != 0 || !strings.Contains(stderr, "shows its own model menu") {
		t.Errorf("a list with no id in the catalog printed %q, stderr %q; want no flag", words, stderr)
	}
}

func TestRunRefusesAMisuse(t *testing.T) {
	var out, errw bytes.Buffer
	for _, args := range [][]string{
		nil,
		{"--bin=app", "--spec={}", "--", "/bin/true"},
		{"--bin=app", "--spec=" + `{"list":"a","into":"b","entries":"m","id":"i"}`},
		{"--weird"},
	} {
		if rc := Run(args, t.TempDir(), &out, &errw); rc != 2 {
			t.Errorf("Run(%q) = %d, want 2", args, rc)
		}
	}
	if out.Len() != 0 {
		t.Errorf("a misuse printed %q on stdout, which the launcher would add to the argv", out.String())
	}
}
