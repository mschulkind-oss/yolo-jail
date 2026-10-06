package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CAPTURE STORE ON macos-user (docs/plans/install-capture.md hand-off H4, answered (b)): a
// launch stages a ROOT-OWNED COPY of each selected installer program's entry under the backend's
// state dir (macosuser.StagedCapturesRoot) and names it to the bootstrap, so the generated launcher
// materializes the capture into the sandbox home instead of running the vendor installer; and a
// launch auto-captures a program the store lacks. The plan, the stage scripts and the pick are
// pinned on Linux (internal/macosuser/capturestore_test.go, internal/cli/macosusercaptures_test.go,
// internal/cli/run/autocapture_test.go); these ask a Mac.
//
// A FIXTURE INSTALLER, never a vendor's: a file:// script under /private/tmp, which the session and
// capture profiles both let the sandbox account read (INFERRED: neither denies reads there), laying
// the shape claude's installer leaves — the program in a versions directory and ~/.local/bin/<bin>
// an absolute link to it, which the relocation rewrites. Each run of it appends one line to a log
// the test reads, so "one installer run" is counted rather than inferred. No agent starts.
//
// ⚠ Mac-only, behind requireMacosUser: the sandbox account and passwordless sudo.

// captureFixture is one fixture installer pack under /private/tmp.
type captureFixture struct {
	bin, root, log string
}

// newCaptureFixture writes the installer, its run log's directory and the pack declaring bin, and
// selects the pack in the isolated user config.
func newCaptureFixture(t *testing.T, bin string) captureFixture {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "yolo-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAsSandboxOwned(t, root, "/private/tmp/yolo-capture-") })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// The sandbox account appends the run log, so its directory is everyone's.
	runs := filepath.Join(root, "runs")
	if err := os.Mkdir(runs, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runs, 0o777); err != nil {
		t.Fatal(err)
	}
	f := captureFixture{bin: bin, root: root, log: filepath.Join(runs, "log")}
	script := filepath.Join(root, "install.sh")
	body := "#!/bin/sh\nset -eu\n" +
		`echo ran >> '` + f.log + `'` + "\n" +
		`mkdir -p "$HOME/.local/share/` + bin + `/v1" "$HOME/.local/bin"` + "\n" +
		`printf '#!/bin/sh\necho ` + bin + ` 1.0 "$@"\n' > "$HOME/.local/share/` + bin + `/v1/` + bin + `"` + "\n" +
		`chmod 755 "$HOME/.local/share/` + bin + `/v1/` + bin + `"` + "\n" +
		`ln -sf "$HOME/.local/share/` + bin + `/v1/` + bin + `" "$HOME/.local/bin/` + bin + `"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(root, "pack")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"capturestorepack","contributes":[`+
		`{"kind":"program","bin":"`+bin+`","via":"installer","url":"file://`+script+`"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs":[{"source":"file://`+pack+`","name":"capturestorepack"}]}`)
	return f
}

// runs is how many times the fixture installer has run.
func (f captureFixture) runs(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "ran\n")
}

// probe is the in-sandbox script: run the program (the launcher's cold branch materializes it),
// then report the materialized file's owner and link count, and whether the sandbox can write
// the staged store — by creating a file in it, by opening each staged copy of the program for
// append, and, when the materialized file is a hardlink, by opening that for append.
//
// THE APPEND PROBES ARE THE ONES THAT CAN SEE AN ACL. Creating a file in entries/ asks the mode
// and ACL of a directory root made 0755 with none, so it says REFUSED whatever the copied files
// carry; and a staged file is root's, so its owner says nothing either. But an entry a macos-user
// capture made carries the shared group's inherited ACE (read, write, append, …, writesecurity,
// chown), which grants what the mode bits deny, and the staged copy is safe only because plain
// `cp -R` does not carry it (macosuser's stageCaptureScript). `: >>` opens for append and writes
// no byte, so a hole is reported without changing what every workspace runs.
func (f captureFixture) probe() string {
	rel := ".local/share/" + f.bin + "/v1/" + f.bin
	file := `"$HOME/` + rel + `"`
	staged := macosuser.StagedCapturesRoot("") + "/entries/*/tree/" + rel
	return strings.Join([]string{
		f.bin + " --version",
		`echo "=== STAT ==="`,
		"/usr/bin/stat -f '%Su %l' " + file,
		`echo "=== WRITE ==="`,
		`if ( : > ` + macosuser.StagedCapturesRoot("") + `/entries/yolo-it-probe ) 2>/dev/null; then echo WROTE; else echo REFUSED; fi`,
		`echo "=== APPEND ==="`,
		`n=0; for p in ` + staged + `; do [ -e "$p" ] || continue; n=$((n+1)); ` +
			`if ( : >> "$p" ) 2>/dev/null; then echo "APPENDED $p"; else echo "REFUSED $p"; fi; done; echo "staged=$n"`,
		`echo "=== LINKED ==="`,
		`if [ "$(/usr/bin/stat -f %l ` + file + `)" -gt 1 ]; then ` +
			`if ( : >> ` + file + ` ) 2>/dev/null; then echo APPENDED; else echo REFUSED; fi; else echo NOTLINKED; fi`,
		`echo "=== END ==="`,
	}, "\n")
}

// materializedRe is capture-materialize's line (internal/cli/capturematerialize.go).
var materializedRe = regexp.MustCompile(`Materialized (\S+) from capture ([0-9a-f]+) by (reflink|hardlink|copy)`)

// checkMaterialized reads one launch's output: the program ran from a materialized capture, by
// which arm, and the sandbox can write no byte of the staged store — not a new file in it, not a
// staged copy of the program, and not the materialized file when it is a hardlink of one, which
// is what a hardlink of an entry a capture made would have handed it. Returns the entry's key.
func (f captureFixture) checkMaterialized(t *testing.T, label string, r result) string {
	t.Helper()
	m := materializedRe.FindStringSubmatch(r.combined())
	if m == nil || m[1] != f.bin {
		t.Fatalf("%s: the launcher did not materialize %s from a capture:\nstdout:\n%s\nstderr:\n%s",
			label, f.bin, r.stdout, r.stderr)
	}
	t.Logf("%s: materialized %s from %s by %s", label, f.bin, m[2], m[3])
	if !strings.Contains(r.stdout, f.bin+" 1.0 --version") {
		t.Errorf("%s: the materialized program did not run:\n%s", label, r.stdout)
	}
	fields := strings.Fields(section(r.stdout, "=== STAT ===", "=== WRITE ==="))
	if len(fields) != 2 {
		t.Fatalf("%s: unreadable stat of the materialized file: %q", label, fields)
	}
	t.Logf("%s: the materialized file is %s's, with %s link(s)", label, fields[0], fields[1])
	if got := section(r.stdout, "=== WRITE ===", "=== APPEND ==="); !strings.Contains(got, "REFUSED") {
		t.Errorf("%s: the sandbox could create a file in the staged capture store:\n%s", label, got)
	}
	appended := section(r.stdout, "=== APPEND ===", "=== LINKED ===")
	if strings.Contains(appended, "APPENDED") {
		t.Errorf("%s: the sandbox could open a staged copy of %s for writing — an ACE crossed with "+
			"the copy, and every workspace on this machine runs those bytes:\n%s", label, f.bin, appended)
	}
	if !regexp.MustCompile(`staged=[1-9]`).MatchString(appended) {
		t.Errorf("%s: the probe found no staged copy of %s to try, so the append probe saw "+
			"nothing:\n%s", label, f.bin, appended)
	}
	switch linked := section(r.stdout, "=== LINKED ===", "=== END ==="); {
	case strings.Contains(linked, "APPENDED"):
		t.Errorf("%s: the materialized file is a hardlink (%s links) the sandbox can open for "+
			"writing, so it could rewrite the staged store's bytes every workspace runs", label, fields[1])
	case strings.Contains(linked, "REFUSED"):
		t.Logf("%s: the materialized file is a hardlink (%s links), and the sandbox cannot write it", label, fields[1])
	case !strings.Contains(linked, "NOTLINKED"):
		t.Errorf("%s: unreadable hardlink probe: %q", label, linked)
	}
	return m[2]
}

// checkStagedRootOwned reports a staged entry whose files are not root's or carry any ACE, and
// schedules its removal.
//
// THE ACL HALF is what the owner half cannot see: the user store's entry carries the shared
// group's inherited ACE, an ACE grants what the mode bits deny, and nothing strips one from the
// staged copy, which relies on plain `cp -R` not carrying it (macosuser's stageCaptureScript says
// why, from Apple's cp source, and why `chmod -R -N` is not the remedy). So a staged tree must
// carry no ACE at all.
func checkStagedRootOwned(t *testing.T, key string) {
	t.Helper()
	dst := filepath.Join(macosuser.StagedCapturesRoot(""), "entries", key)
	t.Cleanup(func() { removeStagedCapture(t, key) })
	_ = filepath.Walk(dst, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			t.Errorf("walking the staged entry %s: %v", dst, err)
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
			t.Errorf("%s in the staged store is uid %d's, not root's", p, st.Uid)
		}
		return nil
	})
	out, err := exec.Command("/bin/ls", "-leR", dst).CombinedOutput()
	if err != nil {
		t.Fatalf("listing the staged entry's ACLs (ls -leR %s): %v\n%s", dst, err, out)
	}
	if aces := aceLineRe.FindAllString(string(out), -1); len(aces) > 0 {
		t.Errorf("the staged entry %s carries %d ACE(s) — the copy carried the user store's ACL "+
			"(group:%s's is the one that would let the sandbox write it):\n%s", dst, len(aces),
			macosuser.SandboxGroup, strings.Join(aces, "\n"))
	}
}

// aceLineRe is one ACE as `ls -le` prints it under a file: " 0: group:_yolojail allow read,…".
var aceLineRe = regexp.MustCompile(`(?m)^\s*\d+: \S+ (allow|deny) .*$`)

// TestMacosUserLaunchesMaterializeACapturedFixture: `yolo capture` admits the fixture into the
// store, and two workspaces' launches each materialize it from the staged copy — the installer
// having run once, for the capture, and never for either launch.
func TestMacosUserLaunchesMaterializeACapturedFixture(t *testing.T) {
	requireMacosUser(t)
	f := newCaptureFixture(t, "yolocapstoreprobe")
	t.Cleanup(func() { removeUserStoreEntries(t, f.bin) })

	c := runCommand(t, t.TempDir(), []string{"capture", f.bin}, withTimeout(macosUserTimeout()), macosUserRunEnv())
	if c.rc != 0 {
		t.Fatalf("yolo capture %s: rc %d\nstdout:\n%s\nstderr:\n%s", f.bin, c.rc, c.stdout, c.stderr)
	}
	if n := f.runs(t); n != 1 {
		t.Fatalf("the capture ran the installer %d times, want 1", n)
	}

	var key string
	for _, label := range []string{"first workspace", "second workspace"} {
		ws := macosUserWorkspace(t, `{}`)
		r := macosUserRunProbe(t, label, ws, f.probe())
		key = f.checkMaterialized(t, label, r)
	}
	if n := f.runs(t); n != 1 {
		t.Errorf("the installer ran %d times across one capture and two launches, want 1", n)
	}
	checkStagedRootOwned(t, key)
}

// TestMacosUserAutoCapturesAFixtureOnFirstLaunch: with auto-capture on, the first launch captures
// the uncaptured fixture with the macos-user capture act, stages it, and materializes it in the
// same launch; a second workspace's launch finds the store full and captures nothing.
func TestMacosUserAutoCapturesAFixtureOnFirstLaunch(t *testing.T) {
	requireMacosUser(t)
	f := newCaptureFixture(t, "yoloautocapprobe")
	t.Cleanup(func() { removeUserStoreEntries(t, f.bin) })
	autoOn := withEnv("YOLO_NO_AUTO_CAPTURE=")

	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "first launch", ws, f.probe(), autoOn)
	if !strings.Contains(r.combined(), "auto-capture") {
		t.Errorf("the first launch did not say it auto-captured:\n%s", r.combined())
	}
	key := f.checkMaterialized(t, "first launch", r)
	if n := f.runs(t); n != 1 {
		t.Errorf("the first launch ran the installer %d times, want 1 (its capture)", n)
	}

	ws2 := macosUserWorkspace(t, `{}`)
	r2 := macosUserRunProbe(t, "second launch", ws2, f.probe(), autoOn)
	f.checkMaterialized(t, "second launch", r2)
	if strings.Contains(r2.combined(), "never recorded on this machine") {
		t.Errorf("the second launch captured again although the store holds the program:\n%s", r2.combined())
	}
	if n := f.runs(t); n != 1 {
		t.Errorf("the installer ran %d times across two launches, want 1", n)
	}
	checkStagedRootOwned(t, key)
}

// removeStagedCapture removes one staged entry, as root: the store's own launches would prune it
// as unselected, but only once a launch stages something.
func removeStagedCapture(t *testing.T, key string) {
	t.Helper()
	if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(key) {
		t.Fatalf("refusing to remove staged capture %q: not a store key", key)
	}
	dst := filepath.Join(macosuser.StagedCapturesRoot(""), "entries", key)
	if !runQuiet(2*time.Minute, "sudo", "-n", "/bin/rm", "-rf", dst) {
		t.Logf("could not remove the staged capture %s; the next launch that stages one prunes it", dst)
	}
}

// removeUserStoreEntries removes every entry of the user's store that records bin — a fixture the
// suite made, in a store the isolated home shares with the machine (packHomeSharedStores).
func removeUserStoreEntries(t *testing.T, bin string) {
	t.Helper()
	entries := filepath.Join(paths.CapturesDirUnder(os.Getenv("HOME")), "entries")
	dirs, _ := os.ReadDir(entries)
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(entries, d.Name(), "receipts.jsonl"))
		if err != nil || !strings.Contains(string(b), `"bin":"`+bin+`"`) {
			continue
		}
		removeAsSandboxOwned(t, filepath.Join(entries, d.Name()), entries+string(os.PathSeparator))
	}
}

// removeAsSandboxOwned removes dir, escalating to sudo for files the sandbox account wrote, and
// only for a path under prefix.
func removeAsSandboxOwned(t *testing.T, dir, prefix string) {
	t.Helper()
	if err := os.RemoveAll(dir); err == nil {
		return
	}
	if !strings.HasPrefix(dir, prefix) || strings.Contains(dir, "..") {
		t.Fatalf("refusing to escalate removal of %s: it is not under %s", dir, prefix)
	}
	if !runQuiet(2*time.Minute, "sudo", "-n", "/bin/rm", "-rf", dir) {
		t.Logf("could not remove %s even with sudo; it will need removing by hand", dir)
	}
}
