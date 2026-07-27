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

// validateAgentsScopeErrs runs ValidateConfig against a merged map and returns
// just the config.agents problems.
func validateAgentsScopeErrs(t *testing.T, workspace, mergedAgents string) []string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"agents": `+mergedAgents+`}`), workspace, nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.agents") {
			out = append(out, e)
		}
	}
	return out
}

// A workspace config selecting an agent the user did not decides a CREDENTIAL
// BOUNDARY question: hostFileArgs mounts each selected agent's
// AgentSpec.HostFiles, so `agents` in an agent-editable, repo-committed file
// could pull a host settings.json into the jail. This is the hole a84b11c closed
// for host_files, which stayed open via `agents`.
func TestValidateAgentsWorkspaceCannotWidenUserSet(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"agents": ["pi"]}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"agents": ["claude"]}`)

	// `agents` is in overrideListKeys, so the merge yields the workspace value.
	errs := validateAgentsScopeErrs(t, ws, `["claude"]`)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	for _, want := range []string{"claude", "widen", "user"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("error %q does not mention %q", errs[0], want)
		}
	}
}

// Narrowing is legitimate: a repo saying "only claude here" mounts strictly
// fewer host files than the user already allowed, so it crosses no boundary.
func TestValidateAgentsWorkspaceMayNarrow(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"agents": ["claude", "pi", "codex"]}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"agents": ["claude"]}`)

	if errs := validateAgentsScopeErrs(t, ws, `["claude"]`); len(errs) != 0 {
		t.Errorf("errors = %v, want none (narrowing is allowed)", errs)
	}
}

// With no user `agents` key the effective user set is DefaultAgents, so a
// workspace naming anything outside it still widens.
func TestValidateAgentsWorkspaceWidensBeyondDefault(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"agents": ["codex"]}`)

	errs := validateAgentsScopeErrs(t, ws, `["codex"]`)
	if len(errs) != 1 || !strings.Contains(errs[0], "codex") {
		t.Fatalf("errors = %v, want one naming codex", errs)
	}
}

// A workspace with no `agents` key at all must stay clean.
func TestValidateAgentsNoWorkspaceKeyClean(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"agents": ["claude"]}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{}`)

	if errs := validateAgentsScopeErrs(t, ws, `["claude"]`); len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

// `gemini` was removed (Google is deprecating Gemini CLI). A config still naming
// it must get a RETIREMENT message, not a bare "unknown agent" that reads like a
// typo — the same treatment `docker` gets in validateRuntime.
func TestValidateAgentsGeminiRetired(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"agents": ["gemini"]}`), t.TempDir(), nil)
	var found string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.agents[0]") {
			found = e
		}
	}
	if found == "" {
		t.Fatalf("no config.agents[0] error; got %v", errs)
	}
	for _, want := range []string{"REMOVED", "agy"} {
		if !strings.Contains(found, want) {
			t.Errorf("error %q does not mention %q", found, want)
		}
	}
	if strings.Contains(found, "unknown agent") {
		t.Errorf("gemini must get a retirement message, not 'unknown agent': %q", found)
	}
}
