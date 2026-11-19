package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests use user TOML, not a wrapper pack or workspace skill_imports.
// The only whole pack is a synthetic destination reader with no program.
func scopedPackHome(t *testing.T) string {
	t.Helper()
	packHome(t, `{"agent_updates": false}`)
	root := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.Remove(filepath.Join(root, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	scopedPackFile(t, root, "config.toml", "# synthetic modern main config\n")
	scopedPackFile(t, root, "packs/reader/pack.toml", "[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n")
	return root
}

func scopedPackFile(t *testing.T, root, name, body string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scopedPackRun(t *testing.T, script string) result {
	t.Helper()
	workspace := writeProject(t, `{}`)
	r := runYolo(t, workspace, script, withEnv("YOLO_NO_PROGRAM_READINESS=1", "YOLO_NO_AUTO_CAPTURE=1",
		"YOLO_NO_AUTO_IMAGE_REAP=1", "YOLO_NO_UPDATE_CHECK=1", "YOLO_AGENT_UPDATES=false"))
	if r.rc != 0 {
		t.Fatalf("direct scoped launch: rc=%d\nstdout=%s\nstderr=%s", r.rc, r.stdout, r.stderr)
	}
	return r
}

func TestDirectUserScopedPackImportsAcrossWorkspaces(t *testing.T) {
	requireJail(t)
	root := scopedPackHome(t)
	for _, name := range []string{"skill-a", "skill-b", "hidden"} {
		scopedPackFile(t, root, "packs/corpus/skills/core-tools/"+name+"/SKILL.md", name+"\n")
	}
	scopedPackFile(t, root, "packs/corpus/skills/core-tools/shared/notes.md", "support, not a candidate\n")
	scopedPackFile(t, root, "packs/corpus/.claude-plugin/plugin.json", `{"name":"scope-poison"}`)
	scopedPackFile(t, root, "packs/corpus/derive.lua", "error('scoped root code must never execute')\n")
	// Raw mode must not parse even a malformed original manifest.
	scopedPackFile(t, root, "packs/corpus/pack.toml", "broken TOML [\n")
	body := `agent_updates = false
packs = ["local/reader", { pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a", "skill-b"] } }]
`
	scopedPackFile(t, root, "config.d/scoped.toml", body)
	script := `set -eu
cat "$HOME/.fixture/skills/corpus-skill-a/SKILL.md"
cat "$HOME/.fixture/skills/corpus-skill-b/SKILL.md"
test ! -e "$HOME/.fixture/skills/corpus-hidden"
test ! -e "$HOME/.fixture/skills/corpus-shared"
test ! -e "$HOME/.yolo/bin/launch/scope-poison"
test ! -e "$HOME/.scope-poison"
printf 'SCOPED-ONLY\n'
`
	for range 2 {
		r := scopedPackRun(t, script)
		if !strings.Contains(r.stdout, "SCOPED-ONLY") {
			t.Fatal(r.combined())
		}
	}
	// Valid poison declarations must be just as inert as malformed raw metadata.
	if err := os.Remove(filepath.Join(root, "packs/corpus/pack.toml")); err != nil {
		t.Fatal(err)
	}
	scopedPackFile(t, root, "packs/corpus/pack.json", scopedPoisonManifest)
	r := scopedPackRun(t, script)
	if !strings.Contains(r.stdout, "SCOPED-ONLY") {
		t.Fatal(r.combined())
	}
}

const scopedPoisonManifest = `{"needs":[{"pack":"never-select-scope-poison"}],"contributes":[{"kind":"program","bin":"scope-poison","via":"npm","package":"synthetic-scope-poison"},{"kind":"hook","hook":"shared_directory","from":".scope-poison","at":".scope-poison-shared"},{"kind":"state","at":".scope-poison-shared","scope":"machine","because":"inert synthetic fixture"},{"kind":"briefing","from":"briefing/poison.md"}]}`

func TestDirectUserScopedTypedMixedRowsInJail(t *testing.T) {
	requireJail(t)
	root := scopedPackHome(t)
	scopedPackFile(t, root, "packs/corpus/pack.json", `{"needs":[{"pack":"never-select-scope-poison"}],"contributes":[{"kind":"skills","from":"extra","agents":["fixture"]},{"kind":"program","bin":"scope-poison","via":"npm","package":"synthetic-scope-poison"},{"kind":"hook","hook":"shared_directory","from":".scope-poison","at":".scope-poison-shared"},{"kind":"state","at":".scope-poison-shared","scope":"machine","because":"inert synthetic fixture"},{"kind":"briefing","from":"briefing/poison.md"}]}`)
	scopedPackFile(t, root, "packs/corpus/extra/marked/SKILL.md", "TYPED-MARKER\n")
	scopedPackFile(t, root, "packs/corpus/extra/folder/notes.md", "TYPED-FOLDER\n")
	scopedPackFile(t, root, "packs/corpus/extra/folder/hooks.json", `{"inert":"support bytes"}`)
	scopedPackFile(t, root, "packs/corpus/skills/hidden/SKILL.md", "hidden\n")
	scopedPackFile(t, root, "packs/corpus/.claude-plugin/plugin.json", `{"name":"scope-poison"}`)
	scopedPackFile(t, root, "packs/corpus/derive.lua", "error('scoped code must never execute')\n")
	scopedPackFile(t, root, "config.d/scoped.toml", `agent_updates = false
packs = ["local/reader", { pack = "local/corpus", skills = { only = ["marked", "folder"] } }]
`)
	r := scopedPackRun(t, `set -eu
cat "$HOME/.fixture/skills/corpus-marked/SKILL.md"
cat "$HOME/.fixture/skills/corpus-folder/notes.md"
cat "$HOME/.fixture/skills/corpus-folder/hooks.json"
test ! -e "$HOME/.fixture/skills/corpus-folder/SKILL.md"
test ! -e "$HOME/.fixture/skills/corpus-hidden"
test ! -e "$HOME/.yolo/bin/launch/scope-poison"
test ! -e "$HOME/.scope-poison"
printf 'TYPED-ONLY\n'
`)
	for _, want := range []string{"TYPED-MARKER", "TYPED-FOLDER", "TYPED-ONLY"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("missing %q: %s", want, r.combined())
		}
	}
}

func TestDirectUserScopedManifestSourcedRowsInJail(t *testing.T) {
	requireJail(t)
	root := scopedPackHome(t)
	repo := resolvedTempDir(t)
	scopedPackFile(t, repo, "skills/upstream/SKILL.md", "SOURCED-MARKER\n")
	scopedPackFile(t, repo, "skills/upstream/bin/probe.sh", "cat \"$(dirname \"$0\")/../../../shared/value\"\n")
	scopedPackFile(t, repo, "shared/value", "SIBLING-SUPPORT\n")
	scopedPackFile(t, repo, "shared/SKILL.md", "not registered\n")
	scopedPackFile(t, repo, "LICENSE", "synthetic license\n")
	for _, args := range [][]string{{"init", "-q", "--initial-branch=main"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "synthetic upstream"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v: %s", err, output)
		}
	}
	source := "git+file://" + filepath.ToSlash(repo) + "//skills/upstream?ref=main"
	scopedPackFile(t, root, "packs/corpus/pack.toml", fmt.Sprintf("[[skills]]\nsource = %q\nas = \"remote\"\nagents = [\"fixture\"]\n", source))
	scopedPackFile(t, root, "packs/corpus/skills/local/SKILL.md", "LOCAL-MARKER\n")
	scopedPackFile(t, root, "packs/corpus/skills/hidden/SKILL.md", "hidden\n")
	scopedPackFile(t, root, "config.d/scoped.toml", `agent_updates = false
packs = ["local/reader", { pack = "local/corpus", skills = { only = ["local", "remote"] } }]
`)
	r := scopedPackRun(t, `set -eu
cat "$HOME/.fixture/skills/corpus-local/SKILL.md"
cat "$HOME/.fixture/skills/corpus-remote/SKILL.md"
sh "$HOME/.fixture/skills/corpus-remote/bin/probe.sh"
test ! -e "$HOME/.fixture/skills/corpus-hidden"
test ! -e "$HOME/.fixture/skills/corpus-shared"
printf 'SOURCED-ONLY\n'
`)
	for _, want := range []string{"LOCAL-MARKER", "SOURCED-MARKER", "SIBLING-SUPPORT", "SOURCED-ONLY"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("missing %q: %s", want, r.combined())
		}
	}
}
