//go:build darwin

package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// darwinbootstrap_darwin_test.go runs the REAL macos-user bootstrap against a real
// filesystem, with JAIL_HOME redirected to a temp dir.
//
// WHY IT EXISTS. Everything this file asserts was verified by hand on a Mac in
// September 2026 and by nothing else: `integration/` has no macos-user file at all,
// so the backend's whole generation half was covered by a person running commands.
// The parts that need PRIVILEGE cannot be automated (the sudo staging, the user
// switch, Seatbelt applying) — but they are three lines of the launch, and
// everything around them takes its inputs from env and argv and needs no privilege
// whatsoever. That is what this covers.
//
// darwin-gated by build tag rather than skipped at runtime: the generators write
// platform-shaped output (BSD `stat`, /usr/bin realbins, login rc files), so a Linux
// run would assert the wrong shapes rather than the same ones.

// bootstrapEnv is the env contract the macos-user launcher bakes onto the
// self-exec argv. Mirrors macosuser.DarwinBootstrapArgv's keys; a drift between the
// two shows up here as a generator that reads nothing.
func bootstrapEnv(t *testing.T, home string, extra map[string]string) *Env {
	t.Helper()
	vars := map[string]string{
		"HOME":                   home,
		"JAIL_HOME":              home,
		"YOLO_HOST_DIR":          "/Users/Shared/yolo/proj",
		"YOLO_BLOCK_CONFIG":      `[]`,
		"YOLO_MISE_TOOLS":        `{}`,
		"YOLO_LSP_SERVERS":       `{}`,
		"YOLO_MCP_SERVERS":       `{}`,
		"YOLO_MCP_PRESETS":       `[]`,
		"YOLO_DARWIN_WORKSPACE":  "/Users/Shared/yolo/proj",
		"YOLO_DARWIN_MACOS_LOG":  "off",
		"YOLO_DARWIN_LOGIN_PATH": "/usr/bin:/bin",
	}
	for k, v := range extra {
		vars[k] = v
	}
	// The REAL translation, not a copy: DarwinEnvFrom is what the launcher's
	// dispatcher calls, so a drift between the contract and this harness is a
	// failure here rather than a divergence nobody notices.
	return DarwinEnvFrom(vars, home)
}

// The bootstrap generates a usable home: shims dir, launcher dir, the login rc files
// macOS needs (path_helper reorders PATH, so the re-prepend is what makes a declared
// tool win), and the mise config.
func TestDarwinBootstrapGeneratesAUsableHome(t *testing.T) {
	home := t.TempDir()
	e := bootstrapEnv(t, home, nil)

	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{
		MacosLog: "off", LoginPath: "/usr/bin:/bin",
	}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	for _, rel := range []string{
		".yolo/bin/block", ".yolo/bin/launch",
		".zprofile", ".zshrc", ".bashrc", ".bash_profile",
		".config/mise/config.toml",
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("bootstrap did not produce %s: %v", rel, err)
		}
	}
}

// A generated blocker must actually REFUSE when executed, with its message and its
// suggestion. Generating a file that does not run is the failure a content check
// cannot see.
func TestDarwinBootstrapBlockerActuallyRefuses(t *testing.T) {
	home := t.TempDir()
	e := bootstrapEnv(t, home, map[string]string{
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"blocked here",` +
			`"suggestion":"use rg","block_flags":["-r"]}]`,
	})
	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	shim := filepath.Join(home, ".yolo", "bin", "block", "grep")
	out, err := exec.Command(shim, "-r", "pattern", ".").CombinedOutput()
	if err == nil {
		t.Fatalf("the blocker exited 0 on a blocked invocation:\n%s", out)
	}
	if !strings.Contains(string(out), "blocked here") {
		t.Errorf("refusal did not print its message:\n%s", out)
	}
	if !strings.Contains(string(out), "use rg") {
		t.Errorf("refusal did not print its suggestion — the whole point of the block "+
			"is redirecting, not just refusing:\n%s", out)
	}

	// And it must NOT refuse a non-matching invocation: `… | grep foo` is the case
	// the flag-scoped rule exists to preserve.
	if out, err := exec.Command(shim, "pattern", os.DevNull).CombinedOutput(); err != nil {
		if strings.Contains(string(out), "blocked here") {
			t.Errorf("a non-recursive grep was refused:\n%s", out)
		}
	}
}

// The composed home overlay lands at its destinations, and REPLACES a destination
// subtree rather than merging into it — a pack that stops shipping a skills dir must
// have it disappear from the home.
func TestDarwinBootstrapInstallsTheHomeOverlay(t *testing.T) {
	home := t.TempDir()
	overlay := t.TempDir()

	if err := os.MkdirAll(filepath.Join(overlay, ".claude", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(overlay, ".claude", "skills", "demo", "SKILL.md"), "demo body")
	write(filepath.Join(overlay, ".claude", "CLAUDE.md"), "briefing body")

	// A skill from a PREVIOUS launch, whose pack has since been removed.
	write(filepath.Join(home, ".claude", "skills", "gone", "SKILL.md"), "stale")

	e := bootstrapEnv(t, home, map[string]string{"YOLO_DARWIN_HOME_OVERLAY": overlay})
	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")); err != nil {
		t.Errorf("skills did not reach the home: %v", err)
	} else if string(got) != "demo body" {
		t.Errorf("skill content = %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Errorf("the briefing did not reach the home: %v", err)
	} else if string(got) != "briefing body" {
		t.Errorf("briefing content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gone")); !os.IsNotExist(err) {
		t.Error("a removed pack's skills survived the overlay install — the destination " +
			"subtree must be REPLACED, not merged into")
	}
}

// An absent overlay is a no-op, not a boot failure: the launch may have raced a
// teardown, and the agent is better off starting with no skills than not starting.
func TestDarwinBootstrapToleratesAMissingOverlay(t *testing.T) {
	home := t.TempDir()
	var warnings strings.Builder
	e := bootstrapEnv(t, home, map[string]string{
		"YOLO_DARWIN_HOME_OVERLAY": filepath.Join(t.TempDir(), "never-staged"),
	})
	e.Stderr = &warnings

	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"}); err != nil {
		t.Fatalf("a missing overlay aborted the boot: %v", err)
	}
	if !strings.Contains(warnings.String(), "not present") {
		t.Errorf("a missing overlay was silent:\n%s", warnings.String())
	}
}
