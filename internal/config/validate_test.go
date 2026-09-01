package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
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
// set is yolo-declared and non-widenable, so a workspace config can no longer
// widen which host files cross the credential boundary. Both keys are dropped
// from knownTopLevelConfigKeys and now hard-error as unknown keys, exactly like
// the retired `docker` runtime — "pretend they never existed" (plan §10.4 D4).
//
// WHERE the set is declared has moved twice and the boundary has not. It was
// `internal/agents.AgentSpec.HostFiles`, a fixed per-agent Go constant; it is now
// each pack's `reads-host` contribution, gated on the pack's content ORIGIN —
// embedded packs ship with yolo and carry its authority, a fetched pack is refused
// outright (packload.HonoredHostFiles, run/packhostgrants.go). Config reads into
// neither, which is the property this test pins.
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

// agentsErrs runs ValidateConfig over a config carrying only `agents`, on the HOST
// (YOLO_VERSION cleared), and returns just the config.agents errors.
func agentsErrs(t *testing.T, body string) []string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"agents": `+body+`}`), t.TempDir(), nil)
	return withPrefix(errs, "config.agents")
}

// agentsInJail is agentsErrs' in-jail twin, returning (errors, warnings).
func agentsInJail(t *testing.T, body string) ([]string, []string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "test")
	errs, warns := ValidateConfig(decode(t, `{"agents": `+body+`}`), t.TempDir(), nil)
	return withPrefix(errs, "config.agents"), withPrefix(warns, "config.agents")
}

func withPrefix(msgs []string, prefix string) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m, prefix) {
			out = append(out, m)
		}
	}
	return out
}

// The `agents` key is DELETED, so a config carrying it must be REJECTED on the host —
// not ignored, and not quietly accepted. Ignoring it would be the worst outcome
// available: the user asked for claude, yolo silently gives them nothing, and
// nothing in the jail can explain why.
// Every shape the key ever took — a valid list, an empty one, null, a bare string, a
// list naming the long-retired `gemini` — must land on the IDENTICAL verdict, because
// the key is gone and its CONTENTS are no longer a question yolo answers. Asserting
// byte-equality across shapes rather than merely "each is rejected" is what pins that:
// a reintroduced per-shape branch (a different message for `[]`, say, or the old
// unknown-agent typo check firing on `["gemini"]`) would otherwise pass unnoticed.
func TestValidateAgentsKeyIsRejected(t *testing.T) {
	shapes := []string{`["claude"]`, `[]`, `null`, `"claude"`, `["gemini"]`}
	var first []string
	for i, body := range shapes {
		errs := agentsErrs(t, body)
		if len(errs) == 0 {
			t.Errorf("agents=%s: want a rejection, got none", body)
			continue
		}
		if i == 0 {
			first = errs
			continue
		}
		if strings.Join(errs, "\n") != strings.Join(first, "\n") {
			t.Errorf("agents=%s produced a shape-specific verdict:\n got %v\nwant %v",
				body, errs, first)
		}
	}
}

// INSIDE a jail the same key is a WARNING, not an error, and this is the regression for
// a real wedge: LoadConfig prefers the host-generated, gitignored config snapshot in a
// jail, so a snapshot still carrying `agents` made every nested launch refuse to start
// over a key the in-jail user cannot fix at its source. Worse, `yolo check` — the command
// the error text tells you to run — merges the user and workspace files directly and
// never reads the snapshot, so it reported that same config "semantically valid".
// Reproduced by hand before the fix: `yolo --dry-run -- true` printed "Invalid jail
// config" while `yolo check --no-build` passed on the identical fixture.
//
// It must still be REPORTED in-jail. Dropping it entirely would hide a real stale
// snapshot; the warning names the host config as the place to remove it.
func TestValidateAgentsIsAWarningInJail(t *testing.T) {
	errs, warns := agentsInJail(t, `["claude"]`)
	if len(errs) != 0 {
		t.Errorf("in a jail the key must not be a hard error — the config is the "+
			"host-written snapshot, so this refuses every nested launch: %v", errs)
	}
	if len(warns) != 1 {
		t.Fatalf("want exactly one config.agents warning in a jail, got %d: %v",
			len(warns), warns)
	}
	if !strings.Contains(warns[0], "REMOVED") || !strings.Contains(warns[0], "HOST") {
		t.Errorf("the in-jail warning must say the key is removed AND that the fix is on "+
			"the host (the snapshot is generated, not authored): %q", warns[0])
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

// SelectedAgents is a shim that returns the EMPTY set now that there is no key to read.
// The load-bearing assertion is EMPTINESS, and that it holds for EVERY input — including
// a config that still names agents, which must not be honored just because it parses.
//
// The non-nil check is kept but is no longer load-bearing, and this comment says so on
// purpose: it used to guard ResolveAgents' nil -> DefaultAgents fallback, which is gone,
// and every YOLO_AGENTS encoder builds its list with make(…, len(x)) so nil already
// serializes as `[]`. Left as a cheap shape pin, not as a hazard guard — a reader who
// finds it should not conclude the old fallback is still out there.
func TestSelectedAgentsIsEmptyAndNonNil(t *testing.T) {
	for _, src := range []string{`{}`, `{"agents": ["claude"]}`, `{"agents": []}`} {
		got := SelectedAgents(decode(t, src))
		if len(got) != 0 {
			t.Errorf("SelectedAgents(%s) = %v, want empty — a config naming agents must "+
				"not be honored", src, got)
		}
		if got == nil {
			t.Errorf("SelectedAgents(%s) returned nil, want a non-nil empty slice", src)
		}
	}
}

// The `agents` key was overrideListKeys' only member, and its replace-wholesale
// merge is what let a repo-committed, agent-editable workspace config decide agent
// selection — and through it which host files mounted (the hole validateAgentsScope
// existed to plug). With the key gone the exception has no members, so EVERY list
// key union-merges, and the mechanism went with it rather than sitting inert waiting
// to reopen that class of hole for whatever key got added next.
//
// This drives EVERY top-level key rather than a hand-picked pair, because the claim is
// universal and a two-key version does not pin it: `packages` and `mcp_presets` union
// both before and after the removal, so a resurrected override-list carrying some OTHER
// key would leave such a test green. Any key excluded from the union is a
// replace-wholesale exception, which is what must not exist.
func TestMergeConfigUnionsEveryListKey(t *testing.T) {
	for key := range knownTopLevelConfigKeys {
		base := decode(t, `{"`+key+`": ["a"]}`)
		over := decode(t, `{"`+key+`": ["b"]}`)
		v, _ := MergeConfig(base, over).Get(key)
		got, _ := v.([]any)
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("%s = %v, want [a b] — a list key that does not union is a "+
				"replace-wholesale exception, and overrideListKeys was deleted so that "+
				"no such exception exists", key, got)
		}
	}
}

// Phase 2 (env-manager): the confinement dial. Validation accepts the three notches
// and rejects anything else; ResolveConfinement defaults to jail and never fails.
func TestConfinementValidateAndResolve(t *testing.T) {
	for _, ok := range []string{"jail", "guest", "host"} {
		errs, _ := ValidateConfig(decode(t, `{"confinement":"`+ok+`"}`), t.TempDir(), nil)
		for _, e := range errs {
			if strings.HasPrefix(e, "config.confinement") {
				t.Errorf("confinement %q should validate, got: %s", ok, e)
			}
		}
		if got := ResolveConfinement(decode(t, `{"confinement":"`+ok+`"}`)); string(got) != ok {
			t.Errorf("ResolveConfinement(%q) = %q", ok, got)
		}
	}

	// An unknown value is a validation error...
	errs, _ := ValidateConfig(decode(t, `{"confinement":"sandbox"}`), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e, "config.confinement") {
			found = true
		}
	}
	if !found {
		t.Error("unknown confinement value must be a validation error")
	}
	// ...but the resolver never fails — it defaults to the strongest notch, so `host`
	// is never reached by a typo (env-manager §7: host is explicit-only).
	if got := ResolveConfinement(decode(t, `{"confinement":"sandbox"}`)); got != ConfinementJail {
		t.Errorf("unknown confinement should resolve to jail (never host), got %q", got)
	}
	// Absent key defaults to jail — the behavior-neutral default.
	if got := ResolveConfinement(decode(t, `{}`)); got != ConfinementJail {
		t.Errorf("absent confinement should default to jail, got %q", got)
	}
}

func TestValidateProviders(t *testing.T) {
	valid := `{"providers": {
		"glm": {
			"base_url": "https://open.bigmodel.cn/api/paas/v4",
			"wire_api": "openai-chat",
			"api_key_env_name": "GLM_API_KEY",
			"models": {"default": "glm-4-plus", "fast": "glm-4-flash"},
			"capabilities": ["code_editing"]
		},
		"bedrock": {
			"wire_api": "anthropic",
			"region": "us-east-1",
			"models": {"default": "us.anthropic.claude-opus-5[1m]"}
		},
		"zai": {
			"api_key_env_name": "ZAI_API_KEY",
			"endpoints": {
				"anthropic": {"base_url": "https://api.z.ai/api/anthropic"},
				"openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat"}
			}
		},
		"named_only": {},
		"disabled_provider": null
	}}`
	errs, _ := ValidateConfig(decode(t, valid), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.providers") {
			t.Errorf("valid providers should pass validation, got error: %s", e)
		}
	}

	invalid := `{"providers": {
		"bad": {
			"base_url": 123,
			"api_key_env_name": "123-bad-name",
			"models": "not-an-object",
			"unknown_key": "xyz"
		}
	}}`
	errs, _ = ValidateConfig(decode(t, invalid), t.TempDir(), nil)
	var provErrs []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.providers") {
			provErrs = append(provErrs, e)
		}
	}
	if len(provErrs) < 3 {
		t.Errorf("expected multiple provider validation errors, got: %v", provErrs)
	}
	named := false
	for _, e := range provErrs {
		// The message names the key it is about, not the one it replaced — a user
		// who wrote the new spelling must be able to find their own line in the error.
		if strings.Contains(e, ".api_key_env_name: invalid env var name") {
			named = true
		}
	}
	if !named {
		t.Errorf("expected an api_key_env_name error naming the new key, got: %v", provErrs)
	}
}

// providerErrors runs ValidateConfig over a providers block and returns only the
// config.providers.* diagnostics.
func providerErrors(t *testing.T, body string) []string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "") // the host view: the retirement checks differ in-jail
	errs, _ := ValidateConfig(decode(t, `{"providers": `+body+`}`), t.TempDir(), nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.providers") {
			out = append(out, e)
		}
	}
	return out
}

// wire_api reaches three agents' config files verbatim, so a typo here is a protocol
// error the AGENT reports, later. The vocabulary is closed for the reason Rule 4 closes
// any fixed syntactic slot (profiles-as-pack-variants.md §4.3), and the set itself is
// packdecl's — the layer a manifest declares the same field in.
func TestValidateProvidersWireAPIIsAClosedEnum(t *testing.T) {
	for _, api := range packdecl.KnownWireAPIs() {
		errs := providerErrors(t, `{"glm": {"wire_api": "`+api+`"}}`)
		if len(errs) != 0 {
			t.Errorf("wire_api %q is in the vocabulary, got error: %v", api, errs)
		}
	}
	errs := providerErrors(t, `{"glm": {"wire_api": "totally-not-a-wire-api"}}`)
	if len(errs) != 1 {
		t.Fatalf("want exactly one wire_api error, got: %v", errs)
	}
	for _, api := range packdecl.KnownWireAPIs() {
		if !strings.Contains(errs[0], api) {
			t.Errorf("the error must name the vocabulary it wants, missing %q: %s", api, errs[0])
		}
	}
	if errs := providerErrors(t, `{"glm": {"wire_api": 4}}`); len(errs) != 1 {
		t.Errorf("a non-string wire_api is still one error, got: %v", errs)
	}
}

// TestWireAPIEnumIsPackdeclsSet pins the ONE-SET claim from the config side:
// validateWireAPI asks packdecl for the enum (KnownWireAPIs), so a wire_api a manifest
// may declare is exactly the wire_api a user may write — the two validators cannot
// disagree about what a provider is. The sweep fails the day a fifth value reaches one
// layer without the other, which each layer's own closed-enum test above would survive:
// both would still be self-consistent, and a manifest would validate for an author whose
// config the same value refused.
func TestWireAPIEnumIsPackdeclsSet(t *testing.T) {
	probes := append(append([]string{}, packdecl.KnownWireAPIs()...),
		"", "openai-chatt", "Anthropic", "chat")
	for _, v := range probes {
		t.Run("value="+v, func(t *testing.T) {
			errs := providerErrors(t, `{"glm": {"wire_api": "`+v+`"}}`)
			accepted := len(errs) == 0
			if accepted != packdecl.KnownWireAPI(v) {
				t.Errorf("config accepts wire_api %q = %v, packdecl.KnownWireAPI = %v — the "+
					"enum has drifted between the config layer and the manifest layer", v, accepted,
					packdecl.KnownWireAPI(v))
			}
		})
	}
}

// `https://user:tok@host/v1` is a credential in a git-tracked config file, and this rule
// is the check (profiles-as-pack-variants.md §4.3): base_url routes an ADDRESS, and the
// credential travels by NAME through api_key_env_name.
func TestValidateProvidersBaseURLMustBeAnAddress(t *testing.T) {
	for _, u := range []string{
		"http://host.example/v1",
		"https://host.example/v1",
		"https://open.bigmodel.cn/api/paas/v4",
	} {
		if errs := providerErrors(t, `{"glm": {"base_url": "`+u+`"}}`); len(errs) != 0 {
			t.Errorf("base_url %q is a usable address, got error: %v", u, errs)
		}
	}
	for _, u := range []string{
		"ftp://host.example/v1",            // not a wire protocol yolo can speak
		"file:///etc/hosts",                // a fact about this machine, not a service
		"host.example/v1",                  // no scheme at all
		"https://user:tok@host.example/v1", // the credential the rule exists for
		"https://user@host.example/v1",     // userinfo without a password counts too
	} {
		errs := providerErrors(t, `{"glm": {"base_url": "`+u+`"}}`)
		if len(errs) != 1 {
			t.Errorf("base_url %q should be refused once, got: %v", u, errs)
			continue
		}
		if strings.Contains(errs[0], "expected a string") {
			t.Errorf("the refusal should say WHY, not the type: %s", errs[0])
		}
	}
}

// The §5 endpoint map: per-protocol objects whose contents are schema like any other.
func TestValidateProvidersEndpoints(t *testing.T) {
	valid := `{"zai": {"endpoints": {
		"anthropic": {"base_url": "https://api.z.ai/api/anthropic"},
		"openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat"}
	}}}`
	if errs := providerErrors(t, valid); len(errs) != 0 {
		t.Errorf("valid endpoints should pass, got: %v", errs)
	}

	for _, body := range []string{
		// Not a map of objects.
		`{"zai": {"endpoints": "https://api.z.ai/api/paas/v4"}}`,
		`{"zai": {"endpoints": {"openai": "https://api.z.ai/api/paas/v4"}}}`,
		// The URL rule and the wire vocabulary hold one level down too.
		`{"zai": {"endpoints": {"openai": {"base_url": "https://u:pw@api.z.ai/v4"}}}}`,
		`{"zai": {"endpoints": {"openai": {"base_url": 4}}}}`,
		`{"zai": {"endpoints": {"openai": {"base_url": "https://api.z.ai/v4", "wire_api": "chat"}}}}`,
		// An unknown key inside an endpoint is refused, not inherited.
		`{"zai": {"endpoints": {"openai": {"base_url": "https://api.z.ai/v4", "models": {}}}}}`,
	} {
		if errs := providerErrors(t, body); len(errs) == 0 {
			t.Errorf("expected a refusal for %s", body)
		}
	}
}

// OQ-15 (profiles-as-pack-variants.md §14): `env_shape` is a service fact like
// endpoints and models, so user config may override it — the validator refusing the key
// while ComposeProviders merged it was a vocabulary disagreement, not a rule. The
// PLACEHOLDER vocabulary stays closed, and it is packdecl's own check that enforces it
// here, so a user-written shape and a pack-shipped one are judged by the same function
// and cannot drift.
func TestValidateProvidersEnvShape(t *testing.T) {
	valid := `{"bedrock": {"env_shape": {"anthropic": {
		"AWS_REGION": "{region}",
		"ANTHROPIC_BASE_URL": "{endpoint}",
		"ANTHROPIC_AUTH_TOKEN": "{key}",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "{model:sonnet}"
	}}}}`
	if errs := providerErrors(t, valid); len(errs) != 0 {
		t.Errorf("every legal placeholder should pass, got: %v", errs)
	}

	// The refusal names the var that carries the bad template and the allowed set —
	// the user has to be able to fix the line from the message alone.
	errs := providerErrors(t, `{"bedrock": {"env_shape": {"anthropic": {
		"ANTHROPIC_AUTH_TOKEN": "Bearer {key}"
	}}}}`)
	if len(errs) != 1 {
		t.Fatalf("want exactly one placeholder refusal, got: %v", errs)
	}
	for _, want := range []string{"ANTHROPIC_AUTH_TOKEN", `{endpoint}`, `{key}`, `{region}`, `{model:<alias>}`} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("refusal should name %s: %s", want, errs[0])
		}
	}

	// A literal is the same refusal: there is no static form in a shape, only
	// placeholders resolved from the provider's own entry (§4.2's escape hatch is a
	// profile's `env`, not this field).
	if errs := providerErrors(t, `{"zai": {"env_shape": {"openai": {
		"OPENAI_BASE_URL": "https://api.z.ai/api/paas/v4"}}}}`); len(errs) != 1 {
		t.Errorf("a literal value is a template the composer silently drops, want a refusal, got: %v", errs)
	}

	// Structure is schema, like endpoints one level up.
	for _, body := range []string{
		`{"zai": {"env_shape": "https://api.z.ai/api/paas/v4"}}`,
		`{"zai": {"env_shape": {"openai": "https://api.z.ai/api/paas/v4"}}}`,
		`{"zai": {"env_shape": {"openai": {"OPENAI_BASE_URL": 4}}}}`,
	} {
		if errs := providerErrors(t, body); len(errs) == 0 {
			t.Errorf("expected a refusal for %s", body)
		}
	}

	// Null is "absent", the convention every other provider key follows.
	if errs := providerErrors(t, `{"zai": {"env_shape": null}}`); len(errs) != 0 {
		t.Errorf("a null env_shape should be inert, got: %v", errs)
	}
}

// The override is the user's own, so the key must be IN the census — an env_shape that
// passed the placeholder check but died on "unknown key" would be the disagreement
// OQ-15 records, one spelling of it further down.
func TestValidateProvidersEnvShapeIsAKnownKey(t *testing.T) {
	if _, known := knownProviderKeys["env_shape"]; !known {
		t.Error("`env_shape` must be in knownProviderKeys, or a valid user shape is " +
			"refused as an unknown key after passing the placeholder check")
	}
}

// Closure rule 1 (zai OQ-Z6): base_url is the single-protocol shorthand and is valid
// ONLY alone. Carrying both is an ambiguity, and the refusal names `endpoints` because
// that is where the URL belongs.
func TestValidateProvidersBaseURLAndEndpointsTogetherIsRefused(t *testing.T) {
	errs := providerErrors(t, `{"zai": {
		"base_url": "https://api.z.ai/api/paas/v4",
		"endpoints": {"openai": {"base_url": "https://api.z.ai/api/paas/v4"}}
	}}`)
	if len(errs) != 1 {
		t.Fatalf("want exactly one coexistence error, got: %v", errs)
	}
	if !strings.Contains(errs[0], "endpoints") {
		t.Errorf("the coexistence refusal must point at endpoints: %s", errs[0])
	}
}

// ...and the other half of rule 1: a provider that is a NAME only — the thing a
// requires_provider assertion or a profile selection points at — is not an error.
func TestValidateProvidersWithNoAddressIsLegal(t *testing.T) {
	if errs := providerErrors(t, `{"bedrock": {"region": "us-east-1"}}`); len(errs) != 0 {
		t.Errorf("a provider that exists to be named is legal, got: %v", errs)
	}
}

// The old credential-pointer spelling is refused BY NAME — and that refusal is the
// ONLY error it produces, so the key stays in knownProviderKeys and the generic
// unknown-key error never duplicates it. TestValidateAgentProfilesRetiredIsTheOnlyError,
// one nesting level down.
func TestValidateProvidersAPIKeyEnvRetiredIsTheOnlyError(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // the host view: in-jail the rename downgrades to a warning
	if _, known := knownProviderKeys["api_key_env"]; !known {
		t.Error("`api_key_env` must stay in knownProviderKeys so the rename message is " +
			"the ONLY error — otherwise the generic unknown-key error duplicates it")
	}
	errs, warns := ValidateConfig(decode(t,
		`{"providers": {"zai": {"api_key_env": "ZAI_API_KEY"}}}`), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Fatalf("want exactly one rename error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "api_key_env_name") {
		t.Errorf("the rename message must name the replacement spelling: %v", errs)
	}
	if len(warns) != 0 {
		t.Errorf("the host refusal is an error, not a warning: %v", warns)
	}
}

// A jail's config is the host-generated snapshot, so one written by an older launcher
// legitimately carries the old spelling — the same downgrade every retired key gets.
func TestValidateProvidersAPIKeyEnvRetiredWarnsInJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.0.0-dev") // in-jail
	errs, warns := ValidateConfig(decode(t,
		`{"providers": {"zai": {"api_key_env": "ZAI_API_KEY"}}}`), t.TempDir(), nil)
	if len(errs) != 0 {
		t.Errorf("in-jail the rename is a warning, not an error: %v", errs)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "api_key_env_name") {
		t.Errorf("want one rename warning naming the replacement spelling, got: %v", warns)
	}
}

// The rename must not conflate "absent" with "present" — the twin of
// TestValidateAgentProfilesAbsentIsClean.
func TestValidateProvidersAPIKeyEnvAbsentIsClean(t *testing.T) {
	errs, _ := ValidateConfig(decode(t,
		`{"providers": {"zai": {"base_url": "https://api.z.ai/api/paas/v4"}}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "api_key_env") {
			t.Errorf("a provider with no credential pointer must not error, got: %s", e)
		}
	}
}

func TestValidatePackProfiles(t *testing.T) {
	valid := `{"pack_profiles": {"claude": "bedrock", "pi": "glm", "codex": "default"}}`
	errs, _ := ValidateConfig(decode(t, valid), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.pack_profiles") {
			t.Errorf("valid pack_profiles should pass validation, got: %s", e)
		}
	}

	invalid := `{"pack_profiles": {"pi": 123}}`
	errs, _ = ValidateConfig(decode(t, invalid), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "config.pack_profiles.pi") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected validation error for non-string profile, got: %v", errs)
	}
}

// The old spelling is refused BY NAME — and that refusal is the ONLY error it
// produces, so the key must stay in knownTopLevelConfigKeys (the generic
// unknown-key error would duplicate it). The agents-retirement twins.
func TestValidateAgentProfilesRetiredIsTheOnlyError(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // the host view: in-jail the retirement downgrades to a warning
	if _, known := knownTopLevelConfigKeys["agent_profiles"]; !known {
		t.Error("`agent_profiles` must stay in knownTopLevelConfigKeys so the retirement " +
			"message is the ONLY error — otherwise the generic unknown-key error " +
			"duplicates it")
	}
	errs, _ := ValidateConfig(decode(t, `{"agent_profiles": {"claude": "bedrock"}}`), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Errorf("want exactly one config.agent_profiles error, got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if e == "config.agent_profiles: unknown key" {
			t.Errorf("the bare unknown-key error duplicates the retirement message: %v", errs)
		}
		if !strings.Contains(e, "pack_profiles") {
			t.Errorf("the retirement message must name the replacement spelling: %v", errs)
		}
	}
}

// A config with NO `agent_profiles` key must stay clean — the regression if the
// retirement check ever conflates "absent" with "present".
func TestValidateAgentProfilesAbsentIsClean(t *testing.T) {
	errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.agent_profiles") {
			t.Errorf("absent retired key must not error, got: %s", e)
		}
	}
}

func TestValidateRequiredCapabilities(t *testing.T) {
	valid := `{"required_capabilities": ["code_editing", "web_search"]}`
	errs, _ := ValidateConfig(decode(t, valid), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.required_capabilities") {
			t.Errorf("valid required_capabilities should pass, got: %s", e)
		}
	}

	invalid := `{"required_capabilities": "not-a-list"}`
	errs, _ = ValidateConfig(decode(t, invalid), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e, "config.required_capabilities") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error for non-list required_capabilities, got: %v", errs)
	}
}

func TestValidateMCPServersProvidesCollision(t *testing.T) {
	valid := `{"mcp_servers": {
		"tavily": {"command": "npx", "provides": "web_search"},
		"github": {"command": "npx", "provides": "git_issues"}
	}}`
	errs, _ := ValidateConfig(decode(t, valid), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.mcp_servers") {
			t.Errorf("valid mcp_servers should pass, got: %s", e)
		}
	}

	collision := `{"mcp_servers": {
		"tavily": {"command": "npx", "provides": "web_search"},
		"brave": {"command": "npx", "provides": "web_search"}
	}}`
	errs, _ = ValidateConfig(decode(t, collision), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "multiple servers declare provides \"web_search\"") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected multiple provides collision error, got: %v", errs)
	}
}

// TestBlockedToolsNamesMustBeBare: a blocked tool's name is a FILENAME in the generated
// block dir (filepath.Join(BlockDir, name)), and blocked_tools reaches the entrypoint from
// the assembled config whose workspace half is agent-editable — so a name carrying ".."
// would write an executable outside the anchor into the jail's persistent home. Both the
// string and object spellings refuse it here, at the config gate, before any writer runs.
func TestBlockedToolsNamesMustBeBare(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	security := jsonx.NewOrderedMap()
	obj := jsonx.NewOrderedMap()
	obj.Set("name", "sub/../../pwn")
	security.Set("blocked_tools", []any{"../../.bashrc", obj})
	cfg.Set("security", security)

	var errs []string
	validateSecurity(cfg, &errs)
	if len(errs) != 2 {
		t.Fatalf("errs = %v, want one refusal per spelling", errs)
	}
	for _, e := range errs {
		if !strings.Contains(e, "bare tool name") {
			t.Errorf("refusal should name the rule: %s", e)
		}
	}
}
