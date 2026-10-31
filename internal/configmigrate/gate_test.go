package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// treeState is every name under root with its bytes or, for a link, its target: what "nothing
// was touched" means.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "-> " + target
		case info.Mode().IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = string(b)
		default:
			out[rel] = "dir"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// launchGateProblems is what the launch's config gate says of the files as they are now.
func launchGateProblems(t *testing.T, ws string) []string {
	t.Helper()
	cfg, _, err := config.LoadConfigWithSources(ws, true, func(string) {})
	if err != nil {
		return []string{err.Error()}
	}
	errs, _ := config.ValidateConfig(cfg, ws, loopholes.NewResolver())
	return errs
}

// RC-D44: the converted files go through the launch's config gate, staged off to the side,
// before any original is touched; a refusal there refuses the plan with nothing changed. The
// gate is faked to refuse the converted layout, so the test fails if Plan stops calling it.
func TestAPlanTheLaunchGateRefusesTouchesNothing(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	elsewhere := filepath.Join(home, "dotfiles")
	write(t, filepath.Join(elsewhere, "config.jsonc"), `{"packages": ["just"]}`+"\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "config.jsonc"), filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	ws := realDir(t)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"packages": ["htop"]}`+"\n")

	real := runGate
	staged := 0
	runGate = func(userConfigDir, workspace string) config.MigrationGate {
		got := real(userConfigDir, workspace)
		if userConfigDir != dir {
			staged++
			got.Problems = append(got.Problems, "config.packages: refused by the test")
			got.Located = append(got.Located, "config.toml:1:1: config.packages: refused by the test")
		}
		return got
	}
	t.Cleanup(func() { runGate = real })

	before := map[string]map[string]string{"home": treeState(t, home), "ws": treeState(t, ws)}
	p, err := Plan(discoverFor(t, Request{UserConfigDir: dir, Workspace: ws}))
	var refused *ValidationError
	if !errors.As(err, &refused) {
		t.Fatalf("Plan = %v, want the launch gate's refusal (gate ran on %d staged layouts)", err, staged)
	}
	if msg := err.Error(); !strings.Contains(msg, "nothing was changed") || !strings.Contains(msg, "refused by the test") {
		t.Errorf("the refusal must say nothing was changed and name the problem: %q", msg)
	}
	if _, err := Apply(p, ApplyOptions{}); err == nil {
		t.Fatal("Apply carried out a plan the launch gate refused")
	}
	after := map[string]map[string]string{"home": treeState(t, home), "ws": treeState(t, ws)}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused migration touched the files:\nbefore %v\nafter  %v", before, after)
	}
}

// Cause 1 of the 2026-10-29 report: an old path entry in `packs` was copied through as written,
// and the next launch refused it ("expected source/pack, such as local/matt"). A pack under
// ~/.config/yolo-jail/packs becomes local/<name>; one elsewhere is named through a
// [pack_sources] entry for its directory (OQ-RC15). The converted files pass the launch's gate.
func TestLegacyPackPathsConvertToSourceHandlesTheLaunchAccepts(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "packs", "mine", "pack.toml"), "name = \"mine\"\n")
	write(t, filepath.Join(home, "src", "packs", "other", "pack.toml"), "name = \"other\"\n")
	write(t, filepath.Join(dir, "config.jsonc"),
		`{"packs": ["`+filepath.Join(dir, "packs", "mine")+`", "~/src/packs/other"]}`+"\n")
	ws := realDir(t)

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "config.toml"))
	for _, want := range []string{`"local/mine"`, `"packs/other"`, "[pack_sources]", `packs = "~/src/packs"`} {
		if !strings.Contains(got, want) {
			t.Errorf("config.toml lacks %s:\n%s", want, got)
		}
	}
	for _, e := range launchGateProblems(t, ws) {
		if strings.Contains(e, "packs") || strings.Contains(e, "pack_sources") {
			t.Errorf("the launch refuses the converted packs: %s", e)
		}
	}
}

// The owner's overrides.jsonc shape (synthetic): an MCP server and a legacy `profile` selection
// with "*" and a null. It converts to [active_provider] with `default` and "native", which the
// launch accepts and reads as the same selection.
func TestALegacyProfileSelectionWithNullConvertsToActiveProvider(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["claude", "bedrock"], "profiles": {"bedrock": {"provider": "bedrock"}}, "include_if_found": ["overrides.jsonc"]}`+"\n")
	write(t, filepath.Join(dir, "overrides.jsonc"), `{
  "mcp_servers": { "tavily": { "command": "npx", "args": ["-y", "tavily-mcp@latest"], "env": { "TAVILY_API_KEY": "${TAVILY_API_KEY}" } } },
  "profile": { "*": "bedrock", "claude": null, }
}
`)
	ws := realDir(t)

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "overrides.toml"))
	for _, want := range []string{"[active_provider]", `default = "bedrock"`, `claude = "native"`, "[mcp_servers.tavily]"} {
		if !strings.Contains(got, want) {
			t.Errorf("overrides.toml lacks %s:\n%s", want, got)
		}
	}
	for _, e := range launchGateProblems(t, ws) {
		if strings.Contains(e, "active_provider") || strings.Contains(e, "profile") || strings.Contains(e, "mcp_servers") {
			t.Errorf("the launch refuses the converted overrides: %s", e)
		}
	}
}
