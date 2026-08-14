package cli

// packloophole_test.go covers the `loophole` kind at the two SINGLE-PACK inspection
// commands, `pack lint` and `pack footprint`.
//
// Both are in one file because the invariant that binds them is that they must not
// DIVERGE. They inlined the same claim-formatting loop under a comment claiming they were
// shared "so their output does not drift", which made a new marker something that had to be
// added twice — and a loophole's marker is the one a reader most needs, since it is the
// difference between "yolo reads a file of yours" and "yolo runs this argv as you".

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// writeLoopholeModulePack scaffolds a pack whose ONLY contribution is one loophole module,
// returning the pack dir.
//
// LOOPHOLE-ONLY, deliberately, and it used to carry `skills` + `briefing` contributions it did
// not need. That was a CRUTCH, not a fixture detail: `stagedContent` built its claimed-paths set
// from KindFiles/KindBriefing/skill roots only, so a loophole's `from` claimed nothing and lint
// rejected every loophole-only pack with "stages N file(s) nothing reads" — naming the manifest
// the pack exists to deliver. The extra contributions hid that by making other paths claimed.
// The shape a real loophole pack has is this one, so the fixture is this one.
func writeLoopholeModulePack(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	mod := filepath.Join(dir, "loopholes", "acme-proxy")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pj := `{"contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A pack whose ONLY contribution is a loophole LINTS CLEAN. That is the shape a real
// loophole-shipping pack has, and lint rejected all of them: `stagedContent`'s sources came from
// KindFiles/KindBriefing/skill roots and never KindLoophole, so the module dir was unclaimed and
// the manifest read as "staged, and read by nothing" — a declaration yolo accepts and then tells
// the author is dead, which is the exact failure the check was rewritten to stop producing.
func TestLoopholeOnlyPackLintsClean(t *testing.T) {
	dir := writeLoopholeModulePack(t, `{
	  "name": "acme-proxy",
	  "description": "a loophole and nothing else",
	  "transport": "none"
	}`)
	var out, errw bytes.Buffer
	rc := packMain([]string{"lint", dir}, &out, &errw, false, nil)
	if rc != 0 {
		t.Fatalf("a pack whose only contribution is a loophole failed lint (rc=%d):\n%s%s",
			rc, out.String(), errw.String())
	}
	if strings.Contains(out.String(), "nothing reads") {
		t.Errorf("lint calls the loophole module's manifest unread content:\n%s", out.String())
	}
}

// And the check still FIRES for a loophole pack that stages content nothing names — the fix must
// widen the claimed set, not silence the rule.
func TestLoopholePackStillFlagsUnclaimedContent(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "loopholes", "acme-proxy")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"),
		[]byte(`{"name":"acme-proxy","transport":"none"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stray tree OUTSIDE the module dir: named by no contribution and in no conventional
	// location. The rule fires only when NOT ONE content file is claimed, so the loophole
	// contribution has to name a dir that is not there.
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pj := `{"contributes":[{"kind":"loophole","from":"loopholes/other"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
		t.Fatalf("lint accepted a pack whose loophole `from` names nothing it contains:\n%s",
			out.String())
	}
	if !strings.Contains(out.String(), "loopholes/other") {
		t.Errorf("the refusal does not name the missing module dir:\n%s", out.String())
	}
}

// daemonManifest declares one of every crossing AND stays INSIDE the pack-shipped subset,
// which `pack lint` now enforces (it is the authoring seam, so a manifest lint accepts has to
// be one every launch accepts).
//
// The bind is `:ro` with a `.sock` basename rather than `readonly: false` on `$HOME/...`, and
// the substitution is worth stating: it still lands in the IPC claim class, because `:ro` is
// no boundary for an AF_UNIX socket (measured — the kernel exempts non-REG/DIR/LNK inodes), so
// bindIsIPC keys on the socket basename too. The claim being asserted below is unchanged; only
// the manifest's legality for a pack changed.
const daemonManifest = `{
  "name": "acme-proxy",
  "description": "an example",
  "host_daemon": {"cmd": ["python3", "{loophole_dir}/acme-daemon.py", "--socket", "{socket}"],
                  "publishes": "socket"},
  "intercepts": [{"host": "api.acme.com"}],
  "host_bind_mounts": [{"host": "{loophole_dir}/acme.sock", "container": "/run/acme.sock",
                        "readonly": true}],
  "host_devices": ["/dev/acme"]
}`

// Both inspection commands show every claim, and both mark the host-EXECUTION one as such.
//
// The subtest split is over COMMANDS with one assertion body, which is the shape that makes
// a divergence fail: the two used to format claims independently, so a marker added to one
// was invisible in the other.
func TestLoopholeClaimsAppearInBothInspectionCommands(t *testing.T) {
	for _, verb := range []string{"lint", "footprint"} {
		t.Run(verb, func(t *testing.T) {
			dir := writeLoopholeModulePack(t, daemonManifest)
			var out, errw bytes.Buffer
			if rc := packMain([]string{verb, dir}, &out, &errw, false, nil); rc != 0 {
				t.Fatalf("%s rc = %d\n%s%s", verb, rc, out.String(), errw.String())
			}
			report := out.String()
			// Every crossing, one line each. A missing line is a crossing the reader never
			// sees, and (at the gate) one packMayAccessHost waves through.
			for _, want := range []string{
				"acme-proxy",     // the base claim's target: the loophole name
				"acme-daemon.py", // the daemon argv, in the base claim
				"acme-proxy:intercept:api.acme.com",
				"acme-proxy:ipc:/run/acme.sock",
				"acme-proxy:device:/dev/acme",
			} {
				if !strings.Contains(report, want) {
					t.Errorf("`pack %s` output is missing %q:\n%s", verb, want, report)
				}
			}
			// The marker: host execution has to READ differently from a host read.
			if !strings.Contains(report, "RUNS CODE ON YOUR MACHINE") {
				t.Errorf("`pack %s` did not mark the daemon claim as host execution — a "+
					"plain `⚠ review` puts it in the same visual class as reading a config "+
					"file:\n%s", verb, report)
			}
			// And the argv is RAW: the token unexpanded, nothing elided.
			if !strings.Contains(report, "{loophole_dir}") {
				t.Errorf("`pack %s` expanded or dropped {loophole_dir}:\n%s", verb, report)
			}
		})
	}
}

// `pack footprint`'s review tail must not reduce a host-execution claim to "1 loophole".
// A count-by-kind is the right unit for reads and the least interesting true thing that can
// be said about a pack that runs a daemon as you.
func TestFootprintReviewTailNamesHostExecution(t *testing.T) {
	dir := writeLoopholeModulePack(t, daemonManifest)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	if !strings.Contains(report, "worth review") {
		t.Fatalf("footprint printed no review summary:\n%s", report)
	}
	if !strings.Contains(report, "RUNNING CODE ON YOUR MACHINE") {
		t.Errorf("the review tail counts the exec claim by KIND (\"1 loophole\"), which says "+
			"nothing about what it does:\n%s", report)
	}
}

// A `from` naming a directory the pack does not contain is refused BY NAME, with a non-zero
// exit — never a silent skip. This is the pack.json layer's half of the split: it is
// decidable without loading any loophole, so lint is the right place to learn it.
func TestLintRefusesLoopholeFromNamingNoDirectory(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	pj := `{"contributes":[{"kind":"loophole","from":"loopholes/ghost"},` +
		`{"kind":"skills","from":"skills","into":".acme/skills"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	rc := packMain([]string{"lint", dir}, &out, &errw, false, nil)
	if rc == 0 {
		t.Fatalf("lint accepted a loophole `from` naming no directory — a declaration yolo "+
			"accepts and ignores is the defect the pack system refuses everywhere else:\n%s",
			out.String())
	}
	if !strings.Contains(out.String(), "loopholes/ghost") {
		t.Errorf("the refusal does not name the missing dir:\n%s", out.String())
	}
}

// The STRICT read of the module manifest belongs to lint, and it is the whole reason lint
// touches this kind: an unknown key is otherwise a declaration that silently does nothing,
// and the symptom surfaces much later as a missing endpoint.
func TestLintReportsATypoInTheLoopholeManifest(t *testing.T) {
	dir := writeLoopholeModulePack(t, `{
	  "name": "acme-proxy",
	  "host_deamon": {"cmd": ["/bin/true"]}
	}`)
	var out, errw bytes.Buffer
	rc := packMain([]string{"lint", dir}, &out, &errw, false, nil)
	if rc == 0 {
		t.Fatalf("lint accepted a misspelled manifest key:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "host_deamon") {
		t.Errorf("the diagnostic does not name the misspelled key:\n%s", out.String())
	}
}

// THE GATE. A pack shipping a loophole that crosses the boundary reaches the approval
// prompt, and the recorded claim carries the RAW argv.
//
// This is the case that used to slip through entirely: with no claims the gate takes the
// `len(want) == 0` branch, "reads nothing from the host, runs nothing on it", and a fetched
// pack's host daemon (or its bind of `/`) arrives with nothing to approve.
func TestLoopholeReachesTheApprovalGate(t *testing.T) {
	dir := writeLoopholeModulePack(t, daemonManifest)
	pr := richtext.Printer{W: &bytes.Buffer{}}
	terminal := func(r io.Reader) approvalStdin {
		return approvalStdin{reader: r, isTerminal: func() bool { return true }}
	}

	var out bytes.Buffer
	approved, denied := resolveHostApproval("acme", dir, packsrc.LockEntry{}, false, pr,
		terminal(strings.NewReader("y\n")), &out)
	if denied {
		t.Fatalf("a `y` at a terminal should approve: approved=%v", approved)
	}
	if len(approved) == 0 {
		t.Fatal("a loophole shipping a host daemon, an intercept, a socket bind and a device " +
			"produced ZERO approval claims — the gate then takes its len(want)==0 branch and " +
			"grants a FETCHED pack host access with no prompt, ever")
	}
	// The prompt must have been shown at all (an empty claim set skips it silently).
	if !strings.Contains(out.String(), "[y/N]") {
		t.Errorf("the approval prompt was never shown:\n%s", out.String())
	}
	joined := strings.Join(approved, "\n")
	for _, want := range []string{
		"RUNS", "on your machine", "{loophole_dir}/acme-daemon.py",
		"INTERCEPTS api.acme.com", "/run/acme.sock", "/dev/acme",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("approved claim set is missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, dir) {
		t.Errorf("an approved claim names this machine's staging path (%s), so it can never "+
			"match an approval recorded elsewhere and would re-prompt forever — and "+
			"promptYesNo fails closed on a non-TTY, so the loophole would be refused "+
			"permanently:\n%s", dir, joined)
	}

	// And an already-approved set carries forward with no prompt: a non-terminal stdin would
	// be refused at the gate, so reaching approved proves nothing was asked.
	prev := packsrc.LockEntry{Name: "acme", ApprovedHostAccess: approved}
	again, denied := resolveHostApproval("acme", dir, prev, true, pr,
		approvalStdin{reader: strings.NewReader("")}, &bytes.Buffer{})
	if denied || len(again) != len(approved) {
		t.Errorf("an unchanged loophole must carry its approval forward without prompting: "+
			"approved=%v denied=%v", again, denied)
	}

	// A CHANGED daemon argv is a claim the lockfile does not hold, so the gate must ask
	// again. This is the property the raw, unelided argv buys: an ellipsis would collapse
	// two different daemons onto one approved string.
	moved := writeLoopholeModulePack(t, strings.Replace(daemonManifest,
		"acme-daemon.py", "acme-daemon-v2.py", 1))
	_, denied = resolveHostApproval("acme", moved, prev, true, pr,
		approvalStdin{reader: strings.NewReader("")}, &bytes.Buffer{})
	if !denied {
		t.Error("a pack whose daemon argv CHANGED was carried forward on the old approval — " +
			"the claim string must be specific enough that a different daemon is a different " +
			"claim")
	}
}

// A loophole that crosses NOTHING claims nothing and lints clean — the enumeration is total
// over crossings, not decorative over declarations.
func TestLoopholeWithNoCrossingsClaimsNothing(t *testing.T) {
	dir := writeLoopholeModulePack(t, `{
	  "name": "acme-proxy",
	  "description": "a loophole that crosses nothing",
	  "transport": "none",
	  "state_files": ["ca.crt"]
	}`)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if strings.Contains(out.String(), "RUNS CODE ON YOUR MACHINE") {
		t.Errorf("a loophole with no host_daemon must not claim host execution:\n%s", out.String())
	}
	if strings.Contains(out.String(), "acme-proxy:") {
		t.Errorf("a loophole declaring only state_files must emit no per-crossing claim "+
			"(state_files stays inside yolo's own state tree):\n%s", out.String())
	}
}
