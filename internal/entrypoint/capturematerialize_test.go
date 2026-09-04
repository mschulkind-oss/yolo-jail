package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capturematerialize_test.go pins the LAUNCHER half of install-capture's second verb: the
// branch in nativeLauncherTemplate's _do_install that materializes an already-captured
// install instead of downloading it (docs/design/program-delivery.md §6.3, amended;
// install-capture.md slice 4).
//
// EVERY CASE GOES THROUGH GenerateAgentLaunchers, not through nativeAgentLauncher, and that
// is deliberate. The whole chain slice 4 adds on this side is three links —
// `capturesDir(e)` reads the boot's CapturesDirEnv, GenerateAgentLaunchers' native arm passes
// it, the template bakes it — and a test that called the renderer with a store path in hand
// would go green with the middle link deleted, which is exactly the "pins the CALLEE while
// the CALL SITE is unpinned" shape AGENTS.md names and slice 3 shipped once already.
//
// The MECHANISM (reflink → hardlink → copy) is not tested here; that is internal/capture's,
// and the fake `yolo` below stands in for it. What is tested here is whether the launcher
// asks at all, with what, and what it does with each answer.

// captureLauncherFixture generates the real launchers for a pack declaring `probetool` via
// an installer URL, with capturesDir as the boot's CapturesDirEnv, and returns the temp home,
// the launcher path, the fake-bin dir and the argv log the fake `yolo` writes.
func captureLauncherFixture(t *testing.T, url, capturesDir string) (home, launcher, fakeBin, argvLog string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home = t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"probetool","via":"installer","url":"` + url + `"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		"JAIL_HOME":      home,
		"YOLO_WORKSPACE": filepath.Join(home, "ws"),
		"YOLO_PACK_ROOT": packRoot,
	}
	if capturesDir != "" {
		vars[CapturesDirEnv] = capturesDir
	}
	e := NewEnv(vars)
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	fakeBin = filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	argvLog = filepath.Join(home, "yolo-argv.log")
	return home, filepath.Join(e.LaunchDir(), "probetool"), fakeBin, argvLog
}

// writeFakeYolo plants a `yolo` on PATH that logs its argv and either installs the program
// (rc 0) or reports a miss (rc 1) — the two answers the launcher's branch has to tell apart.
func writeFakeYolo(t *testing.T, fakeBin, argvLog string, succeed bool) {
	t.Helper()
	body := `#!/bin/bash
printf '%s\n' "$*" >> ` + shq(argvLog) + `
`
	if succeed {
		body += `mkdir -p "$HOME/.local/bin"
printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"
chmod +x "$HOME/.local/bin/probetool"
echo MATERIALIZED >&2
exit 0
`
	} else {
		body += `echo NO_CAPTURE >&2
exit 1
`
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// shq single-quotes a path for the fake script above. The tests' own paths come from
// t.TempDir(), so this is hygiene rather than a guard against a hostile value —
// launchersplice_test.go owns that question for the generated launchers themselves.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runCaptureLauncher runs a generated launcher with only the fake bin plus the real PATH.
func runCaptureLauncher(t *testing.T, home, launcher, fakeBin string) (int, string) {
	t.Helper()
	cmd := exec.Command(launcher)
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	rc := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running launcher: %v (output %s)", err, out)
		}
		rc = ee.ExitCode()
	}
	return rc, string(out)
}

// THE SLICE-4 ACCEPTANCE CASE, at the launcher level: with a capture store present and a
// materialize that succeeds, the vendor installer is NEVER FETCHED.
//
// The installer URL is a live localhost server whose script SHOUTS when it runs, so "no
// download happened" is an assertion about observed behaviour rather than about an absence —
// the same construction installonly_test.go uses.
func TestNativeLauncherMaterializesInsteadOfDownloading(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		`echo INSTALLER_RAN`,
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")
	store := t.TempDir()
	home, launcher, fakeBin, argvLog := captureLauncherFixture(t, url, store)
	writeFakeYolo(t, fakeBin, argvLog, true)

	rc, out := runCaptureLauncher(t, home, launcher, fakeBin)

	if rc != 0 {
		t.Errorf("rc=%d\n%s", rc, out)
	}
	if strings.Contains(out, "INSTALLER_RAN") {
		t.Errorf("the vendor installer was FETCHED AND RUN even though a capture "+
			"materialized — this is the download slice 4 exists to delete:\n%s", out)
	}
	if !strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("the launcher did not exec the materialized program:\n%s", out)
	}
	// The argv is the contract with `yolo internal capture-materialize`, and every flag in
	// it is a value only the launcher knows.
	argv := readFileString(t, argvLog)
	for _, want := range []string{
		"internal capture-materialize",
		"--store=" + store,
		"--home=" + home,
		"--bin=probetool",
		"--declared=" + url,
		"--receipts=" + filepath.Join(home, "ws", ".yolo", "receipts.jsonl"),
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("the materialize argv is missing %q:\n%s", want, argv)
		}
	}
}

// THE FALLBACK IS NOT REMOVABLE. install-capture.md's Blockers say making a capture mandatory
// for the installer class is a behaviour change nobody has ruled on, and a first run on a
// machine with no capture must still work — so a materialize that reports a miss must leave
// the launcher downloading exactly as it did before slice 4.
func TestNativeLauncherDownloadsWhenTheMaterializeMisses(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		`echo INSTALLER_RAN`,
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	store := t.TempDir()
	home, launcher, fakeBin, argvLog := captureLauncherFixture(t, url, store)
	writeFakeYolo(t, fakeBin, argvLog, false)

	rc, out := runCaptureLauncher(t, home, launcher, fakeBin)

	if rc != 0 {
		t.Errorf("rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "INSTALLER_RAN") {
		t.Errorf("a materialize MISS must fall through to the vendor installer:\n%s", out)
	}
	if !strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("the launcher did not exec the installed program:\n%s", out)
	}
	if !strings.Contains(readFileString(t, argvLog), "capture-materialize") {
		t.Errorf("the launcher never asked the store at all")
	}
}

// A jail with NO STORE never invokes the materializer. That is the macos-user shape today and
// the pre-slice-4 host shape, and it must cost nothing: no subprocess, no message.
func TestNativeLauncherWithNoCaptureStoreNeverAsks(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		`echo INSTALLER_RAN`,
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	home, launcher, fakeBin, argvLog := captureLauncherFixture(t, url, "")
	writeFakeYolo(t, fakeBin, argvLog, true)

	rc, out := runCaptureLauncher(t, home, launcher, fakeBin)

	if rc != 0 {
		t.Errorf("rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "INSTALLER_RAN") {
		t.Errorf("with no store the launcher must install as it always did:\n%s", out)
	}
	if _, err := os.Stat(argvLog); err == nil {
		t.Errorf("the launcher ran the materializer with no store configured:\n%s",
			readFileString(t, argvLog))
	}
}

// A CONFIGURED store that does not EXIST is a miss, not a failure. The launch pipeline skips
// the mount for a store that is not there, and a host↔jail skew could leave the variable set
// with nothing behind it; either way the answer is "download".
func TestNativeLauncherTreatsAMissingStoreDirAsAMiss(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		`echo INSTALLER_RAN`,
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	home, launcher, fakeBin, argvLog := captureLauncherFixture(t,
		url, filepath.Join(t.TempDir(), "never-created"))
	writeFakeYolo(t, fakeBin, argvLog, true)

	rc, out := runCaptureLauncher(t, home, launcher, fakeBin)

	if rc != 0 || !strings.Contains(out, "INSTALLER_RAN") {
		t.Errorf("a store path with nothing behind it must read as a miss (rc=%d):\n%s", rc, out)
	}
	if _, err := os.Stat(argvLog); err == nil {
		t.Errorf("the launcher invoked the materializer against a store that does not exist")
	}
}

// A NON-ABSOLUTE store path is dropped with a warning rather than baked. It can only arrive
// from a host emitting a malformed -e, and a relative path would make the launcher's
// `[ -d "$CAPTURES_DIR" ]` resolve against whatever directory the agent happened to be in.
func TestCapturesDirRefusesARelativePath(t *testing.T) {
	var warnings []string
	e := NewEnv(map[string]string{"JAIL_HOME": "/home/agent", CapturesDirEnv: "captures"})
	e.Stderr = writerFunc(func(p []byte) (int, error) {
		warnings = append(warnings, string(p))
		return len(p), nil
	})
	if got := capturesDir(e); got != "" {
		t.Errorf("capturesDir with a relative value = %q, want \"\"", got)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), CapturesDirEnv) {
		t.Errorf("a malformed %s must be reported, got %v", CapturesDirEnv, warnings)
	}
}

// writerFunc adapts a function to io.Writer for the warning capture above.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// The capture receipt ROUND-TRIPS through its own reader, for both acts.
//
// The reader is what resolves bin+platform → store key on the materialize path, so a writer
// and a reader that drifted would not produce a parse error — it would produce a store that
// answers every lookup with a miss, and a machine that quietly re-downloads forever.
func TestCaptureReceiptRoundTripsBothActs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.jsonl")
	when := time.Date(2026, 9, 4, 12, 34, 56, 0, time.UTC)
	for _, act := range []string{ReceiptActRecord, ReceiptActMaterialize} {
		line := CaptureReceipt{
			Bin: "probetool", Declared: "https://example.invalid/i.sh",
			Key: "0123456789abcdef", Digest: strings.Repeat("a", 64), Bytes: 4096,
			Path: "/store/entries/0123456789abcdef", Platform: "linux/amd64",
			Act: act, Time: when,
		}.Line()
		if err := AppendReceiptLine(path, line); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := ReadCaptureReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d capture receipts, want 2: %+v", len(recs), recs)
	}
	for i, wantAct := range []string{ReceiptActRecord, ReceiptActMaterialize} {
		r := recs[i]
		if r.Act != wantAct {
			t.Errorf("act[%d] = %q, want %q", i, r.Act, wantAct)
		}
		if r.Bin != "probetool" || r.Platform != "linux/amd64" || r.Key != "0123456789abcdef" {
			t.Errorf("the (bin, platform, key) triple did not survive: %+v", r)
		}
		if r.Digest != strings.Repeat("a", 64) || r.Bytes != 4096 {
			t.Errorf("digest/bytes did not survive: %+v", r)
		}
		if !r.Time.Equal(when) {
			t.Errorf("time = %v, want %v", r.Time, when)
		}
	}
	// Only capture lines come back: the file is shared with the installer/npm kinds.
	if err := AppendReceiptLine(path, receiptPrefix("installer", "probetool", "u")+
		`,"act":"install","time":"2026-09-04T00:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	if recs, err = ReadCaptureReceipts(path); err != nil || len(recs) != 2 {
		t.Errorf("ReadCaptureReceipts returned %d receipts (err %v), want the 2 capture "+
			"lines and not the installer one", len(recs), err)
	}
}
