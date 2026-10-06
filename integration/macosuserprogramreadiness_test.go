package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// These fixture-only launches exercise macos-user's program-readiness act without invoking an
// agent or contacting a registry/API. The installer is a local file in the workspace; the target
// is a shell probe, so the test observes readiness before any selected program is run.
func macosUserProgramFixture(t *testing.T, ws, installer string) string {
	t.Helper()
	const packName = "macos-user-readiness-fixture"
	pack := resolvedTempDir(t)
	manifest := `{"name":"` + packName + `","description":"local readiness fixture","contributes":[` +
		`{"kind":"program","bin":"yolo-readiness-fixture","via":"installer","url":"file://` + installer + `"}]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs":[{"source":"file://`+pack+`","name":"`+packName+`"}]}`)
	return packName
}

func writeMacosUserFixtureInstaller(t *testing.T, ws, body string) string {
	t.Helper()
	path := filepath.Join(ws, "readiness-installer.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\nset -euo pipefail\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMacosUserInstallsAnAbsentDeclaredProgramBeforeTheTarget(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)
	installer := writeMacosUserFixtureInstaller(t, ws, `
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/yolo-readiness-fixture" <<'TOOL'
#!/bin/sh
echo readiness-fixture-program-ran
TOOL
chmod +x "$HOME/.local/bin/yolo-readiness-fixture"
`)
	macosUserProgramFixture(t, ws, installer)

	cold := runMacosUser(t, ws, `
if [ -x "$HOME/.local/bin/yolo-readiness-fixture" ]; then echo PROGRAM_PRESENT_BEFORE_TARGET; fi
echo TARGET_RAN
`, withReadiness())
	if cold.rc != 0 || !strings.Contains(cold.stdout, "PROGRAM_PRESENT_BEFORE_TARGET") ||
		!strings.Contains(cold.stdout, "TARGET_RAN") {
		t.Fatalf("the cold macos-user launch did not install the selected program before its target (rc %d):\n%s\n%s",
			cold.rc, cold.stdout, cold.stderr)
	}
	if !strings.Contains(cold.combined(), "Installing yolo-readiness-fixture...") {
		t.Errorf("the missing program did not run through its own install-only launcher:\n%s", cold.combined())
	}
	if strings.Contains(cold.combined(), "readiness-fixture-program-ran") {
		t.Errorf("readiness ran the installed program instead of only installing it:\n%s", cold.combined())
	}

	sentinel := macosUserSeedStageLog(t, ws)
	warm := runMacosUser(t, ws, `echo TARGET_RAN`, withReadiness())
	if warm.rc != 0 || !strings.Contains(warm.stdout, "TARGET_RAN") {
		t.Fatalf("the warm macos-user launch failed (rc %d):\n%s\n%s", warm.rc, warm.stdout, warm.stderr)
	}
	if got, err := os.ReadFile(provision.StartupLog(ws)); err != nil || string(got) != sentinel+"\n" {
		t.Errorf("the warm admission started the provisioning stage; startup.log = %q, err = %v",
			string(got), err)
	}
	if strings.Contains(warm.combined(), "Installing yolo-readiness-fixture...") {
		t.Errorf("a host-proven installed program unnecessarily started readiness again:\n%s", warm.combined())
	}
}

func TestMacosUserProgramReadinessDoesNotReuseAProgramFromThePreviousWorkspace(t *testing.T) {
	requireMacosUser(t)
	workspaceA := macosUserWorkspace(t, `{}`)
	installerA := writeMacosUserFixtureInstaller(t, workspaceA, `
mkdir -p "$HOME/.local/bin"
printf '#!/bin/sh\necho workspace-a-program-ran\n' > "$HOME/.local/bin/yolo-readiness-fixture"
chmod +x "$HOME/.local/bin/yolo-readiness-fixture"
`)
	macosUserProgramFixture(t, workspaceA, installerA)
	launchA := runMacosUser(t, workspaceA, `test -x "$HOME/.local/bin/yolo-readiness-fixture" && echo WORKSPACE_A_READY`, withReadiness())
	if launchA.rc != 0 || !strings.Contains(launchA.stdout, "WORKSPACE_A_READY") {
		t.Fatalf("workspace A did not receive its selected program (rc %d):\n%s\n%s",
			launchA.rc, launchA.stdout, launchA.stderr)
	}

	workspaceB := macosUserWorkspace(t, `{}`)
	installerB := writeMacosUserFixtureInstaller(t, workspaceB, `
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/yolo-readiness-fixture" <<'TOOL'
#!/bin/sh
echo workspace-b-program-ran
TOOL
chmod +x "$HOME/.local/bin/yolo-readiness-fixture"
`)
	macosUserProgramFixture(t, workspaceB, installerB)
	launchB := runMacosUser(t, workspaceB, `
if [ -x "$HOME/.local/bin/yolo-readiness-fixture" ]; then echo WORKSPACE_B_READY; fi
echo TARGET_RAN
`, withReadiness())
	if launchB.rc != 0 || !strings.Contains(launchB.stdout, "WORKSPACE_B_READY") ||
		!strings.Contains(launchB.stdout, "TARGET_RAN") {
		t.Fatalf("workspace A's account-home symlink hid cold workspace B's missing program (rc %d):\n%s\n%s",
			launchB.rc, launchB.stdout, launchB.stderr)
	}
	if !strings.Contains(launchB.combined(), "Installing yolo-readiness-fixture...") {
		t.Errorf("workspace B did not start readiness despite its own physical install surface being cold:\n%s",
			launchB.combined())
	}
}

func TestMacosUserProgramReadinessRefusesAndHonorsTheExistingBypass(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)
	installer := writeMacosUserFixtureInstaller(t, ws, `echo "fixture installer failed" >&2
exit 1
`)
	macosUserProgramFixture(t, ws, installer)

	refused := runMacosUser(t, ws, `echo TARGET_RAN`, withReadiness())
	if refused.rc != 1 || strings.Contains(refused.stdout, "TARGET_RAN") {
		t.Fatalf("an installer failure must refuse before the target (rc %d):\n%s\n%s",
			refused.rc, refused.stdout, refused.stderr)
	}
	if !strings.Contains(refused.combined(), "REFUSING to start this jail") ||
		!strings.Contains(refused.combined(), "program yolo-readiness-fixture (pack macos-user-readiness-fixture)") ||
		!strings.Contains(refused.combined(), "fixture installer failed") ||
		!strings.Contains(refused.combined(), "its install exited 1") ||
		!strings.Contains(refused.combined(), "YOLO_ALLOW_MISSING_PROGRAMS=1") {
		t.Errorf("the refusal did not use the shared bootstrap disclosure and existing bypass:\n%s", refused.combined())
	}

	bypassed := runMacosUser(t, ws, `echo TARGET_RAN`, withEnv("YOLO_ALLOW_MISSING_PROGRAMS=1"), withReadiness())
	if bypassed.rc != 0 || !strings.Contains(bypassed.stdout, "TARGET_RAN") {
		t.Fatalf("the existing bypass did not let the target run (rc %d):\n%s\n%s",
			bypassed.rc, bypassed.stdout, bypassed.stderr)
	}
	for _, want := range []string{
		"YOLO_ALLOW_MISSING_PROGRAMS is set, so this jail starts WITHOUT what a selected pack declares",
		"program yolo-readiness-fixture (pack macos-user-readiness-fixture)",
		"fixture installer failed",
		"its install exited 1",
	} {
		if !strings.Contains(bypassed.combined(), want) {
			t.Errorf("the bypass disclosure does not say %q:\n%s", want, bypassed.combined())
		}
	}
}
