package pluginpack

// pluginpack_test.go pins RECOGNITION: whether yolo notices a plugin, and what it reads out of
// it. The stakes are asymmetric and worth naming — a plugin yolo fails to recognize is a
// plugin whose hooks were never surfaced for approval, so a false negative here is a hole in
// the trust gate, not a missing convenience.

import (
	"os"
	"path/filepath"
	"testing"
)

// writePlugin creates a plugin dir with the given manifest body and returns its path.
func writePlugin(t *testing.T, root, name, manifest string) string {
	t.Helper()
	return writePluginAt(t, root, name, PreferredManifestDir, manifest)
}

// writePluginAt is writePlugin with control over which of the searched manifest dirs is used.
func writePluginAt(t *testing.T, root, name, manifestDir, manifest string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	md := filepath.Join(dir, manifestDir)
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeSkill creates a skill dir (the shape every one of these tools reads).
func writeSkill(t *testing.T, parent, name string) {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: d\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The headline: a plugin-shaped tree is recognized, and its name comes from the MANIFEST (what
// the tools namespace by) rather than from the directory.
func TestRecognizesPluginTree(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "on-disk-name",
		`{"name":"declared-name","description":"a test plugin","skills":["./"]}`)

	p, ok := Load(dir)
	if !ok {
		t.Fatal("a dir carrying .claude-plugin/plugin.json must be recognized as a plugin")
	}
	if got := p.Name(); got != "declared-name" {
		t.Errorf("Name() = %q, want the manifest's name — that is what the tools qualify "+
			"its skills with", got)
	}
	if p.Manifest.Description != "a test plugin" {
		t.Errorf("description = %q", p.Manifest.Description)
	}
}

// A dir with no manifest is not a plugin. Without this, every skills dir would be delivered as
// a plugin and yolo would rewrite trees it does not own.
func TestPlainDirIsNotAPlugin(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "just-a-skill")
	if _, ok := Load(filepath.Join(root, "just-a-skill")); ok {
		t.Error("a skill dir with no plugin manifest must not be recognized as a plugin")
	}
}

// Every manifest location the tools search is recognized, not only Claude's. Recognition drives
// the TRUST report, so a manifest yolo overlooks is a manifest whose hooks nobody approved.
func TestRecognizesEveryManifestLocation(t *testing.T) {
	for _, md := range []string{".claude-plugin", ".plugin", ".", ".github/plugin"} {
		t.Run(md, func(t *testing.T) {
			root := t.TempDir()
			dir := writePluginAt(t, root, "p", md, `{"name":"p","hooks":{"x":[]}}`)
			p, ok := Load(dir)
			if !ok {
				t.Fatalf("manifest at %s/plugin.json must be recognized — a copilot-shaped "+
					"tree declaring hooks would otherwise bypass the trust report", md)
			}
			// The marker must be written back into the SAME file, or a plugin gets two
			// manifests and the tools may read the unmarked one.
			wantRel := "plugin.json"
			if md != "." {
				wantRel = md + "/plugin.json"
			}
			if got := p.ManifestRel(); got != wantRel {
				t.Errorf("ManifestRel() = %q, want %q", got, wantRel)
			}
		})
	}
}

// Unknown manifest fields travel through untouched. This is the whole "do not re-implement the
// plugin schema" contract: the plugin's next release must not become yolo's parse error.
func TestUnknownManifestFieldsAreTolerated(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "p",
		`{"name":"p","someFutureFeature":{"deeply":["nested"]},"version":"2.0"}`)
	p, ok := Load(dir)
	if !ok {
		t.Fatal("an unknown field must not stop recognition — it is someone else's feature, " +
			"not an authoring mistake yolo should refuse")
	}
	if p.Manifest.Version != "2.0" {
		t.Errorf("version = %q, want the fields yolo DOES model to still decode", p.Manifest.Version)
	}
}

// The trust question: hooks and servers are code, everything else is content. That split is the
// same one the origin gate already draws between an npm package and a curl-piped installer.
func TestCodeRunningComponentsAreIdentified(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "p", `{"name":"p",
		"hooks":{"PreToolUse":[]},"mcpServers":{"db":{"command":"x"}},
		"lspServers":{"gopls":{}},"commands":"./cmds","agents":["./agents"],
		"outputStyles":["./styles"]}`)
	p, _ := Load(dir)

	got := map[string]bool{}
	for _, c := range p.Components() {
		got[c.Name] = c.RunsCode
	}
	want := map[string]bool{
		"hooks": true, "mcpServers": true, "lspServers": true,
		"commands": false, "agents": false, "outputStyles": false,
	}
	for name, runs := range want {
		v, ok := got[name]
		if !ok {
			t.Errorf("component %q declared by the manifest was not reported — it would be "+
				"silently dropped on a flat destination", name)
			continue
		}
		if v != runs {
			t.Errorf("component %q RunsCode = %v, want %v", name, v, runs)
		}
	}
	if !p.RunsCode() {
		t.Error("a plugin declaring hooks and MCP servers must report RunsCode")
	}
	// THE APPROVAL STRINGS ARE GONE with the prompt and the lockfile that consumed them
	// (docs/design/trust-paths.md OQ-TP9, 2026-09-04). Plugin.HostAccessClaims rendered one
	// per code-running component, naming the plugin; what a user sees now is the pack
	// footprint's ⚠ RUNS CODE line, built from Components() by packload.FootprintOf and
	// pinned there (packload/plugins_test.go). Components() and RunsCode(), asserted above,
	// are the inputs that survived.
}

// A skills-only plugin runs no code, so nothing about it is review-worthy. If this regressed,
// every plugin-as-pack would carry a ⚠ RUNS CODE line and the marker would stop meaning
// anything — the same argument that used to be about the prompt, now about the disclosure.
func TestSkillsOnlyPluginRunsNoCode(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "p", `{"name":"p","skills":["./"]}`)
	p, _ := Load(dir)
	if p.RunsCode() {
		t.Error("a skills-only plugin must not report RunsCode")
	}
	for _, c := range p.Components() {
		if c.RunsCode {
			t.Errorf("component %q of a skills-only plugin reports RunsCode", c.Name)
		}
	}
}

// The `skills` field's three shapes resolve the way the TOOLS resolve them. A divergence means
// yolo delivers a different skill set than the tool would load from the same tree.
func TestSkillRootResolution(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     []string // plugin-relative
	}{
		{"absent defaults to skills/", `{"name":"p"}`, []string{"skills"}},
		// The scaffolder's own shape. It yields BOTH the root (a skill directly there) and the
		// nested skills/ dir — reading it as exclusive would silently drop every nested skill.
		{"self-reference also takes skills/", `{"name":"p","skills":["./"]}`,
			[]string{".", "skills"}},
		{"bare string", `{"name":"p","skills":"custom"}`, []string{"custom"}},
		{"list is exclusive", `{"name":"p","skills":["a","b"]}`, []string{"a", "b"}},
		{"object adds the default", `{"name":"p","skills":{"paths":["a"]}}`,
			[]string{"skills", "a"}},
		{"exclusive object omits it",
			`{"name":"p","skills":{"paths":["a"],"exclusive":true}}`, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := writePlugin(t, root, "p", tc.manifest)
			p, _ := Load(dir)
			roots, problems := p.SkillRoots()
			if len(problems) > 0 {
				t.Fatalf("unexpected problems: %v", problems)
			}
			var got []string
			for _, r := range roots {
				rel, _ := filepath.Rel(dir, r)
				got = append(got, rel)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("roots = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("roots = %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}

// A `skills` path escaping the plugin dir is REFUSED, not clamped. `skills: ["../../.ssh"]` in
// someone else's repo must not become a directory yolo copies into a real home.
func TestEscapingSkillPathIsRefused(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "p", `{"name":"p","skills":["../../elsewhere"]}`)
	p, _ := Load(dir)
	roots, problems := p.SkillRoots()
	if len(problems) == 0 {
		t.Fatalf("an escaping skills path must be refused by name, got roots %v", roots)
	}
	for _, r := range roots {
		if !Contains(dir, r) {
			t.Errorf("root %q resolved outside the plugin dir", r)
		}
	}
}

// SkillDirs finds the skills a flat (tier-B) delivery can carry, and finds ONLY skills: the
// manifest dir and a non-skill component dir must not come along as if they were skills.
func TestSkillDirsFindsOnlySkills(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "p", `{"name":"p","skills":["./"],"hooks":{"x":[]}}`)
	writeSkill(t, filepath.Join(dir, "skills"), "alpha")
	writeSkill(t, filepath.Join(dir, "skills"), "beta")
	// Non-skill dirs that must be ignored: no SKILL.md.
	for _, d := range []string{"hooks", "agents"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p, _ := Load(dir)
	got := p.SkillDirs()
	if len(got) != 2 || got["alpha"] == "" || got["beta"] == "" {
		t.Errorf("SkillDirs() = %v, want exactly the two skills — a non-skill dir delivered "+
			"as a skill is content the agent cannot read", keys(got))
	}
	for _, bad := range []string{"hooks", "agents", PreferredManifestDir} {
		if got[bad] != "" {
			t.Errorf("%q was collected as a skill", bad)
		}
	}
}

// Discover finds both supported layouts and NOT an arbitrary-depth one. Depth would find a
// plugin vendored in a skill's test fixtures and deliver it — with its hooks.
func TestDiscoverFindsWrappedAndRootPlugins(t *testing.T) {
	packRoot := t.TempDir()
	// Layout 1: under skills/.
	writePlugin(t, filepath.Join(packRoot, SkillsSubdir), "wrapped", `{"name":"wrapped"}`)
	// A plugin buried deeper must NOT be found.
	writePlugin(t, filepath.Join(packRoot, SkillsSubdir, "a-skill", "fixtures"), "buried",
		`{"name":"buried","hooks":{"x":[]}}`)

	got := names(Discover(packRoot))
	if len(got) != 1 || got[0] != "wrapped" {
		t.Errorf("Discover() = %v, want just [wrapped] — a nested plugin must not be picked "+
			"up, or a test fixture arrives with hooks", got)
	}

	// Layout 2: the pack root itself.
	md := filepath.Join(packRoot, PreferredManifestDir)
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"),
		[]byte(`{"name":"rootplugin"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = names(Discover(packRoot))
	if len(got) != 2 || got[0] != "rootplugin" || got[1] != "wrapped" {
		t.Errorf("Discover() = %v, want [rootplugin wrapped] in a deterministic order", got)
	}
}

// A malformed manifest reads as NOT a plugin, so it degrades to ordinary content rather than
// failing a whole pack load over a file yolo only consults to be generous.
func TestMalformedManifestIsNotAPlugin(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "p", `{"name": broken`)
	if _, ok := Load(dir); ok {
		t.Error("an unparseable manifest must not be treated as a plugin")
	}
}

func names(ps []*Plugin) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
