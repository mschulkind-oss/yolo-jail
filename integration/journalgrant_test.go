package integration

// journalgrant_test.go is the container tier of the journal's flip
// (docs/reference/loophole-grants.md F1): the shipped `journal` loophole grants access
// through its [[grants]], not the old `enabled` switch. Selecting the pack runs the bridge; what a
// request may read is the grant the project holds, approved at a gate. The unit tiers pin the
// daemon's decision against a grant file (internal/journald/grant_test.go) and the launch's grant
// block (internal/cli/run); only a launch shows the config, the gate, the grant file, the daemon
// and the in-jail client joined.

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// journalProbe runs one request through the in-jail client and prints its exit status after
// the client's own output, so a refusal is read off the wire and not off the launch's status.
const journalProbe = `yolo-journalctl -n 1; echo "JOURNAL-RC=$?"`

// A project that selected the journal pack and holds no grant gets the requestable refusal,
// exit 64, naming the one command that asks for `read` — on Linux, the one platform the loophole
// declares; elsewhere the same exit 64 carries the inert note instead. `read` is requestable by default, so
// no user ceiling line is needed for the refusal to name a request rather than `yolo allow`.
// The refusal comes before the daemon runs journalctl, so this needs no host journal.
func TestJournalWithNoGrantRefusesInTheJail(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = [\"journal\"]\n")
	dir := writeWorkspaceTOML(t, "")

	r := runYolo(t, dir, journalProbe)
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	t.Logf("the jail's output:\n%s", r.combined())
	if !strings.Contains(r.stdout, "JOURNAL-RC=64") {
		t.Fatalf("yolo-journalctl with no grant must exit 64 (a grant refusal):\n%s", r.combined())
	}
	// The journal loophole declares `platforms = ["linux"]`, so on a Mac host it is inert, and
	// the client says so before it tries the endpoint (PD-8): there is no grant to ask for,
	// because granting one would still serve nothing. Naming `yolo request journal read` there
	// would send the user to a step that cannot help, so the darwin tier pins the inert note
	// instead, and that the request step is absent from it.
	if runtime.GOOS != "linux" {
		const inert = "yolo-journalctl: the journal loophole does nothing on this setup"
		if !strings.Contains(r.combined(), inert) {
			t.Errorf("on %s the journal loophole is unsupported, and the client must say %q:\n%s",
				runtime.GOOS, inert, r.combined())
		}
		if strings.Contains(r.combined(), "yolo request journal read") {
			t.Errorf("on %s the refusal must not name `yolo request journal read`, a grant "+
				"that would still serve nothing:\n%s", runtime.GOOS, r.combined())
		}
		return
	}
	// combined, not stderr alone: the in-jail stderr reaches this process through the
	// launcher, and only the refusal prints this line.
	if !strings.Contains(r.combined(), "yolo request journal read") {
		t.Errorf("the refusal must name `yolo request journal read`, the step that asks for "+
			"the grant:\n%s", r.combined())
	}
}

// A project that requests `read`, launched with the gate accepted (jailRunArgs carries
// --accept-config-changes), holds the grant, and the client reads the user journal. PD-14: a
// host with no readable user journal (a nested jail has none) cannot show a served request,
// so the test skips there with the reason; CI's Linux runner has one.
func TestJournalReadGrantServesTheUserJournal(t *testing.T) {
	requireJail(t)
	if err := exec.Command("journalctl", "--user", "-n", "1").Run(); err != nil {
		t.Skipf("this host has no readable user journal (`journalctl --user -n 1`: %v), so a "+
			"served request cannot be shown here (plan PD-14)", err)
	}

	writeUserTOML(t, "packs = [\"journal\"]\n")
	dir := writeWorkspaceTOML(t, "[requests.journal]\nread = true\n")

	r := runYolo(t, dir, journalProbe)
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "JOURNAL-RC=0") {
		t.Fatalf("yolo-journalctl with `read` requested and the gate accepted must exit 0:\n%s",
			r.combined())
	}
}
