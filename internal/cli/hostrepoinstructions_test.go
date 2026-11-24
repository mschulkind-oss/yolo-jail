package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func hostRepoInstrConfig(mode string) *jsonx.OrderedMap {
	table := jsonx.NewOrderedMap()
	table.Set("mode", mode)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("repo_instructions", table)
	return cfg
}

func shippedHostPacks(t *testing.T, names ...string) []*packload.Pack {
	t.Helper()
	var out []*packload.Pack
	for _, want := range names {
		for _, p := range packload.Embedded() {
			if p.Name == want {
				c := *p
				out = append(out, &c)
			}
		}
	}
	if len(out) != len(names) {
		t.Fatalf("missing a shipped pack among %v", names)
	}
	return out
}

// `yolo host -- claude` has no per-workspace settings file, so the unconfined path carries the
// exclusions as claude's own --settings argument (IF-D16), anchored at the directory it runs in,
// and says so; a program that is no agent gets no line.
func TestHostLaunchHandsClaudeItsSettingsFlagAndSaysSo(t *testing.T) {
	ws := t.TempDir()
	packs := shippedHostPacks(t, "claude", "pi")
	launch := &hostComposition{cfg: hostRepoInstrConfig("ignore"), packs: packs, workspace: ws}
	var errw bytes.Buffer
	applyHostRepoInstructions(launch, "claude", &errw)
	settings := packload.LaunchFlagsFor(packs, false)["claude"]
	joined := strings.Join(settings, " ")
	if !strings.Contains(joined, "--settings") || !strings.Contains(joined, `"claudeMdExcludes":["`+ws+`/**/AGENTS.md"`) {
		t.Errorf("claude's host flags = %v, want --settings JSON anchored at %s", settings, ws)
	}
	if !strings.HasPrefix(errw.String(), "yolo host: repo instructions: claude: ignoring ") || strings.Contains(errw.String(), "pi:") {
		t.Errorf("disclosure = %q", errw.String())
	}
	var other bytes.Buffer
	applyHostRepoInstructions(launch, "bash", &other)
	if other.Len() != 0 {
		t.Errorf("a command that is no agent got %q", other.String())
	}
	var kept bytes.Buffer
	applyHostRepoInstructions(&hostComposition{cfg: hostRepoInstrConfig("keep"), packs: shippedHostPacks(t, "claude"), workspace: ws}, "claude", &kept)
	if kept.Len() != 0 {
		t.Errorf("keep said %q", kept.String())
	}
}

// `yolo host apply` writes a real home every workspace shares: it carries the user scope's
// replacement text and nothing per workspace (IF-D13).
func TestHostApplyBriefingCarriesTheUserScopesReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "rules.md"), []byte("Always run the tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	user := "agents_md_extra = \"my extra\"\n\n[repo_instructions]\nmode = \"replace\"\nreplace_with = \"~/rules.md\"\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	extra, err := hostBriefingExtra()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(extra, "my extra") || !strings.Contains(extra, "Always run the tests.") {
		t.Errorf("extra = %q", extra)
	}
	// An unreadable replacement is a reason, not a stopped apply: the plain extra comes back.
	// (The apply call site is pinned by the next test.)
	if err := os.Remove(filepath.Join(home, "rules.md")); err != nil {
		t.Fatal(err)
	}
	extra, err = hostBriefingExtra()
	if err == nil || extra != "my extra" || !strings.Contains(err.Error(), "yolo config unset repo_instructions.replace_with") {
		t.Errorf("extra = %q, err = %v", extra, err)
	}
}
