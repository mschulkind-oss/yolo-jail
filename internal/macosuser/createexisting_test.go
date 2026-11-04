package macosuser

import (
	"bytes"
	"strings"
	"testing"
)

// createexisting_test.go pins `yolo yolo-account create` on a Mac already set up (W-SU5 of
// docs/design/named-setups-plan.md): no sudo that can prompt, exit 0, and the sudo path only
// when a step is known to need root.

// setUpMacDeps is mockDeps on a Mac `yolo yolo-account create` has finished on: the account
// and its home, the shared root (yours, group-only, with its inheriting ACL), the staging and
// capture roots yours, and the run-as rule granting with no password. Every Run is recorded and
// exits 0, except probeRC for the grant probe.
func setUpMacDeps(rec *[]string, out *bytes.Buffer, probeRC int) Deps {
	d := mockDeps(rec)
	d.Out = out
	d.StagingRootFacts = func(p string) StagingRootFacts {
		if p == SharedRootDefault() {
			return StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o770, HasACL: true}
		}
		return StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o755}
	}
	probe := strings.Join(grantProbeArgv(), " ")
	d.Run = func(argv []string) int {
		joined := strings.Join(argv, " ")
		*rec = append(*rec, "run:"+joined)
		if joined == probe {
			return probeRC
		}
		return 0
	}
	// The rule file is root's 0440, so the invoking user cannot read it.
	d.ReadFile = func(p string) (string, bool) { return "", false }
	return d
}

// promptingSudo is each recorded sudo that could ask for a password: one without -n.
func promptingSudo(rec []string) []string {
	var out []string
	for _, r := range rec {
		argv := strings.Fields(strings.TrimPrefix(r, "run:"))
		if len(argv) > 0 && argv[0] == sudoBin && (len(argv) < 2 || argv[1] != "-n") {
			out = append(out, r)
		}
	}
	return out
}

func TestCreateOnASetUpMacAsksForNoPasswordAndExitsZero(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := setUpMacDeps(&rec, &buf, 0)
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d, want 0 on a Mac already set up\n%s", rc, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "The yolo account _yolojail already exists.") {
		t.Errorf("the witness's line is missing:\n%s", out)
	}
	if p := promptingSudo(rec); len(p) > 0 {
		t.Errorf("ran a sudo that can prompt for a password:\n%s", strings.Join(p, "\n"))
	}
	if recAt(rec, strings.Join(grantProbeArgv(), " ")) < 0 {
		t.Errorf("the run-as grant was not probed:\n%s", strings.Join(rec, "\n"))
	}
	if strings.Contains(out, "sudo needs your password") || strings.Contains(out, "sudo did not run") {
		t.Errorf("announced or failed a password prompt:\n%s", out)
	}
	// The rule's text could not be read, so the next step names the command that re-checks it.
	if !strings.Contains(out, RecheckCommand) {
		t.Errorf("an unverified rule must name %q:\n%s", RecheckCommand, out)
	}
	if !strings.Contains(out, "macos-user backend ready") {
		t.Errorf("no ready verdict:\n%s", out)
	}
}

// The probe ignores sudo's cached credentials (-k with a command) without clearing them.
func TestTheExistingSetupProbeIgnoresTheCredentialCache(t *testing.T) {
	want := "/usr/bin/sudo -n -k --user=_yolojail /usr/bin/env /usr/bin/true"
	if got := strings.Join(grantProbeArgv(), " "); got != want {
		t.Errorf("probe = %q, want %q", got, want)
	}
}

func TestCreateOnASetUpMacWithAReadableRuleNamesNoRecheck(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := setUpMacDeps(&rec, &buf, 0)
	rule, err := SudoersRule("matt")
	if err != nil {
		t.Fatal(err)
	}
	d.ReadFile = func(p string) (string, bool) {
		if p == SudoersFile {
			return rule, true
		}
		return "", false
	}
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	if strings.Contains(buf.String(), RecheckCommand) {
		t.Errorf("a verified rule needs no re-check:\n%s", buf.String())
	}
	if p := promptingSudo(rec); len(p) > 0 {
		t.Errorf("ran a sudo that can prompt:\n%s", strings.Join(p, "\n"))
	}
}

// Each state known to need root takes the sudo path, which asks about the existing rule first.
func TestCreateTakesTheSudoPathWhenAStepIsKnownToNeedRoot(t *testing.T) {
	for _, tc := range []struct {
		name, why string
		tweak     func(*Deps)
		probeRC   int
	}{
		{name: "no run-as grant", why: "sudo does not run commands as _yolojail", probeRC: 1},
		{name: "the rule is another text", why: "is missing or is not this yolo's rule",
			tweak: func(d *Deps) {
				d.ReadFile = func(string) (string, bool) { return "someone ALL=(ALL) ALL\n", true }
			}},
		{name: "the rule file is absent", why: "is missing or is not this yolo's rule",
			tweak: func(d *Deps) { d.ReadFile = func(string) (string, bool) { return "", true } }},
		{name: "the home is missing", why: "its home /Users/_yolojail is missing",
			tweak: func(d *Deps) { d.PathIsDir = func(p string) bool { return p != SandboxHome() } }},
		{name: "the shared root has no ACL", why: "the shared root",
			tweak: func(d *Deps) {
				d.StagingRootFacts = func(string) StagingRootFacts {
					return StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o755}
				}
			}},
		{name: "the capture root is root's", why: CaptureRootDefault(),
			tweak: func(d *Deps) {
				facts := d.StagingRootFacts
				d.StagingRootFacts = func(p string) StagingRootFacts {
					if p == CaptureRootDefault() {
						return StagingRootFacts{Exists: true, IsDir: true, UID: 0, Mode: 0o755}
					}
					return facts(p)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec []string
			var buf bytes.Buffer
			d := setUpMacDeps(&rec, &buf, tc.probeRC)
			if tc.tweak != nil {
				tc.tweak(&d)
			}
			AccountCreate(d)
			if recAt(rec, "/usr/bin/sudo /usr/bin/grep -s -qxF") < 0 {
				t.Errorf("did not take the sudo path:\n%s", strings.Join(rec, "\n"))
			}
			if !strings.Contains(buf.String(), tc.why) {
				t.Errorf("does not say why setup needs the password (%q):\n%s", tc.why, buf.String())
			}
		})
	}
}

// --recheck takes the sudo path on a Mac that needs nothing.
func TestCreateRecheckTakesTheSudoPath(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := setUpMacDeps(&rec, &buf, 0)
	if rc := AccountCreateWith(d, CreateOptions{Recheck: true}); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	for _, want := range []string{"/usr/bin/sudo /usr/bin/grep -s -qxF", "/usr/sbin/visudo -c -f"} {
		if recAt(rec, want) < 0 {
			t.Errorf("--recheck did not run %q:\n%s", want, strings.Join(rec, "\n"))
		}
	}
}
