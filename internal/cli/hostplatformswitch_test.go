package cli

// hostplatformswitch_test.go pins PP-D1 (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29) at `yolo host --`: the real ~/.claude/settings.json turning on
// CLAUDE_CODE_USE_BEDROCK while claude's selected provider is not Bedrock is named in one line
// with both fixes, and the launch runs. Through hostMain to the exec, so deleting the call in
// hostExec fails here.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

func TestHostLaunchNamesAUsersOwnBedrockSwitch(t *testing.T) {
	withSwitch := func(value string) func(string) {
		return func(home string) {
			p := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(`{"env": {"CLAUDE_CODE_USE_BEDROCK": `+value+`}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	const line = "yolo host: claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which puts claude " +
		"on its own \"aws-bedrock\" client, but no \"aws-bedrock\" provider is selected for claude, so yolo " +
		"delivers it none of that platform's credentials: select one (-p bedrock), or remove " +
		"CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json (yolo leaves it alone)."

	rc, env, errs := hostGateRunIn(t, claudeAlone, nil, nil, "claude", withSwitch(`"1"`))
	if rc != 0 || env == nil {
		t.Fatalf("the line is a disclosure, never a refusal: rc=%d\n%s", rc, errs)
	}
	if strings.Count(errs, line) != 1 {
		t.Errorf("an unprofiled claude with the user's own Bedrock switch must be told once:\n%s", errs)
	}

	// Served: claude on bedrock, whose provider is Bedrock. And off: a switch reading false.
	for _, tc := range []struct {
		name, value string
		flags       []string
	}{
		{"claude on bedrock", `"1"`, []string{"-p", "bedrock"}},
		{"a switch that is off", `false`, nil},
	} {
		_, _, errs := hostGateRunIn(t, claudeAlone, nil, tc.flags, "claude", withSwitch(tc.value))
		if strings.Contains(errs, "sets CLAUDE_CODE_USE_BEDROCK") {
			t.Errorf("%s: nothing to name:\n%s", tc.name, errs)
		}
	}
}

// A SWITCH yolo WROTE, end to end at the host (HC-D25, PP-D1): `yolo host apply` on bedrock writes
// CLAUDE_CODE_USE_BEDROCK into the real ~/.claude/settings.json (providers.md#pv-d8). A launch on
// another provider while the host selection is still Bedrock names the key as yolo's, and the
// apply that moves claude's host selection off Bedrock removes it, after which no line prints.
// Before, the key stayed forever and the line told the user to remove a key of theirs.
//
// THE LEAF RECORD IS THE rmw ARM'S, so the fixture is claude with its settings surface declaring
// `"mode": "rmw"` (claudeForkWithRMWSettings) under `host_management: "own"`. The shipped surface
// declares no mode, so `own` composes it whole and writes no leaf record; the retired `assert`
// read-modify-wrote every surface, which is how this ran over the shipped pack before OQ-CO14.
func TestHostApplyRemovesTheBedrockSwitchItWroteAndTheLineSaysWhoWroteIt(t *testing.T) {
	const providers = `"providers": {"bedrock": {"region": "us-west-2"},
	  "mine": {"endpoints": {"anthropic": {"base_url": "https://anthropic.example"}}}},
	  "profiles": {"mine": {"provider": "mine"}}`
	fork := claudeForkWithRMWSettings(t)
	home := hostGateHome(t, `{"packs": ["`+fork+`"], "host_management": "own", "profile": {"claude": "bedrock"}, `+
		providers+`}`, nil)
	// `yolo host apply` refuses while a declared program is missing, and `claude` is on this
	// development jail's PATH but not on CI's: stub it, or the test passes only here.
	stubDeclaredBins(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	apply := func() string {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
			t.Fatalf("yolo host apply: rc=%d\n%s\n%s", rc, out.String(), errw.String())
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := apply(); !strings.Contains(got, `"CLAUDE_CODE_USE_BEDROCK"`) {
		t.Fatalf("host apply on bedrock must write the switch:\n%s", got)
	}

	// The host selection is still Bedrock; this one launch is not.
	const yolos = "yolo host: claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which " +
		"`yolo host apply` wrote there for claude's host selection, and it puts claude on its own " +
		"\"aws-bedrock\" client, but no \"aws-bedrock\" provider is selected for claude, so yolo " +
		"delivers it none of that platform's credentials: select one (-p bedrock), or run `yolo host " +
		"apply` with claude on a provider of another platform, which removes it."
	rc, reached, errs := hostExecRun(t, "claude", "-p", "mine")
	if rc != 0 || !reached {
		t.Fatalf("yolo host -p mine -- claude: rc=%d\n%s", rc, errs)
	}
	if strings.Count(errs, yolos) != 1 || strings.Contains(errs, "yolo leaves it alone") {
		t.Errorf("a switch yolo wrote must be named as yolo's, once:\n%s", errs)
	}

	// The host selection leaves Bedrock: the apply removes what it wrote, and nothing is named.
	userCfg(t, home, `{"packs": ["`+fork+`"], "host_management": "own", "profile": {"claude": "mine"}, `+
		providers+`}`)
	if got := apply(); strings.Contains(got, `"CLAUDE_CODE_USE_BEDROCK"`) {
		t.Errorf("host apply with claude off Bedrock must remove the switch it wrote:\n%s", got)
	}
	if rc, reached, errs := hostExecRun(t, "claude"); rc != 0 || !reached ||
		strings.Contains(errs, "sets CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("with the switch gone, no line: rc=%d\n%s", rc, errs)
	}
}

// A SWITCH yolo WROTE, AT A LAUNCH WHERE NO HOST APPLY RENDERS (OQ-CO14): the key unset, or
// "none", over a home the retired `assert` wrote CLAUDE_CODE_USE_BEDROCK into, with the host's
// computed-leaf record naming it. `yolo host apply` refuses here, so a line sending the user to
// it to remove the key repeated at every launch; the line names the removal by hand, says the
// apply renders only under "own", and offers `--revert`, which runs under "none". Through
// hostMain to the pre-flight, so deleting the field's assignment in platformSwitchConflicts
// fails here (and TestHostApplyRemovesTheBedrockSwitchItWroteAndTheLineSaysWhoWroteIt, under
// "own", fails the other way).
//
// The launched program is claude by NAME, since the host notch names the conflicts of the one
// agent it launches, but a path that does not exist: the line prints in the pre-flight, before
// the target resolves, and nothing can be exec'd past it.
func TestHostLaunchWhereNoHostApplyRendersNamesAYoloWrittenSwitchForRemovalByHand(t *testing.T) {
	const providers = `"providers": {"mine": {"endpoints": {"anthropic": {"base_url": "https://anthropic.example"}}}},
	  "profiles": {"mine": {"provider": "mine"}}`
	for _, mode := range []string{"", `"host_management": "none", `} {
		home := hostGateHome(t, `{"packs": ["claude"], `+mode+providers+`}`, nil)
		writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`)
		rec := render.Host(home, nil, render.OwnershipUnstated).LeafRecordPath("claude", "settings")
		if want := filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
			"claude-settings.leaves.json"); rec != want {
			t.Fatalf("fixture: the leaf record is at %s, want %s", rec, want)
		}
		writeFile(t, rec, `{"/env/CLAUDE_CODE_USE_BEDROCK": "1"}`)

		reached := false
		orig := hostSyscallExec
		hostSyscallExec = func(string, []string, []string) error { reached = true; return nil }
		t.Cleanup(func() { hostSyscallExec = orig })
		var out, errw bytes.Buffer
		bin := filepath.Join(t.TempDir(), "no-such-dir", "claude")
		hostMain([]string{"-p", "mine", "--", bin}, &out, &errw, false, nil)
		errs := errw.String()
		if reached {
			t.Fatalf("host_management %q: the launch exec'd a program that does not exist:\n%s", mode, errs)
		}
		if !strings.Contains(errs, "which `yolo host apply` wrote there for claude's host selection") {
			t.Fatalf("host_management %q: no line naming the switch yolo wrote:\n%s", mode, errs)
		}
		if strings.Contains(errs, "or run `yolo host apply` with") {
			t.Errorf("host_management %q: the line sends the user to an apply that refuses here:\n%s", mode, errs)
		}
		for _, want := range []string{"remove CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json by hand",
			`host_management is not "own"`, "`yolo host apply --revert`"} {
			if !strings.Contains(errs, want) {
				t.Errorf("host_management %q: the line does not say %q:\n%s", mode, want, errs)
			}
		}
	}
}

// claudeForkWithRMWSettings is a copy of the shipped claude pack, configured by path, whose
// claude/settings surface declares `"mode": "rmw"`, so `own` runs the rmw arm over it — the arm
// that keeps the host's computed-leaf record (HC-D25) — where the shipped surface composes whole.
func claudeForkWithRMWSettings(t *testing.T) string {
	t.Helper()
	var claude *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "claude" {
			claude = p
		}
	}
	if claude == nil {
		t.Fatal("fixture: no shipped claude pack")
	}
	fork := filepath.Join(t.TempDir(), "claude")
	if err := os.CopyFS(fork, os.DirFS(claude.Root)); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(fork, "pack.json")
	doc, err := json5.Decode([]byte(readFileT(t, manifest)))
	decl, _ := doc.(*jsonx.OrderedMap)
	if err != nil || decl == nil {
		t.Fatalf("fixture: the shipped claude pack.json: %v", err)
	}
	found := 0
	contributes, _ := decl.Get("contributes")
	list, _ := contributes.([]any)
	for _, c := range list {
		c, _ := c.(*jsonx.OrderedMap)
		if c == nil {
			continue
		}
		if kind, _ := c.Get("kind"); kind != "config" {
			continue
		}
		surfaces, _ := c.Get("config")
		inner, _ := surfaces.([]any)
		for _, s := range inner {
			s, _ := s.(*jsonx.OrderedMap)
			if s == nil {
				continue
			}
			if name, _ := s.Get("name"); name != "settings" {
				continue
			}
			if mode, declared := s.Get("mode"); declared {
				t.Fatalf("fixture: claude/settings already declares a mode (%v)", mode)
			}
			s.Set("mode", "rmw")
			found++
		}
	}
	if found != 1 {
		t.Fatalf("fixture: %d claude/settings config surfaces in the shipped pack, want 1", found)
	}
	out, err := jsonx.DumpsIndent(decl, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifest, out+"\n")
	return fork
}
