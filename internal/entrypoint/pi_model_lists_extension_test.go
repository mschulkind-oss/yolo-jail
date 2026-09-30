package entrypoint

// pi_model_lists_extension_test.go pins the pi half of an `only`
// (docs/design/model-lists-and-pickers.md MM-D6) from the shipped declaration to what the
// extension hands pi: the pi pack delivers yolo-model-lists.js into pi's extension discovery
// directory, and the extension, reading the file a real boot render wrote at the path the
// manifest declares, registers exactly the narrowed list with `models` alone — the form that
// keeps pi's own address, wire and credential for the provider.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestShippedPiPackDeliversTheModelListsExtension(t *testing.T) {
	p := shippedPiPack(t)
	var found bool
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "extensions/yolo-model-lists.js" &&
			c.Into == ".pi/agent/extensions/yolo-model-lists.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("the pi pack does not deliver yolo-model-lists.js to pi's extension directory")
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped pi extensions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "yolo-model-lists.js")); err != nil {
		t.Fatalf("pi cannot discover the rendered extension: %v", err)
	}
}

// piModelListsRel is the pi/model-lists surface's path relative to HOME, read off the shipped
// manifest, so the extension's path is pinned to the manifest's.
func piModelListsRel(t *testing.T) string {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	surfaces, _ := pi.SurfacesFor(false)
	for _, s := range surfaces {
		if s.Agent == "pi" && s.Name == "model-lists" {
			rel, ok := strings.CutPrefix(s.Path, "~/")
			if !ok {
				t.Fatalf("pi/model-lists path %q is not home-relative", s.Path)
			}
			return rel
		}
	}
	t.Fatal("packs/pi declares no pi/model-lists surface")
	return ""
}

func TestPiModelListsExtensionRegistersTheRenderedListAlone(t *testing.T) {
	// The file a boot render writes for zai narrowed by a company pack's `only`.
	providers, profiles := zaiNarrowed(t, "pi", nil)
	r := newPioencodeRender(t, providers)
	r.wireProfiles(profiles)
	r.render(t, `{"pi":"zai"}`)
	raw, err := os.ReadFile(filepath.Join(r.e.Home, filepath.FromSlash(piModelListsRel(t))))
	if err != nil {
		t.Fatal(err)
	}

	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-model-lists.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, filepath.FromSlash(piModelListsRel(t)))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// A stand-in for pi's own catalog: glm-5.3 is known to it, glm-5.3-flash is not.
	pkg := filepath.Join(dir, "node_modules", "@earendil-works", "pi-ai")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(
		`{"name":"@earendil-works/pi-ai","type":"module","exports":{"./providers/all":"./all.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "all.js"), []byte(`
export function getBuiltinModel(provider, id) {
	if (provider !== "zai" || id !== "glm-5.3") return undefined;
	return { id, name: "Catalog GLM-5.3", api: "openai-completions", provider, baseUrl: "https://catalog.example",
		reasoning: true, input: ["text"], maxTokens: 131072, contextWindow: 200000,
		cost: { input: 1, output: 3, cacheRead: 0, cacheWrite: 0 } };
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.mjs"), []byte(`
import extension from "./extension.mjs";
const registrations = [];
await extension({ registerProvider(name, config) { registrations.push({ name, config }); } });
console.log(JSON.stringify(registrations));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the shipped extension: %v\n%s", err, out)
	}
	var got []struct {
		Name   string                     `json:"name"`
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	if len(got) != 1 || got[0].Name != "zai" {
		t.Fatalf("registrations = %s, want one, for zai", out)
	}
	for key := range got[0].Config {
		if key != "models" {
			t.Errorf("the registration carries %q: only `models` keeps pi's own address, wire and "+
				"credential for the provider", key)
		}
	}
	var models []map[string]any
	if err := json.Unmarshal(got[0].Config["models"], &models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0]["id"] != "glm-5.3" || models[1]["id"] != "glm-5.3-flash" {
		t.Fatalf("registered models = %v, want glm-5.3 then glm-5.3-flash", models)
	}
	known, unknown := models[0], models[1]
	if known["name"] != "Catalog GLM-5.3" || known["maxTokens"] != float64(131072) {
		t.Errorf("glm-5.3 = %v, want pi's catalog facts where yolo declares none", known)
	}
	if known["contextWindow"] != float64(1000000) {
		t.Errorf("glm-5.3 contextWindow = %v, want zai's declared 1,000,000 over the catalog's", known["contextWindow"])
	}
	for _, k := range []string{"api", "baseUrl", "provider"} {
		if _, has := known[k]; has {
			t.Errorf("glm-5.3 carries the catalog's %s: the registration must never repoint the provider", k)
		}
	}
	if unknown["name"] != "glm-5.3-flash" || unknown["maxTokens"] != float64(16384) {
		t.Errorf("glm-5.3-flash = %v, want pi's models.json defaults for an id its catalog lacks", unknown)
	}
	if in, _ := unknown["input"].([]any); len(in) != 2 {
		t.Errorf("glm-5.3-flash input = %v, want zai's declared text and image", unknown["input"])
	}
}
