package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unit tests for ValidateConfig's cache_relocations rules. Other validators are
// covered by the differential oracle; these are the filesystem-touching ones.

// validateCache runs ValidateConfig over a config containing only
// cache_relocations and returns just the cache_relocations errors.
func validateCache(t *testing.T, workspace, body string) []string {
	t.Helper()
	// Pin the host case explicitly. The target-parent probe is host-only (see
	// validateCacheRelocations), and this project is developed from inside its
	// own jail, where YOLO_VERSION is set — without this the suite would assert
	// host behavior while running as if in a jail.
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"cache_relocations": `+body+`}`), workspace, nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.cache_relocations") {
			out = append(out, e)
		}
	}
	return out
}

func TestValidateCacheRelocationsKnownKey(t *testing.T) {
	// Without the knownTopLevelConfigKeys entry every config carrying the key
	// would fail with "unknown key".
	errs, _ := ValidateConfig(decode(t, `{"cache_relocations": {}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "unknown key") {
			t.Errorf("unexpected error: %s", e)
		}
	}
}

func TestValidateCacheRelocationsBadSubdirs(t *testing.T) {
	ws := t.TempDir()
	parent := filepath.Join(ws, "data")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "hf")
	for _, key := range []string{"../etc", "a/b", ".", "..", ""} {
		errs := validateCache(t, ws, `{"`+key+`": "`+target+`"}`)
		if len(errs) != 1 || !strings.Contains(errs[0], "invalid subdir") {
			t.Errorf("subdir %q: errors = %v, want one 'invalid subdir'", key, errs)
		}
	}
}

func TestValidateCacheRelocationsTargetRules(t *testing.T) {
	ws := t.TempDir()
	parent := filepath.Join(ws, "data")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	hf := filepath.Join(parent, "hf")

	cases := []struct {
		name string
		body string
		want string
	}{
		{"relative target", `{"huggingface": "data/hf"}`, "must be an absolute path"},
		{"non-string target", `{"huggingface": 7}`, "expected an absolute host path string"},
		{"not an object", `["huggingface"]`, "expected an object mapping"},
		{
			"duplicate targets",
			`{"huggingface": "` + hf + `", "uv": "` + hf + `/"}`,
			"is already relocated from subdir",
		},
		{
			"missing target parent",
			`{"huggingface": "` + filepath.Join(ws, "typo", "hf") + `"}`,
			"parent directory of the target does not exist",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateCache(t, ws, tc.body)
			if len(errs) != 1 || !strings.Contains(errs[0], tc.want) {
				t.Errorf("errors = %v, want one containing %q", errs, tc.want)
			}
		})
	}
}

// A missing FINAL segment is fine — storage.EnsureCacheRelocations creates it.
// Only a missing parent is an error (a missing parent means a typo).
func TestValidateCacheRelocationsMissingFinalSegmentIsOK(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	errs := validateCache(t, ws, `{"huggingface": "`+filepath.Join(ws, "data", "hf")+`"}`)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

func TestValidateCacheRelocationsRejectsWorkspaceScope(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "hf")
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"cache_relocations": {"huggingface": "`+target+`"}}`)

	// ValidateConfig sees only the merged map, so the workspace-scope error can
	// only come from the re-read of the workspace config.
	errs := validateCache(t, ws, `{"huggingface": "`+target+`"}`)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	for _, want := range []string{"user-scope only", "~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("error %q does not name %q", errs[0], want)
		}
	}
}

func TestValidateCacheRelocationsWorkspaceLocalScopeAlsoRejected(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "hf")
	write(t, filepath.Join(ws, WorkspaceLocalConfigName),
		`{"cache_relocations": {"huggingface": "`+target+`"}}`)

	errs := validateCache(t, ws, `{"huggingface": "`+target+`"}`)
	if len(errs) != 1 || !strings.Contains(errs[0], "user-scope only") {
		t.Errorf("errors = %v, want one 'user-scope only'", errs)
	}
}

// Regression: a valid HOST relocation must not brick jails. In a jail the
// merged config is the host-written snapshot (or the read-only-mounted host user
// config), so it carries the host's targets — paths deliberately absent from the
// jail's mount namespace. Stat'ing them there turned every nested `yolo` run and
// every in-jail `yolo check` into "Invalid jail config". The shape and scope
// rules must still fire; only the filesystem probe is host-only.
func TestValidateCacheRelocationsSkipsTargetParentInJail(t *testing.T) {
	ws := t.TempDir()
	// Use a path under a fresh temp dir: the target's parent must not exist on
	// the host for the missing-parent probe to fire. The old hardcoded
	// /data/relocated/yolo-jail/cache/huggingface path exists on dev machines
	// with cache_relocations configured, making this test host-dependent.
	hostOnly := filepath.Join(t.TempDir(), "nonexistent-parent", "huggingface")
	body := `{"huggingface": "` + hostOnly + `"}`

	if errs := validateCache(t, ws, body); len(errs) != 1 ||
		!strings.Contains(errs[0], "parent directory of the target does not exist") {
		t.Fatalf("host: errors = %v, want the missing-parent error (typo protection)", errs)
	}

	// validateCache pins YOLO_VERSION=""; set it back to model a jail.
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	errs, _ := ValidateConfig(decode(t, `{"cache_relocations": `+body+`}`), ws, nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.cache_relocations") {
			t.Errorf("in jail: unexpected error %q — a valid host config must not brick nested jails", e)
		}
	}

	// Shape errors still fire in a jail: only the fs probe is gated.
	errs, _ = ValidateConfig(decode(t, `{"cache_relocations": {"../etc": "`+hostOnly+`"}}`), ws, nil)
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e, "config.cache_relocations") {
			found = true
		}
	}
	if !found {
		t.Errorf("in jail: bad subdir accepted; shape rules must apply everywhere")
	}
}

// User scope only: no workspace file, valid entry -> clean.
func TestValidateCacheRelocationsUserScopeClean(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "hf")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if errs := validateCache(t, ws, `{"huggingface": "`+target+`"}`); len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

// repo_path was retired (2026-07-23). It is still a KNOWN key (so an existing
// config does not hard-error on upgrade), but the resolver no longer reads it —
// so its presence must yield a deprecation WARNING, never an error.
func TestValidateRepoPathRetiredIsWarnNotError(t *testing.T) {
	errs, warns := ValidateConfig(decode(t, `{"repo_path": "/home/matt/code/yolo-jail"}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "repo_path") {
			t.Errorf("repo_path produced an error, want warning only: %q", e)
		}
	}
	found := false
	for _, w := range warns {
		if strings.HasPrefix(w, "config.repo_path:") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a config.repo_path deprecation warning, got warns=%v", warns)
	}
}

// A non-string repo_path is still a type error (guards the value shape even for
// a retired key, so a malformed config is not silently accepted).
func TestValidateRepoPathNonStringStillErrors(t *testing.T) {
	errs, _ := ValidateConfig(decode(t, `{"repo_path": 42}`), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "config.repo_path: expected a string path") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a repo_path type error, got errs=%v", errs)
	}
}

// host_claude_files and host_pi_files were RETIRED (2026-07-23): the host-file
// set each agent reads is now a yolo-declared, non-widenable per-agent constant
// (internal/agents.AgentSpec.HostFiles), so a workspace config can no longer
// widen which host files cross the credential boundary. Both keys are dropped
// from knownTopLevelConfigKeys and now hard-error as unknown keys, exactly like
// the retired `docker` runtime — "pretend they never existed" (plan §10.4 D4).
func TestRetiredHostFilesKeysAreUnknown(t *testing.T) {
	for _, key := range []string{"host_pi_files", "host_claude_files"} {
		errs, _ := ValidateConfig(decode(t, `{"`+key+`": ["settings.json"]}`), t.TempDir(), nil)
		want := "config." + key + ": unknown key"
		found := false
		for _, e := range errs {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("key %q: want %q in errs, got %v", key, want, errs)
		}
	}
}

// agentsErrs runs ValidateConfig over a config carrying only `agents` and returns
// just the config.agents problems.
func agentsErrs(t *testing.T, body string) []string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"agents": `+body+`}`), t.TempDir(), nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.agents") {
			out = append(out, e)
		}
	}
	return out
}

// The `agents` key is DELETED, so a config carrying it must be REJECTED — not
// ignored, and not quietly accepted. Ignoring it would be the worst outcome
// available: the user asked for claude, yolo silently gives them nothing, and
// nothing in the jail can explain why.
func TestValidateAgentsKeyIsRejected(t *testing.T) {
	// Every shape the key ever took, valid or not, must land on the same verdict —
	// the key is gone, so its CONTENTS are no longer a question yolo answers.
	for _, body := range []string{`["claude"]`, `[]`, `null`, `"claude"`, `["gemini"]`} {
		errs := agentsErrs(t, body)
		if len(errs) == 0 {
			t.Errorf("agents=%s: want a rejection, got none", body)
		}
	}
}

// A retired key earns a RETIREMENT message, not a bare "unknown key": "unknown"
// reads like a typo and sends the reader hunting for the correct spelling of
// something that no longer exists. Same treatment `docker` gets in validateRuntime
// and `env` gets in validateEnvSources — and the message must point at what
// replaced it, which is `packs`.
func TestValidateAgentsRetirementMessageNamesPacks(t *testing.T) {
	errs := agentsErrs(t, `["claude"]`)
	var retirement string
	for _, e := range errs {
		if strings.Contains(e, "REMOVED") {
			retirement = e
		}
	}
	if retirement == "" {
		t.Fatalf("no REMOVED message among %v", errs)
	}
	for _, want := range []string{"packs", "pack"} {
		if !strings.Contains(retirement, want) {
			t.Errorf("retirement message %q does not mention %q", retirement, want)
		}
	}
}

// `agents` stays IN knownTopLevelConfigKeys even though the key is deleted, so the
// user gets ONE targeted error instead of two reports of the same problem.
//
// Dropping it from the set also produced a generic "config.agents: unknown key"
// alongside the retirement message — verified by hand, `yolo check` printed both
// lines. The retirement message already says the key is gone AND what to do
// instead, so the bare one adds nothing and makes the output look like two
// separate mistakes. This is the same reason `repo_path` stays listed.
//
// The key is still a hard ERROR, not a warning: it is deleted, not merely
// deprecated, so a config naming it cannot be honored.
func TestValidateAgentsRetiredIsTheOnlyError(t *testing.T) {
	if _, known := knownTopLevelConfigKeys["agents"]; !known {
		t.Error("`agents` must stay in knownTopLevelConfigKeys so the retirement " +
			"message is the ONLY error — otherwise the generic unknown-key error " +
			"duplicates it")
	}
	errs := agentsErrs(t, `["claude"]`)
	if len(errs) != 1 {
		t.Errorf("want exactly one config.agents error, got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if e == "config.agents: unknown key" {
			t.Errorf("the bare unknown-key error duplicates the retirement message: %v", errs)
		}
	}
}

// A config with NO `agents` key must stay clean. The obvious regression if the
// retirement check ever conflates "absent" with "nil".
func TestValidateAgentsAbsentIsClean(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.agents") {
			t.Errorf("unexpected error on a config without the key: %s", e)
		}
	}
}

// SelectedAgents is a shim that returns the EMPTY set now that there is no key to
// read and no DefaultAgents to fall back on. The non-nil part is load-bearing:
// agents.ResolveAgents treats a nil names argument as "unspecified" and substitutes
// DefaultAgents, so a nil return here would resurrect claude in every caller — the
// exact behavior deleting the key removes.
func TestSelectedAgentsIsEmptyAndNonNil(t *testing.T) {
	for _, src := range []string{`{}`, `{"agents": ["claude"]}`, `{"agents": []}`} {
		got := SelectedAgents(decode(t, src))
		if len(got) != 0 {
			t.Errorf("SelectedAgents(%s) = %v, want empty", src, got)
		}
		if got == nil {
			t.Errorf("SelectedAgents(%s) returned nil; ResolveAgents(nil) falls back to DefaultAgents", src)
		}
	}
}

// The `agents` key was overrideListKeys' only member, and its replace-wholesale
// merge is what let a repo-committed, agent-editable workspace config decide agent
// selection — and through it which host files mounted (the hole validateAgentsScope
// existed to plug). With the key gone the exception has no members, so EVERY list
// key union-merges. This is the regression for the mechanism's removal: a
// resurrected override-list would silently reopen that class of hole for whatever
// key got added to it.
func TestMergeConfigUnionsEveryListKey(t *testing.T) {
	base := decode(t, `{"packages": ["a"], "mcp_presets": ["chrome-devtools"]}`)
	over := decode(t, `{"packages": ["b"], "mcp_presets": ["sequential-thinking"]}`)
	merged := MergeConfig(base, over)
	for key, want := range map[string][]string{
		"packages":    {"a", "b"},
		"mcp_presets": {"chrome-devtools", "sequential-thinking"},
	} {
		v, _ := merged.Get(key)
		got, _ := v.([]any)
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v (union, not replace)", key, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %v, want %q", key, i, got[i], want[i])
			}
		}
	}
}
