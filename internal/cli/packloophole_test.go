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
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
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
			if rc := packMain([]string{verb, dir}, &out, &errw, false); rc != 0 {
				t.Fatalf("%s rc = %d\n%s%s", verb, rc, out.String(), errw.String())
			}
			report := out.String()
			// Every crossing, one line each. A missing line is a crossing the reader never
			// sees — and since OQ-TP9 there is no second chance at `pack install`.
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
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
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
	packMain([]string{"init", dir}, &out, &errw, false)
	pj := `{"contributes":[{"kind":"loophole","from":"loopholes/ghost"},` +
		`{"kind":"skills","from":"skills","into":".acme/skills"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
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
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
	if rc == 0 {
		t.Fatalf("lint accepted a misspelled manifest key:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "host_deamon") {
		t.Errorf("the diagnostic does not name the misspelled key:\n%s", out.String())
	}
}

// THE FOOTPRINT IS THE WHOLE OF WHAT A USER GETS about a loophole's crossings, so it must be
// SPECIFIC ENOUGH TO CHECK: the raw argv, unelided and unexpanded, and one line per crossing.
//
// It was TestLoopholeReachesTheApprovalGate, over resolveHostApproval — the prompt OQ-TP9
// deleted (docs/design/trust-paths.md, 2026-09-04). Two properties it pinned survive the
// deletion and matter MORE without a prompt in front of them:
//
//   - a crossing with no line is a crossing nobody is told about. Under the gate that was a
//     silent grant (`len(want) == 0` read as "nothing to approve"); now it is a silent
//     crossing, which is the same hole with one fewer layer above it.
//   - the argv must be RAW and machine-independent. Under the gate an expanded
//     {loophole_dir} could never match a recorded approval and re-prompted forever; now it
//     is what makes the line a reader can compare against the pack's own manifest.
//
// Driven through `yolo pack footprint`, the command a user actually runs before selecting a
// pack, rather than through a producer function.
func TestLoopholeCrossingsAreShownSpecificallyEnoughToCheck(t *testing.T) {
	dir := writeLoopholeModulePack(t, daemonManifest)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	for _, want := range []string{
		"RUNS", "on your machine", "{loophole_dir}/acme-daemon.py",
		"api.acme.com", "/run/acme.sock", "/dev/acme",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the footprint is missing %q:\n%s\nA loophole shipping a host daemon, an "+
				"intercept, a socket bind and a device has four crossings, and since OQ-TP9 "+
				"this report is the only place any of them is disclosed", want, report)
		}
	}
	if strings.Contains(report, dir) {
		t.Errorf("the footprint names this machine's staging path (%s), so the line a reader "+
			"is meant to compare against the pack's manifest does not match it:\n%s",
			dir, report)
	}
	// A CHANGED daemon argv must produce a DIFFERENT line. Under the gate this bought a
	// re-prompt; it now buys the only way a reader who checked once can see it changed. The
	// raw, unelided argv is what makes it true — an ellipsis would collapse two daemons onto
	// one string.
	moved := writeLoopholeModulePack(t, strings.Replace(daemonManifest,
		"acme-daemon.py", "acme-daemon-v2.py", 1))
	var out2 bytes.Buffer
	if rc := packMain([]string{"footprint", moved}, &out2, &errw, false); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s", rc, out2.String())
	}
	if !strings.Contains(out2.String(), "acme-daemon-v2.py") {
		t.Errorf("a pack whose daemon argv CHANGED shows the same line as before:\n%s", out2.String())
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
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
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
