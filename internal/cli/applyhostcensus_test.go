package cli

// applyhostcensus_test.go is the STRUCTURAL gate behind gap G1: every contribution kind a
// pack declares must appear in `apply --host` output — as rendered, as refused, or as
// honored-but-unbuilt — and never be silently absent.
//
// The bug this prevents is subtle enough to be worth spelling out. `render.HostFields()`
// declares which kinds a host target honors; `entrypoint.RenderHostPack` renders the config
// SURFACES. Those are two different lists, and nothing tied them together: `skills` and
// `briefing` were in the honored set (they are prose kinds that "port") but no code rendered
// them, so they produced no output line at all — not even a refusal. `refusalReasons` could
// not catch them, because the census says they DO apply.
//
// So the fix is not "add two refusal strings" (that is the symptom); it is this test. It
// asserts the invariant over the WHOLE kind set, so the next kind added to HostFields()
// without a renderer fails here instead of vanishing in front of a user.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestApplyHostAccountsForEveryDeclaredKind builds a pack declaring one contribution of
// every kind in the closed set, runs `apply --host` in its default observe posture, and
// requires each kind to be named in the output.
func TestApplyHostAccountsForEveryDeclaredKind(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "census")
	writeCensusPack(t, packDir)

	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"packs":[{"source":"file://` + packDir + `","name":"census"}]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// A real $HOME is never touched: both the home and the config live under t.TempDir().
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	var out, errw bytes.Buffer
	// Observe posture (write=false) — this test is about the census, not about writing.
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("apply --host rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()

	for _, kind := range packdecl.KnownKinds() {
		if !strings.Contains(report, string(kind)) {
			t.Errorf("kind %q declared by the pack produced NO line in `apply --host` "+
				"output — it must be rendered, refused, or named as unimplemented, never "+
				"silently skipped (gap G1). Full output:\n%s", kind, report)
		}
	}
}

// writeCensusPack writes a pack.json declaring one contribution per known kind, plus the
// minimal tree those contributions point at. It is deliberately built from
// packdecl.KnownKinds() rather than a hand-written list, so a new kind joins the census
// automatically instead of being quietly untested.
func writeCensusPack(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"skills/censusskill", "files", "prompts"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("AGENTS.md", "# census pack\n\nprose\n")
	write("skills/censusskill/SKILL.md", "---\nname: censusskill\ndescription: d\n---\nbody\n")
	write("files/marker.txt", "marker\n")

	// One contribution per kind. The bodies are minimal-but-valid: what matters is that
	// each kind is DECLARED, so apply --host has to account for it.
	byKind := map[packdecl.Kind]string{
		packdecl.KindProgram:  `{"kind":"program","bin":"censusbin","via":"npm","package":"census-pkg"}`,
		packdecl.KindSkills:   `{"kind":"skills","from":"skills","into":".census/skills"}`,
		packdecl.KindBriefing: `{"kind":"briefing","from":"AGENTS.md","into":".census/AGENTS.md"}`,
		packdecl.KindFiles:    `{"kind":"files","from":"files","into":".census/files"}`,
		packdecl.KindConfig: `{"kind":"config","config":[{"agent":"census","name":"settings",` +
			`"codec":"json","path":"~/.census/settings.json","mode":"rmw",` +
			`"managed":{"censusKey":"censusValue"}}]}`,
		packdecl.KindConfigOverlay: `{"kind":"config-overlay","surface":"census/settings",` +
			`"config":{"managed":{"overlaidKey":true}}}`,
		packdecl.KindState:     `{"kind":"state","at":".census/state","scope":"workspace"}`,
		packdecl.KindReadsHost: `{"kind":"reads-host","host":".census/hostfile","into":"hostfile"}`,
		packdecl.KindMount:     `{"kind":"mount","host":".census/hostdir","into":"census-hostdir"}`,
		packdecl.KindEnv:       `{"kind":"env","vars":{"CENSUS_VAR":"1"}}`,
		packdecl.KindLaunch:    `{"kind":"launch","bin":"censusbin","flags":["--census"]}`,
		packdecl.KindHook:      `{"kind":"hook","hook":"per_jail_history","from":".census/history"}`,
		packdecl.KindAutonomy: `{"kind":"autonomy","autonomous":{"launch":[{"bin":"censusbin",` +
			`"flags":["--yolo"]}]},"guarded":{"launch":[{"bin":"censusbin"}]}}`,
	}

	var entries []string
	for _, k := range packdecl.KnownKinds() {
		body, ok := byKind[k]
		if !ok {
			t.Fatalf("kind %q has no census contribution — add one to writeCensusPack so "+
				"the new kind is covered by the no-silent-skip invariant", k)
		}
		entries = append(entries, body)
	}
	write("pack.json", `{"name":"census","contributes":[`+strings.Join(entries, ",")+`]}`)
}
