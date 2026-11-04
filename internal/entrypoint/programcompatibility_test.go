package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// programcompatibility_test.go drives the shipped declaration through the real launcher
// generator and native launcher. The binaries are fakes: --version is the only probe and
// normal/update invocations only write a local log.
func TestShippedCopilotCompatibilityRefusesOnlyMacosUserSelf(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })

	for _, version := range []string{"1.0.93", "1.0.103", "1.0.105"} {
		for _, tc := range []struct {
			name       string
			context    string
			native     bool
			updates    string
			hatch      string
			wantRefuse bool
		}{
			{name: "self", context: `{"mode":1,"workspace":"/w","home":"/h"}`, native: true, updates: "true", wantRefuse: true},
			{name: "self-updates-disabled-use-installed", context: `{"mode":1,"workspace":"/w","home":"/h"}`, native: true, updates: "false", hatch: "1", wantRefuse: true},
			{name: "account", context: `{"mode":0,"workspace":"/w","home":"/h"}`, native: true, updates: "false"},
			{name: "container", updates: "false"},
		} {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				p := newCopilotCompatibilityProbe(t, tc.context, tc.native, tc.updates, version)
				out, err := p.run(t, tc.hatch)
				log := p.log(t)
				refused := strings.Contains(out, "macos-user self's required SecurityServer deny")
				if refused != tc.wantRefuse {
					t.Errorf("refused=%v want=%v (err=%v):\n%s", refused, tc.wantRefuse, err, out)
				}
				if tc.wantRefuse {
					if err == nil || strings.Contains(log, "UPDATE") || strings.Contains(log, "EXEC") {
						t.Errorf("affected installed Copilot reached update or execution (err=%v, log=%q):\n%s", err, log, out)
					}
				} else if err != nil || !strings.Contains(log, "EXEC "+version) {
					t.Errorf("compatible control did not execute (err=%v, log=%q):\n%s", err, log, out)
				}
			})
		}
	}
}

func TestShippedCopilotCompatibilityBakesItsOwnPackData(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "false", "1.0.103")
	mutable := filepath.Join(p.home, ".config", "yolo-program-compatibility", "copilot.json")
	if err := os.MkdirAll(filepath.Dir(mutable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutable, []byte(`{"refuse_at_or_above":"99.0.0","message":"mutable home data"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := p.run(t, "")
	if err == nil || !strings.Contains(out, "macos-user self's required SecurityServer deny") || strings.Contains(out, "mutable home data") || strings.Contains(p.log(t), "EXEC") {
		t.Fatalf("launcher did not use its baked pack-owned compatibility data (err=%v, log=%q):\n%s", err, p.log(t), out)
	}
}

func TestGenerateAgentLaunchersRejectsInvalidDeclaredCopilotCompatibilityData(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*testing.T, string)
	}{
		{
			name: "malformed source",
			want: "compatibility",
			edit: func(t *testing.T, root string) {
				t.Helper()
				source := movedCompatibilityHomeFile(filepath.Join(root, "_official", "copilot"), "copilot")
				if _, err := os.Stat(source); err != nil {
					t.Fatalf("premise: the copilot pack no longer delivers its compatibility data through home/: %v", err)
				}
				if err := os.WriteFile(source, []byte(`{`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// A home/ file that is gone is no longer declared at all, so the missing source a
			// declaration still names is an explicit files contribution's.
			name: "missing source",
			want: "compatibility",
			edit: func(t *testing.T, root string) {
				t.Helper()
				packDir := movedDeclareCompatibility(t, root, "compatibility.json", 1)
				if err := os.Remove(filepath.Join(packDir, "compatibility.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "traversal source",
			want: `must not contain ".."`,
			edit: func(t *testing.T, root string) {
				t.Helper()
				movedDeclareCompatibility(t, root, "../outside.json", 1)
			},
		},
		{
			name: "symlink escapes owning pack",
			want: "compatibility",
			edit: func(t *testing.T, root string) {
				t.Helper()
				packDir := movedDeclareCompatibility(t, root, "compatibility-link.json", 1)
				outside := filepath.Join(root, "_official", "outside.json")
				if err := os.WriteFile(outside, []byte(`{"refuse_at_or_above":"1.0.93","message":"outside pack root"}`), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../outside.json", filepath.Join(packDir, "compatibility-link.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "duplicate declaration",
			want: "compatibility",
			edit: func(t *testing.T, root string) {
				t.Helper()
				movedDeclareCompatibility(t, root, "compatibility.json", 2)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orig := imageProbeBase
			imageProbeBase = t.TempDir()
			t.Cleanup(func() { imageProbeBase = orig })
			home := t.TempDir()
			root := stageShippedPacks(t)
			tc.edit(t, root)
			e := NewEnv(map[string]string{
				"JAIL_HOME": home, "YOLO_PACK_ROOT": root, AgentUpdatesEnv: "false",
			})
			e.Stderr = &strings.Builder{}
			if err := GenerateAgentLaunchers(e); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid declared compatibility data did not fail closed: %v", err)
			}
		})
	}
}

func TestGenerateAgentLaunchersIgnoresCompatibilityDataOwnedByAnotherPack(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	root := stageShippedPacks(t)
	copilotDir := filepath.Join(root, "_official", "copilot")
	claudeDir := filepath.Join(root, "_official", "claude")
	// Copilot stops delivering its own data, and claude delivers data to copilot's destination
	// through its own home/ tree instead.
	if err := os.Remove(movedCompatibilityHomeFile(copilotDir, "copilot")); err != nil {
		t.Fatalf("Copilot compatibility data not found in its home/ tree: %v", err)
	}
	foreign := movedCompatibilityHomeFile(claudeDir, "copilot")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(`{"refuse_at_or_above":"1.0.93","message":"foreign pack data"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root, AgentUpdatesEnv: "false"})
	e.Stderr = &strings.Builder{}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers: %v", err)
	}
	launcher, err := os.ReadFile(filepath.Join(e.LaunchDir(), "copilot"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(launcher), "foreign pack data") || strings.Contains(string(launcher), "COMPATIBILITY_REFUSE_AT_OR_ABOVE='1.0.93'") {
		t.Fatalf("Copilot launcher consumed another pack's data:\n%s", launcher)
	}
}

func TestShippedCopilotInvalidGenerationMetadataRefuses(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })

	for _, tc := range []struct {
		name, context string
	}{
		{name: "missing", context: ""},
		{name: "malformed", context: `{`},
		{name: "missing-mode", context: `{"workspace":"/w","home":"/h"}`},
		{name: "null-mode", context: `{"mode":null,"workspace":"/w","home":"/h"}`},
		{name: "unknown-mode", context: `{"mode":2,"workspace":"/w","home":"/h"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newCopilotCompatibilityProbe(t, tc.context, true, "true", "1.0.92")
			out, err := p.run(t, "")
			if err == nil || !strings.Contains(out, "invalid macos-user launch context") || strings.Contains(p.log(t), "UPDATE") || strings.Contains(p.log(t), "EXEC") {
				t.Fatalf("invalid native metadata did not refuse before update/execution (err=%v, log=%q):\n%s", err, p.log(t), out)
			}
		})
	}
}

// TestShippedCopilotCompatibilityRefusalNamesIssueAndRemedy: issue #65, option 1. The refusal
// says why (the installed version, the SecurityServer deny, upstream #5085) and names, as commands
// to type outside the jail, the two routes that run Copilot: the dedicated-account mode and a
// container backend. It never offers YOLO_USE_INSTALLED, which cannot get past this deny, and so
// it carries no "Start anyway:" block. Run with the hatch set, as the user who tried it would.
func TestShippedCopilotCompatibilityRefusalNamesIssueAndRemedy(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	for _, hatch := range []string{"", "1", "copilot"} {
		p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "true", "1.0.103")
		out, err := p.run(t, hatch)
		if err == nil || strings.Contains(p.log(t), "UPDATE") || strings.Contains(p.log(t), "EXEC") {
			t.Fatalf("hatch %q: an affected Copilot reached its update or ran (err=%v, log=%q):\n%s", hatch, err, p.log(t), out)
		}
		for _, want := range []string{
			"copilot 1.0.103 is installed",
			"com.apple.SecurityServer",
			"https://github.com/github/copilot-cli/issues/5085",
			"\nNext steps, in a terminal outside this jail:\n",
			"\n  yolo config set macos_user_mode account --user ",
			"yolo yolo-account create",
			"\n  yolo --setup podman-self -- copilot ",
			"apple-container-self for Apple Container",
			"\n  yolo config set setup podman-self --user ",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("hatch %q: the refusal lacks %q:\n%s", hatch, want, out)
			}
		}
		for _, unwanted := range []string{"Start anyway", "YOLO_USE_INSTALLED=", "1.0.92"} {
			if strings.Contains(out, unwanted) {
				t.Errorf("hatch %q: the refusal offers %q, which does not run an affected Copilot (or is ruled out):\n%s", hatch, unwanted, out)
			}
		}
	}
}

func TestShippedCopilotMalformedVersionTokensFailClosed(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })

	for _, version := range []string{"1.0.92.103", "1.0.", "1.0.92.103.", "1234567890.0.1"} {
		t.Run(version, func(t *testing.T) {
			p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "false", version)
			out, err := p.run(t, "")
			if err == nil || strings.Contains(p.log(t), "EXEC") ||
				(!strings.Contains(out, "cannot parse copilot's declared version probe") && !strings.Contains(out, "outside the supported numeric comparison bound")) {
				t.Fatalf("malformed version token did not fail closed (err=%v, log=%q):\n%s", err, p.log(t), out)
			}
		})
	}
}

func TestShippedCopilotVersionProbeAcceptsVendorLabelAndNumericComparison(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	for _, tc := range []struct {
		name, version string
		wantRefuse    bool
	}{
		{name: "affected", version: "Copilot CLI v1.0.103 (stable)", wantRefuse: true},
		{name: "compatible", version: "Copilot CLI v1.0.92 (stable)"},
		// Copilot 1.0.103's own output, captured natively on macOS: its version ends a sentence.
		{name: "affected sentence", version: "GitHub Copilot CLI 1.0.103.\nRun 'copilot update' to check for updates.", wantRefuse: true},
		{name: "compatible sentence", version: "GitHub Copilot CLI 1.0.92.\nRun 'copilot update' to check for updates."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "false", tc.version)
			out, err := p.run(t, "")
			refused := strings.Contains(out, "macos-user self's required SecurityServer deny")
			if refused != tc.wantRefuse || strings.Contains(out, "cannot parse copilot's declared version probe") ||
				(err == nil) == tc.wantRefuse || (tc.wantRefuse && strings.Contains(p.log(t), "EXEC")) ||
				(!tc.wantRefuse && !strings.Contains(p.log(t), "EXEC")) {
				t.Fatalf("vendor-labeled version was not compared numerically (refused=%v, err=%v, log=%q):\n%s", refused, err, p.log(t), out)
			}
		})
	}
}

func TestShippedCopilotVersionCompatibilityTriesProbeAlternatives(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "false", "1.0.92", "--version", "-v")
	binary := filepath.Join(p.home, ".local", "bin", "copilot")
	body := `#!/bin/sh
case "$*" in
  --version) printf '%s\n' '--version' >> "$HOME/probes.log"; exit 2 ;;
  -v) printf '%s\n' '-v' >> "$HOME/probes.log"; echo 1.0.92; exit 0 ;;
  *) printf 'EXEC %s\n' "$*" >> "$HOME/calls.log"; exit 0 ;;
esac
`
	if err := os.WriteFile(binary, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := p.run(t, "")
	probes, readErr := os.ReadFile(filepath.Join(p.home, "probes.log"))
	if err != nil || readErr != nil || !strings.HasPrefix(string(probes), "--version\n-v\n") || !strings.Contains(p.log(t), "EXEC --yolo chat") {
		t.Fatalf("version check did not try declared alternatives separately (err=%v, readErr=%v, probes=%q, log=%q):\n%s", err, readErr, probes, p.log(t), out)
	}
}

func TestShippedCopilotUnaffectedVersionRunsInMacosUserSelf(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "false", "1.0.92")
	out, err := p.run(t, "1")
	if err != nil || !strings.Contains(p.log(t), "EXEC 1.0.92") || strings.Contains(out, "macos-user self's required SecurityServer deny") {
		t.Fatalf("unaffected Copilot 1.0.92 did not run in self mode (err=%v, log=%q):\n%s", err, p.log(t), out)
	}
}

func TestShippedCopilotVersionProbeRemainsUsableWhenAffected(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "true", "1.0.103")
	out, err := p.runArgs(t, "--version")
	if err != nil || strings.TrimSpace(out) != "1.0.103" || strings.Contains(p.log(t), "UPDATE") || strings.Contains(p.log(t), "EXEC") {
		t.Fatalf("version probe should answer without update or ordinary execution (err=%v, log=%q):\n%s", err, p.log(t), out)
	}
}

func TestShippedCopilotInstallOnlyRefusesAnAffectedInstall(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "true", "")
	if err := os.Remove(filepath.Join(p.home, ".local", "bin", "copilot")); err != nil {
		t.Fatal(err)
	}
	curlDir := t.TempDir()
	curl := `#!/bin/sh
out=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out=$2; shift 2; else shift; fi
done
cat > "$out" <<'INSTALLER'
#!/bin/sh
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/copilot" <<'COPILOT'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then printf 'PROBE\n' >> "$HOME/calls.log"; echo 1.0.103; exit 0; fi
printf 'EXEC\n' >> "$HOME/calls.log"
COPILOT
chmod +x "$HOME/.local/bin/copilot"
INSTALLER
`
	if err := os.WriteFile(filepath.Join(curlDir, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(p.launcher)
	cmd.Env = append(os.Environ(), "HOME="+p.home, "PATH="+curlDir+":"+p.path, "YOLO_INSTALL_ONLY=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "macos-user self's required SecurityServer deny") || strings.Contains(p.log(t), "EXEC") {
		t.Fatalf("install-only accepted an affected binary (err=%v, log=%q):\n%s", err, p.log(t), out)
	}
	if _, statErr := os.Stat(filepath.Join(p.home, ".local", "bin", "copilot")); statErr != nil {
		t.Fatalf("install-only refusal should follow a completed first install: %v", statErr)
	}
}

func TestShippedCopilotUnparseableVersionProbeFailsClosed(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "false", "not-a-version")
	out, err := p.run(t, "1")
	if err == nil || !strings.Contains(out, "cannot parse copilot's declared version probe") || strings.Contains(p.log(t), "EXEC") ||
		!strings.Contains(out, "\n  yolo config set macos_user_mode account --user ") || strings.Contains(out, "Start anyway") {
		t.Fatalf("unparseable version did not fail closed (err=%v, log=%q):\n%s", err, p.log(t), out)
	}
}

func TestShippedCopilotPostUpdateCompatibilityCheck(t *testing.T) {
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	p := newCopilotCompatibilityProbe(t, `{"mode":1,"workspace":"/w","home":"/h"}`, true, "true", "1.0.92")
	if err := os.MkdirAll(filepath.Dir(p.stamp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.stamp, []byte("stamp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p.stamp, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := p.runWithUpdate(t, "1.0.103", "1")
	log := p.log(t)
	if err == nil || !strings.Contains(out, "macos-user self's required SecurityServer deny") || strings.Contains(log, "EXEC") || !strings.Contains(log, "UPDATE 1.0.103") {
		t.Fatalf("post-update version was not refused before execution (err=%v, log=%q):\n%s", err, log, out)
	}
}

type copilotCompatibilityProbe struct {
	launcher string
	home     string
	logPath  string
	stamp    string
	path     string
}

func newCopilotCompatibilityProbe(t *testing.T, context string, native bool, updates, version string, probeArgs ...string) copilotCompatibilityProbe {
	t.Helper()
	home := t.TempDir()
	packRoot := stageShippedPacks(t)
	if len(probeArgs) > 0 {
		newProbe := `probe_args = [`
		for i, arg := range probeArgs {
			if i > 0 {
				newProbe += ", "
			}
			newProbe += strconv.Quote(arg)
		}
		newProbe += `]`
		movedReplaceManifest(t, filepath.Join(packRoot, "_official", "copilot"), `probe_args = ["--version"]`, newProbe)
	}
	vars := map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, AgentUpdatesEnv: updates}
	if native {
		vars["YOLO_DARWIN_WORKSPACE"] = filepath.Join(home, "ws")
		vars["YOLO_MACOS_USER_TMPDIR_CONTEXT"] = context
	}
	e := NewEnv(vars)
	e.Stderr = &strings.Builder{}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers: %v", err)
	}
	launcher := filepath.Join(e.LaunchDir(), "copilot")
	logPath := filepath.Join(home, "calls.log")
	binary := filepath.Join(e.LocalBin(), "copilot")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf '%s\n' "$(cat "$HOME/version")" >> "$HOME/calls.log"
  cat "$HOME/version"
  exit 0
fi
if [ "${1:-}" = "update" ]; then
  printf 'UPDATE %s\n' "${FAKE_UPDATE_VERSION:-}" >> "$HOME/calls.log"
  [ -z "${FAKE_UPDATE_VERSION:-}" ] || printf '%s\\n' "$FAKE_UPDATE_VERSION" > "$HOME/version"
  exit 0
fi
printf 'EXEC %s\n' "$(cat "$HOME/version")" >> "$HOME/calls.log"
exit 0
`
	if err := os.WriteFile(binary, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "version"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return copilotCompatibilityProbe{launcher: launcher, home: home, logPath: logPath,
		stamp: filepath.Join(home, ".cache", "yolo-agent-stamps", "copilot.stamp"),
		path:  e.LocalBin() + ":" + os.Getenv("PATH")}
}

func (p copilotCompatibilityProbe) run(t *testing.T, hatch string) (string, error) {
	return p.runWithUpdate(t, "", hatch)
}

func (p copilotCompatibilityProbe) runArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(p.launcher, args...)
	cmd.Env = append(os.Environ(), "HOME="+p.home, "PATH="+p.path, NoLaunchFlagsEnv+"=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (p copilotCompatibilityProbe) runWithUpdate(t *testing.T, updateVersion, hatch string) (string, error) {
	t.Helper()
	cmd := exec.Command(p.launcher, "chat")
	cmd.Env = append(os.Environ(), "HOME="+p.home, "PATH="+p.path, "FAKE_UPDATE_VERSION="+updateVersion)
	if hatch != "" {
		cmd.Env = append(cmd.Env, "YOLO_USE_INSTALLED="+hatch)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (p copilotCompatibilityProbe) log(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(p.logPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
