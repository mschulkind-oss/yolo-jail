package cli

// verbadvice_test.go pins the advice that names a config verb rather than a key to hand-edit
// (AGENTS.md "Advice names a verb, not a file to hand-edit"), and that names the user config file
// that exists, never a hard-coded legacy config.jsonc.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// verbAdviceHome points HOME at a fresh, symlink-resolved home whose user config is the named
// file, or none when name is "".
func verbAdviceHome(t *testing.T, name string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	if name != "" {
		dir := filepath.Join(home, ".config", "yolo-jail")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "\n"
		if strings.HasSuffix(name, ".jsonc") {
			body = "{}\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// The launch's selection is made the configured one by `yolo active-provider` for one profile and
// by the generic `yolo config set active_provider.<agent>` for an active set, never by the legacy
// `"profile": {…}` spelling.
func TestHostSelectionRemedyNamesTheActiveProviderVerb(t *testing.T) {
	one := &hostComposition{agent: "claude", profile: "bedrock", set: []string{"bedrock"}}
	if got, want := one.selectionRemedy(), "run `yolo active-provider claude bedrock`, then `yolo host apply`"; !strings.Contains(got, want) {
		t.Errorf("one-profile remedy = %q, want it to contain %q", got, want)
	}
	set := &hostComposition{agent: "pi", profile: "zai", set: []string{"zai", "openrouter"}}
	if got, want := set.selectionRemedy(), "`yolo config set active_provider.pi '[\"zai\", \"openrouter\"]' --user`"; !strings.Contains(got, want) {
		t.Errorf("active-set remedy = %q, want it to contain %q", got, want)
	}
	for _, c := range []*hostComposition{one, set} {
		if got := c.selectionRemedy(); strings.Contains(got, `"profile"`) {
			t.Errorf("remedy names the legacy profile key: %q", got)
		}
	}
}

// Each command the remedies name is one the verb accepts: run through the dispatcher's own
// entry points with --dry-run against a temp home, so nothing is written.
func TestVerbAdviceCommandsAreAcceptedByTheirVerbs(t *testing.T) {
	home := verbAdviceHome(t, "config.toml")
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"),
		[]byte("packs = [\"claude\", \"aws-auth\", \"macos-log\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheParent := t.TempDir()
	ws := t.TempDir()
	t.Chdir(ws)
	for _, args := range [][]string{
		{"set", "macos_user_shared_tmp", "true", "--user", "--dry-run"},
		{"add", "env.files", `"/srv/env/prod.env"`, "--user", "--dry-run"},
		{"set", "programs.overrides.pi.prebuilt", "true", "--user", "--dry-run"},
		{"set", "mcp_servers.board.command", `"board-mcp"`, "--user", "--dry-run"},
		{"set", "allow.aws-auth.role_arn", `"arn:aws:iam::123456789012:role/narrow"`, "--user", "--dry-run"},
		{"set", "allow.aws-auth.unnarrowed", "true", "--user", "--dry-run"},
		{"set", "allow.macos-log.full", "true", "--user", "--dry-run"},
		{"set", "cache_relocations.go-build", `"` + filepath.Join(cacheParent, "go-build") + `"`, "--user", "--dry-run"},
	} {
		var out, errw bytes.Buffer
		if rc := runConfigEditWith(args, &out, &errw, ws); rc != 0 {
			t.Errorf("yolo config %s: rc %d, stderr %s", strings.Join(args, " "), rc, errw.String())
		}
	}
	var out, errw bytes.Buffer
	if rc := activeProviderRunW([]string{"claude", "native", "--dry-run"}, &out, &errw); rc != 0 {
		t.Errorf("yolo active-provider claude native: rc %d, stderr %s", rc, errw.String())
	}
	// packload.PackAddCommand's form, for a shipped pack the user config does not select yet,
	// and `yolo pack remove`'s for one it does.
	if rc := packAdd([]string{"copilot-auth", "--dry-run"}, &out, &errw); rc != 0 {
		t.Errorf("%s: rc %d, stderr %s", packload.PackAddCommand("copilot-auth"), rc, errw.String())
	}
	if rc := packRemove([]string{"claude", "--dry-run"}, &out, &errw); rc != 0 {
		t.Errorf("yolo pack remove claude: rc %d, stderr %s", rc, errw.String())
	}
}

// A host apply's remedy names the user config that exists in the rendered home — config.toml
// when there is none — never a legacy config.jsonc the user does not have.
func TestUserConfigPathInNamesTheLiveFile(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"", "config.toml"},
		{"config.toml", "config.toml"},
		{"config.jsonc", "config.jsonc"},
	} {
		home := verbAdviceHome(t, tc.file)
		if got, want := userConfigPathIn(home), filepath.Join(home, ".config", "yolo-jail", tc.want); got != want {
			t.Errorf("user config %q: userConfigPathIn = %q, want %q", tc.file, got, want)
		}
	}
}

// The declare-it remedy names the verb that writes one field of the entry, and the gated-loss
// remedy, for a readable user config, the readable keys and their verbs.
func TestHostApplyRemediesNameTheConfigVerbs(t *testing.T) {
	home := verbAdviceHome(t, "")
	if got := mcpEntryRemedy(home, nil); !strings.Contains(got, "`yolo config set <table>.<name>.<field> <value> --user`") {
		t.Errorf("declare-it remedy names no verb: %s", got)
	}
	readable := entrypoint.GatedEntryLossRemedy(userConfigPathIn(home))
	for _, want := range []string{"`env.files`", "`yolo config add env.files <file> --user`", "`yolo active-provider <agent> <provider>`"} {
		if !strings.Contains(readable, want) {
			t.Errorf("gated-loss remedy for a TOML user config lacks %q: %s", want, readable)
		}
	}
	if strings.Contains(readable, "env_sources") || strings.Contains(readable, "`profile`") {
		t.Errorf("gated-loss remedy for a TOML user config names a legacy key: %s", readable)
	}
	legacy := entrypoint.GatedEntryLossRemedy(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"))
	if !strings.Contains(legacy, "`env_sources`") {
		t.Errorf("gated-loss remedy for a legacy user config lost its key: %s", legacy)
	}
}

// A fetched pack's MCP server is left out at the host with the verb that declares it in the user
// config that exists, not a hard-coded config.jsonc.
func TestHostMCPOmissionNamesTheVerbAndTheLiveUserConfig(t *testing.T) {
	home := verbAdviceHome(t, "")
	p := &packload.Pack{Name: "fetched"}
	for _, shipped := range packload.Embedded() {
		if shipped.Name == "chrome-devtools" {
			p.Decl = shipped.Decl
		}
	}
	_, omitted := hostMCPPacks([]*packload.Pack{p})
	if len(omitted) != 1 {
		t.Fatalf("omitted = %v, want one line", omitted)
	}
	for _, want := range []string{"`yolo config set mcp_servers.chrome-devtools.command <command> --user`",
		filepath.Join(home, ".config", "yolo-jail", "config.toml")} {
		if !strings.Contains(omitted[0], want) {
			t.Errorf("omission lacks %q: %s", want, omitted[0])
		}
	}
	if strings.Contains(omitted[0], "config.jsonc") {
		t.Errorf("omission names the legacy file: %s", omitted[0])
	}
}
