package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestWriteUserEnvFileBytes(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("FOO", "bar")
	env.Set("QUOTED", "it's a 'test'")
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	writeUserEnvFile(p, env, nil)
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Auto-generated from yolo-jail.jsonc env config.\n" +
		"# Override by editing this file or workspace .env (mise).\n" +
		"export FOO=${FOO:-'bar'}\n" +
		`export QUOTED=${QUOTED:-'it'\''s a '\''test'\'''}` + "\n"
	if string(got) != want {
		t.Errorf("yolo-user-env.sh bytes:\n got: %q\nwant: %q", got, want)
	}
}

func TestWriteUserEnvFileEmptyTouches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	writeUserEnvFile(p, jsonx.NewOrderedMap(), nil)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("empty env should touch an empty file, size=%d", info.Size())
	}
}

// TestWriteUserEnvFileEmptyClearsStaleRender is the regression for creds that
// outlive their config. Dropping env_sources makes ResolveEnvSources return an
// empty map, which used to no-op on an ALREADY-EXISTING file (touchFile returns
// early when the path is there) — so the last populated render stayed mounted at
// ~/.config/yolo-user-env.sh and kept exporting AWS keys through both
// hydrateEnvFromUserEnvFile and .bashrc, across any number of jail rebuilds.
// Removing a credential from config must revoke it, so the empty case truncates.
func TestWriteUserEnvFileEmptyClearsStaleRender(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")

	// A previous launch rendered real credentials into the file.
	populated := jsonx.NewOrderedMap()
	populated.Set("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
	writeUserEnvFile(p, populated, nil)

	// This launch has env_sources commented out.
	writeUserEnvFile(p, jsonx.NewOrderedMap(), nil)

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("file must still exist (the bind-mount source): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("stale render survived an empty env: %q", got)
	}
}

// The file holds hydrated env_sources values — API keys in practice — so it is
// owner-only. It was 0644 until 2026-09-01, measured in a live jail as
// `-rw-r--r--` carrying two provider keys, while packs/zai's README tells the user
// to keep that key in a file that is "untracked, 0600": yolo's own copy was
// downgrading the mode the user chose.
func TestWriteUserEnvFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "yolo-user-env.sh")

	env := jsonx.NewOrderedMap()
	env.Set("ZAI_API_KEY", "sk-secret")
	writeUserEnvFile(f, env, nil)
	st, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("a file holding a plaintext credential must be 0600, got %04o", got)
	}

	// The EMPTY path carries the mode too: dropping env_sources truncates rather
	// than removing, and a truncation that widened the mode back would undo this on
	// the next launch.
	writeUserEnvFile(f, jsonx.NewOrderedMap(), nil)
	st, err = os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("the truncating path must keep 0600, got %04o", got)
	}
}

// os.WriteFile applies its mode only when CREATING, so a file an older yolo left
// at 0644 would keep it forever without an explicit chmod. Every launch is the
// migration, and this is the test that fails if the chmod is dropped.
func TestWriteUserEnvFileNarrowsAnExistingWideFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "yolo-user-env.sh")
	if err := os.WriteFile(f, []byte("# left by an older yolo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := jsonx.NewOrderedMap()
	env.Set("ZAI_API_KEY", "sk-secret")
	writeUserEnvFile(f, env, nil)
	st, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("an existing 0644 file must be narrowed in place, got %04o", got)
	}
}

// testChannel builds the smallest channel the writer consumes, in the shape
// composePackChannel produces. The JSON table values are asserted with Contains
// (their exact bytes are jsonx.DumpsCompact's business); the LINE grammar and the
// ORDER are this file's frozen contract, and those are asserted exactly.
func testChannel() *packChannel {
	providers := jsonx.NewOrderedMap()
	zai := jsonx.NewOrderedMap()
	zai.Set("api_key_env_name", "ZAI_API_KEY")
	providers.Set("zai", zai)
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "zai")
	return &packChannel{
		profiles:  profiles,
		providers: providers,
		packEnv:   map[string]string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"},
		shapeVars: []agentenv.Var{
			{Key: "ANTHROPIC_BASE_URL", Value: "https://api.z.ai/api/anthropic"},
			{Key: "ANTHROPIC_AUTH_TOKEN", Value: "sk-it's"},
		},
	}
}

// TestWriteUserEnvFileChannelSection pins the per-entry channel's landing: the
// same file, a marked section, and a DIFFERENT export grammar — plain
// `export K='v'` (unconditional) for the channel beside `export K=${K:-'v'}`
// (overridable default) for env_sources. The grammar is the precedence: bash
// sourcing and hydrateEnvFromUserEnvFile both read "plain form beats the
// environment, def form does not" off the line itself, which is what makes the
// file a per-ENTRY delivery rather than a second frozen copy.
func TestWriteUserEnvFileChannelSection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	env := jsonx.NewOrderedMap()
	env.Set("ZAI_API_KEY", "sk-secret")
	writeUserEnvFile(p, env, testChannel())
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	// env_sources half unchanged: def-form, overridable.
	if !strings.Contains(s, "export ZAI_API_KEY=${ZAI_API_KEY:-'sk-secret'}\n") {
		t.Errorf("env_sources line must stay def-form:\n%s", s)
	}
	// The three wire tables, in the frozen order, plain-form.
	for _, k := range []string{"YOLO_PROVIDERS", "YOLO_PROFILES", "YOLO_USE_PROFILES"} {
		if !strings.Contains(s, "export "+k+"='") {
			t.Errorf("channel section must export %s plain-form:\n%s", k, s)
		}
	}
	if strings.Index(s, "YOLO_PROVIDERS='") > strings.Index(s, "YOLO_PROFILES='") ||
		strings.Index(s, "YOLO_PROFILES='") > strings.Index(s, "YOLO_USE_PROFILES='") {
		t.Errorf("wire tables out of order:\n%s", s)
	}
	if !strings.Contains(s, "YOLO_USE_PROFILES='{\"claude\": \"zai\"}'\n") {
		t.Errorf("effective table value wrong:\n%s", s)
	}
	// packEnv (sorted) then shapeVars (channel order), both plain-form, after the tables.
	if !strings.Contains(s, "export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1'\n") {
		t.Errorf("packEnv line missing:\n%s", s)
	}
	if !strings.Contains(s, "export ANTHROPIC_BASE_URL='https://api.z.ai/api/anthropic'\n") {
		t.Errorf("shape var line missing:\n%s", s)
	}
	// Single quotes escape the same way in both grammars.
	if !strings.Contains(s, `export ANTHROPIC_AUTH_TOKEN='sk-it'\''s'`+"\n") {
		t.Errorf("shape var quoting wrong:\n%s", s)
	}
	if strings.Index(s, "YOLO_USE_PROFILES='") > strings.Index(s, "ANTHROPIC_BASE_URL='") {
		t.Errorf("packEnv/shapeVars must follow the tables:\n%s", s)
	}
	// The channel section is marked, so a reader can tell composed lines from
	// overridable ones without parsing every export.
	if !strings.Contains(s, channelSectionHeader) {
		t.Errorf("channel section header missing:\n%s", s)
	}
}

// TestWriteUserEnvFileChannelRevokesOnRemoval is the channel twin of
// TestWriteUserEnvFileEmptyClearsStaleRender: the file is rewritten whole by every
// entry, so a session that selects no profile carries none of the previous entry's
// provider environment. The three tables stay (they cross on every entry, `{}` is
// a meaningful "none"); the pack env and shape vars must be gone.
func TestWriteUserEnvFileChannelRevokesOnRemoval(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	writeUserEnvFile(p, jsonx.NewOrderedMap(), testChannel())
	// Next entry: no profiles active, no pack env, no shape vars.
	writeUserEnvFile(p, jsonx.NewOrderedMap(), &packChannel{
		profiles:  jsonx.NewOrderedMap(),
		providers: jsonx.NewOrderedMap(),
	})
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, gone := range []string{"ANTHROPIC_", "CLAUDE_CODE_DISABLE", "claude"} {
		if strings.Contains(s, gone) {
			t.Errorf("previous entry's channel survived the rewrite:\n%s", s)
		}
	}
	for _, k := range []string{"YOLO_PROVIDERS", "YOLO_PROFILES", "YOLO_USE_PROFILES"} {
		if !strings.Contains(s, "export "+k+"='{}'\n") {
			t.Errorf("table %s must still cross, empty:\n%s", k, s)
		}
	}
}

// A nil channel writes no channel section at all — the pre-channel spelling, which
// an empty env_sources must still be able to truncate to zero bytes (the
// bind-mount source contract in TestWriteUserEnvFileEmptyClearsStaleRender).
func TestWriteUserEnvFileNilChannelTruncates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "yolo-user-env.sh")
	writeUserEnvFile(p, jsonx.NewOrderedMap(), testChannel())
	writeUserEnvFile(p, jsonx.NewOrderedMap(), nil)
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("nil channel + empty env must truncate to zero bytes, got:\n%s", got)
	}
}
