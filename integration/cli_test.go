package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shim-behavior and host-CLI-surface tests. Every test drives the real `yolo`
// binary against a real runtime, so each calls requireJail(t) first — that also
// gates them out of `go test -short`, where TestMain never builds yoloBin.
//
// Assertions were re-derived from what the Go implementation actually emits (the
// entrypoint shim generator and the host CLI), not copied from the Python era;
// where a message string differs from the old test, the expectation was fixed,
// never the implementation.

// TestBlockedTools confirms the entrypoint's blocked-tool shims in ONE jail
// launch (the three checks share the tempProject fixture — curl blocked with the
// DEFAULT message, grep blocked with a CUSTOM message, everything else allowed —
// so they exercise the identical image; merged to pay the container cold-start
// once instead of three times). Each command runs independently (NO `set -e`),
// tagging its own exit code so per-check assertions are preserved:
//
//   - curl  → rc 127 + default "Error: tool curl is blocked" (no custom message).
//   - grep  → rc 127 + the fixture's custom "NO GREP ALLOWED" (the exhaustive
//     block-only-recursive matrix lives in internal/entrypoint/shims_behavior_test.go;
//     here we only confirm the wiring fires + surfaces the custom text).
//   - ls    → rc 0 + /workspace (an unblocked tool runs normally).
func TestBlockedTools(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir, strings.Join([]string{
		`curl --version; echo "CURL rc=$?"`,
		`grep -r 'foo' .; echo "GREP rc=$?"`,
		`ls -d /workspace; echo "LS rc=$?"`,
	}, "\n"))
	out := r.combined() // block messages go to stderr, ls to stdout; check both.

	// curl: default blocked-tool message + rc 127.
	if !strings.Contains(out, "CURL rc=127") {
		t.Fatalf("expected curl blocked (rc 127), got:\n%s", out)
	}
	if !strings.Contains(out, "Error: tool curl is blocked") {
		t.Fatalf("expected default blocked-tool message for curl, got:\n%s", out)
	}
	// grep: custom message + rc 127.
	if !strings.Contains(out, "GREP rc=127") {
		t.Fatalf("expected grep blocked (rc 127), got:\n%s", out)
	}
	if !strings.Contains(out, "NO GREP ALLOWED") {
		t.Fatalf("expected custom grep message, got:\n%s", out)
	}
	// ls: allowed, rc 0, prints the workspace path.
	if !strings.Contains(out, "LS rc=0") {
		t.Fatalf("expected ls allowed (rc 0), got:\n%s", out)
	}
	if !strings.Contains(r.stdout, "/workspace") {
		t.Fatalf("expected /workspace in stdout, got:\n%s", r.stdout)
	}
}

// TestShimPersistence confirms a shim does not survive its removal from config.
// The container uses --rm, so the second run is a fresh launch; the non-TTY
// config-change path auto-accepts (config.CheckConfigChanges), regenerating shims
// from the new config, so unblocking curl takes effect.
func TestShimPersistence(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"security": {"blocked_tools": ["curl"]}}`)
	cfgPath := filepath.Join(dir, "yolo-jail.jsonc")

	// 1. curl is blocked.
	r := runYolo(t, dir, "curl --version")
	if r.rc != 127 {
		t.Fatalf("expected curl blocked (rc 127), got %d\n%s", r.rc, r.combined())
	}

	// 2. Unblock curl in the same workspace and rerun.
	if err := os.WriteFile(cfgPath, []byte(`{"security": {"blocked_tools": []}}`), 0o644); err != nil {
		t.Fatalf("rewriting config: %v", err)
	}
	r = runYolo(t, dir, "curl --version")
	if r.rc != 0 {
		t.Fatalf("expected curl allowed after unblock (rc 0), got %d\n%s", r.rc, r.combined())
	}
}

// TestYoloInit confirms `yolo init` scaffolds yolo-jail.jsonc in an empty dir.
func TestYoloInit(t *testing.T) {
	requireJail(t)
	dir := t.TempDir()
	r := runYoloCLI(t, dir, "init")
	if r.rc != 0 {
		t.Fatalf("expected rc 0 from yolo init, got %d\n%s", r.rc, r.combined())
	}
	if _, err := os.Stat(filepath.Join(dir, "yolo-jail.jsonc")); err != nil {
		t.Fatalf("yolo init did not create yolo-jail.jsonc: %v", err)
	}
}

// TestYoloCheckValidConfig confirms host-side `yolo check --no-build` validates a
// normal config and reports success (check.go's "Merged config is semantically
// valid").
func TestYoloCheckValidConfig(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYoloCLI(t, dir, "check", "--no-build")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Merged config is semantically valid") {
		t.Fatalf("expected semantic-valid message, got:\n%s", r.combined())
	}
}

// TestYoloCheckInvalidConfigFails confirms `yolo check --no-build` fails fast on a
// bad network.mode, surfacing the config.network.mode error from validate.go.
func TestYoloCheckInvalidConfigFails(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridg"}}`)
	r := runYoloCLI(t, dir, "check", "--no-build")
	if r.rc != 1 {
		t.Fatalf("expected rc 1, got %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "config.network.mode") {
		t.Fatalf("expected config.network.mode error, got:\n%s", r.combined())
	}
}

// TestYoloRunInvalidConfigFailsBeforeStart confirms `yolo run` rejects malformed
// JSONC at preflight (load.go's "Failed to parse yolo-jail.jsonc") rather than
// silently defaulting and launching a container.
func TestYoloRunInvalidConfigFailsBeforeStart(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"security": {"blocked_tools": [}`)
	r := runYoloCLI(t, dir, "run", "--", "bash", "-lc", "true")
	if r.rc != 1 {
		t.Fatalf("expected rc 1, got %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Failed to parse yolo-jail.jsonc") {
		t.Fatalf("expected parse-failure message, got:\n%s", r.combined())
	}
}

// TestYoloDirectCommand confirms `yolo run -- <cmd>` runs a command directly (not
// wrapped in a login shell) via the explicit -- delimiter.
func TestYoloDirectCommand(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, "{}")
	r := runYoloDirect(t, dir, "ls", "-d", "/workspace")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "/workspace") {
		t.Fatalf("expected /workspace in stdout, got:\n%s", r.stdout)
	}
}

// TestYoloCheckAvailableInsideJail confirms an in-jail agent can run
// `yolo check --no-build` mid-session (the in-jail yolo shim works without the
// Python toolchain — this subsumes the dropped test_yolo_help_inside_jail).
func TestYoloCheckAvailableInsideJail(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir, "yolo check --no-build")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.stderr)
	}
	if !strings.Contains(r.stdout, "YOLO Jail Check") {
		t.Fatalf("expected 'YOLO Jail Check' banner in stdout, got:\n%s", r.stdout)
	}
}
