package run

// loopholerefusal_test.go pins the REPORTING half of §4.3 G3 — "refusals printed per-claim" —
// and the honesty of the pre-spawn disclosure that sits beside it.
//
// Two defects, one shape. The launch reported refusals from HonoredHostFiles, HonoredMounts
// and HonoredInstalls; there was no equivalent for a LOOPHOLE, so an unapproved fetched pack's
// loophole was withheld in SILENCE — a pack the user installed, selected, and whose whole
// purpose is a loophole, doing nothing, with no line saying why or how to fix it. (Since
// OQ-TP6 that report is fatal rather than advisory; see packrefusal.go. The reporting is still
// what this file pins — the ruling changed what happens after the sentence, not the sentence.)
// And the
// pre-spawn exec disclosure printed that same loophole's daemon argv under the heading "This
// launch runs pack code on your machine", because the footprint is deliberately not gated on
// MayAccessHost for this kind (it answers what a pack WANTS). So the one banner a user reads
// before host code runs announced, as imminent, a daemon that was never going to start —
// which is worse than silence: it teaches the reader that the block is not to be trusted.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A REFUSED loophole is named, with the reason and the fix. The three shipped Honored*
// reporters set the shape: "pack X: refused <thing> — <why>".
//
// UPDATED 2026-08-18 (docs/design/trust-paths.md OQ-TP6): the report is no longer a warning
// beside a working jail, it is the launch REFUSAL itself. The assertions are unchanged — the
// sentence a user reads is the same sentence, and it is now the whole outcome rather than a
// line above one — but they read the error instead of stdout, because a warning nobody had to
// act on is exactly what the ruling retired.
func TestUnapprovedLoopholeRefusesTheLaunch(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	isolatePackModules(t)
	fakeBundled(t)
	fetchedLoopholePack(t, home)

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out
	_, _, _, err := o.stagePacks("yolo-test-loophole-refusal")
	if err == nil {
		t.Fatalf("an unapproved fetched pack's loophole was refused and the launch went ahead "+
			"anyway — OQ-TP6 says a refused contribution refuses the launch, because a pack "+
			"that half-loads is one nobody can predict from reading it:\n%s", out.String())
	}
	got := err.Error()
	for _, want := range []string{
		"acme",         // the pack
		"acme-proxy",   // the loophole, by name
		"refused",      // that it did not happen
		"pack install", // the route to approving it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal never names the withheld loophole (%q missing). This message "+
				"is now the ENTIRE user experience of the failure — there is no jail left to "+
				"go looking in:\n%s", want, got)
		}
	}
}

// HonoredLoopholes is the predicate behind that line, and it is shaped like its three
// siblings: granted + refused, refused NEVER silent.
func TestHonoredLoopholesGrantsAndRefusesByOrigin(t *testing.T) {
	body := `{"name":"acme-proxy","transport":"none","host_daemon":{"cmd":["/bin/true"],` +
		`"publishes":"socket"},"host_devices":["/dev/snd"]}`

	granted := writeRealLoopholePack(t, "acme", "acme-proxy", body)
	mods, refused := granted.HonoredLoopholes()
	if len(mods) != 1 || len(refused) != 0 {
		t.Errorf("an approved pack's loophole must be granted and silent: mods=%d refused=%v",
			len(mods), refused)
	}

	unapproved := writeRealLoopholePack(t, "acme", "acme-proxy", body)
	unapproved.MayAccessHost = false
	mods, refused = unapproved.HonoredLoopholes()
	if len(mods) != 0 {
		t.Errorf("an UNAPPROVED pack's loophole must be withheld, got %d modules", len(mods))
	}
	if len(refused) != 1 {
		t.Fatalf("refused = %v, want exactly one message naming the loophole", refused)
	}
	for _, want := range []string{"acme", "acme-proxy", "refused", "pack install"} {
		if !strings.Contains(refused[0], want) {
			t.Errorf("the refusal is missing %q — it lands on a user whose pack silently does "+
				"nothing, so the sentence is the whole interface:\n  %s", want, refused[0])
		}
	}
}

// THE DISCLOSURE MUST NOT LIE. An unapproved loophole's daemon argv may not appear under "this
// launch runs pack code on your machine": that block's entire value is that every line in it
// is about to happen, and a refused daemon shown as pending is worse than silence.
//
// The footprint deliberately keeps the claim (it answers what a pack WANTS, which is the
// question `pack footprint` and `pack install` ask), so the fix belongs at the DISCLOSURE,
// which answers what THIS LAUNCH does.
func TestDisclosureDoesNotAnnounceARefusedLoopholeAsPending(t *testing.T) {
	body := `{"name":"acme-proxy","transport":"none",` +
		`"host_daemon":{"cmd":["python3","{loophole_dir}/acme-daemon.py"],"publishes":"socket"},` +
		`"intercepts":[{"host":"api.acme.test"}]}`
	p := writeRealLoopholePack(t, "acme", "acme-proxy", body)
	p.MayAccessHost = false

	execLines := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureExec))
	if strings.Contains(execLines, "acme-daemon.py") {
		t.Errorf("the pre-spawn block announces an UNAPPROVED pack's daemon as about to run:\n%s\n"+
			"That block's value is that every line in it is imminent. A refused daemon shown "+
			"as pending teaches the reader to distrust the whole block", execLines)
	}
	readLines := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureRead))
	if strings.Contains(readLines, "api.acme.test") {
		t.Errorf("a refused loophole's intercept is disclosed as though it happened:\n%s", readLines)
	}

	// The APPROVED pack still discloses both, or the fix is a blanket silencing of the kind.
	p.MayAccessHost = true
	if got := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureExec)); !strings.Contains(got, "acme-daemon.py") {
		t.Errorf("an APPROVED daemon is missing from the pre-spawn block:\n%s", got)
	}
	if got := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureRead)); !strings.Contains(got, "api.acme.test") {
		t.Errorf("an APPROVED intercept is missing from the read disclosure:\n%s", got)
	}
}

// The subtraction is a rule over CLASSES, not a special case for `loophole`. Every kind the
// disclosure treats as host-crossing must be withheld for an unapproved pack, with `env` as
// the one named exception (literal strings from pack.json, never origin-gated, and a refused
// pack still gets them).
//
// A table-driven check rather than one about the loophole kind, because "which claims survive
// the gate" was a fact only the footprint knew — and the footprint's answer is deliberately
// different from the launch's for at least one kind. That divergence is fine and it is exactly
// why the launch needs its own rule, stated once.
func TestNoHostCrossingClaimIsDisclosedForAnUnapprovedPack(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		body     string
		fragment string
		ungated  bool
	}{
		{"reads-host", `{"kind":"reads-host","host":".netrc"}`, ".netrc", false},
		{"mount", `{"kind":"mount","host":"datasets/acme","into":"acme"}`, "datasets/acme", false},
		{"program installer", `{"kind":"program","bin":"acme","via":"installer",` +
			`"url":"https://acme.test/i.sh"}`, "acme.test/i.sh", false},
		{"briefing after host", `{"kind":"briefing","into":"AGENTS.md","after":"host:AGENTS.md"}`,
			"host:AGENTS.md", false},
		// The named exception: `env` is not origin-gated anywhere, so it still happens.
		{"env", `{"kind":"env","vars":{"ACME":"1"}}`, "ACME", true},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "pack.json"),
				[]byte(`{"contributes":[`+tc.body+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			p, probs := packload.LoadDir(root, "acme", false)
			if len(probs) > 0 {
				t.Fatalf("fixture: %v", probs)
			}
			var joined string
			for _, class := range []disclosureClass{disclosureRead, disclosureExec} {
				joined += renderLines(disclosedClaims([]*packload.Pack{p}, class))
			}
			present := strings.Contains(joined, tc.fragment)
			if tc.ungated && !present {
				t.Errorf("%s is not origin-gated, so an unapproved pack still gets it and the "+
					"launch must still say so:\n%s", tc.kind, joined)
			}
			if !tc.ungated && present {
				t.Errorf("the launch disclosed a REFUSED %s claim as though it were "+
					"happening:\n%s", tc.kind, joined)
			}
		})
	}
}

// And end to end at the spawn boundary: the launch says the loophole was REFUSED and does not
// say its daemon is about to run. Both halves in one output, because the defect was that a
// reader could not tell one from the other.
func TestSpawnBoundarySaysRefusedNotPending(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	p := writeRealLoopholePack(t, "acme", "acme-proxy", `{
		"name": "acme-proxy",
		"transport": "none",
		"host_daemon": {"cmd": ["python3", "{loophole_dir}/acme-daemon.py"], "publishes": "socket"}
	}`)
	p.MayAccessHost = false

	cname := "yolo-refused-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = &errBuf
	o.PathExists = func(string) bool { return false }
	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p})

	got := errBuf.String()
	if strings.Contains(got, "runs pack code on your machine") {
		t.Errorf("the launch announced host execution for a REFUSED loophole:\n%s", got)
	}
	if strings.Contains(got, "acme-daemon.py") {
		t.Errorf("the launch printed a refused daemon's argv as though it were happening:\n%s", got)
	}
}
