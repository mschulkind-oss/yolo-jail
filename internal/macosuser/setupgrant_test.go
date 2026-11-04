package macosuser

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// setupgrant_test.go pins `yolo yolo-account create`'s administrator act G0 for the one run-as rule
// (task 7 of docs/design/macos-user-privilege-lifecycle-plan.md) and its undo in
// `yolo yolo-account remove`.

func TestSetupInstallsTheRuleThenProvesItWithAnEmptyCache(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.StagingRootFacts = func(string) StagingRootFacts { return StagingRootFacts{} } // fresh Mac
	d.HostUser = func() string { return "matt" }
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	order := []string{
		"run:/usr/bin/sudo /usr/sbin/visudo -c -f /etc/sudoers.d/.yolo-jail.new",
		"run:/usr/bin/sudo /bin/mv -f /etc/sudoers.d/.yolo-jail.new /etc/sudoers.d/yolo-jail",
		"run:/usr/bin/sudo /usr/sbin/visudo -c",
		"run:/usr/bin/sudo -k",
		"run:/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true",
	}
	last := -1
	for _, w := range order {
		i := slices.Index(rec, w)
		if i <= last {
			t.Fatalf("%q missing or out of order:\n%s", w, strings.Join(rec, "\n"))
		}
		last = i
	}
}

func TestSetupRefusesASecondEnrolledUserBeforeAnySudo(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.StagingRootFacts = func(string) StagingRootFacts {
		return StagingRootFacts{Exists: true, IsDir: true, UID: 502, Mode: 0o755}
	}
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "sudo") >= 0 {
		t.Errorf("ran sudo before refusing: %v", rec)
	}
	if !strings.Contains(buf.String(), "yolo yolo-account remove") {
		t.Errorf("the refusal names no remedy:\n%s", buf.String())
	}
}

// stubAccountNames makes the owner lookup answer from names, for the length of the test.
func stubAccountNames(t *testing.T, names map[uint32]string) {
	t.Helper()
	prev := accountNameOfUID
	accountNameOfUID = func(uid uint32) string { return names[uid] }
	t.Cleanup(func() { accountNameOfUID = prev })
}

// A root the yolo account made first is a squat, not an enrollment: setup refuses it without
// adopting it, before any sudo, and names how to inspect and remove it.
func TestSetupRefusesAStagingRootTheSandboxAccountSquatted(t *testing.T) {
	stubAccountNames(t, map[uint32]string{601: SandboxUser})
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.StagingRootFacts = func(string) StagingRootFacts {
		return StagingRootFacts{Exists: true, IsDir: true, UID: 601, Mode: 0o755}
	}
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "sudo") >= 0 {
		t.Errorf("ran sudo before refusing: %v", rec)
	}
	want := "/Users/Shared/yolo-staged already exists and belongs to _yolojail, not to you. Setup does " +
		"not adopt it: whoever owns it controls what the sandbox reads. Inspect it with " +
		"`ls -leR /Users/Shared/yolo-staged`, remove it with `sudo rm -rf /Users/Shared/yolo-staged`, " +
		"then run yolo yolo-account create again."
	if !strings.Contains(buf.String(), want) {
		t.Errorf("squat refusal:\n%s\nwant %q", buf.String(), want)
	}
}

// grepRun is mockDeps' Run with the existing-rule check answering grepRC and the root -n probe
// answering rootRC.
func grepRun(rec *[]string, grepRC, rootRC int) func([]string) int {
	return func(argv []string) int {
		joined := strings.Join(argv, " ")
		*rec = append(*rec, "run:"+joined)
		switch {
		case strings.HasPrefix(joined, "/usr/bin/sudo /usr/bin/grep -s -qxF"):
			return grepRC
		case joined == "/usr/bin/sudo -n /usr/bin/true":
			return rootRC
		}
		return 0
	}
}

// A rule in /etc/sudoers.d/yolo-jail that is not this user's (grep exits 1 while sudo itself
// ran) is another macOS user's enrollment: refused before anything is created or installed.
func TestSetupRefusesWhenTheInstalledRuleNamesAnotherUser(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.Run = grepRun(&rec, 1, 0)
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if i := slices.Index(rec, "run:/usr/bin/sudo /usr/bin/grep -s -qxF -e matt ALL=(_yolojail) "+
		"NOPASSWD: /usr/bin/env /etc/sudoers.d/yolo-jail"); i < 0 {
		t.Errorf("the existing rule was not asked about:\n%s", strings.Join(rec, "\n"))
	}
	for _, never := range []string{"visudo", "dscl", "/bin/mkdir", "write:"} {
		if recAt(rec, never) >= 0 {
			t.Errorf("went on after refusing (%s):\n%s", never, strings.Join(rec, "\n"))
		}
	}
	out := buf.String()
	if !strings.Contains(out, "This Mac's macos-user backend is set up for another macOS user") ||
		!strings.Contains(out, "That user runs yolo yolo-account remove, then you run yolo yolo-account create.") {
		t.Errorf("second-user refusal:\n%s", out)
	}
}

// A grep exit of 1 that sudo produced (the password was not given) is not another user's rule:
// setup stops, changes nothing, and does not tell the user someone else is enrolled.
func TestSetupDoesNotMistakeAFailedSudoForAnotherUsersRule(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.Run = grepRun(&rec, 1, 1)
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if strings.Contains(buf.String(), "another macOS user") {
		t.Errorf("blamed another user for a failed sudo:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "yolo yolo-account create") || recAt(rec, "visudo") >= 0 {
		t.Errorf("printed %q and ran %v", buf.String(), rec)
	}
}

// The rule setup copies into place is exactly SudoersRule's, staged under the inert dot name,
// owned root:wheel 0440 before visudo checks it.
func TestSetupStagesExactlyTheRuleUnderTheInertName(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var tmp, content string
	d.WriteHostFile = func(path, c string, _ os.FileMode) bool {
		rec = append(rec, "write:"+path)
		tmp, content = path, c
		return true
	}
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	want, _ := SudoersRule("matt")
	if content != want {
		t.Errorf("rule written =\n%s\nwant\n%s", content, want)
	}
	order := []string{
		"write:" + tmp,
		"run:/usr/bin/sudo /bin/cp " + tmp + " /etc/sudoers.d/.yolo-jail.new",
		"run:/usr/bin/sudo /usr/sbin/chown root:wheel /etc/sudoers.d/.yolo-jail.new",
		"run:/usr/bin/sudo /bin/chmod 0440 /etc/sudoers.d/.yolo-jail.new",
		"run:/usr/bin/sudo /usr/sbin/visudo -c -f /etc/sudoers.d/.yolo-jail.new",
	}
	last := -1
	for _, w := range order {
		i := slices.Index(rec, w)
		if i <= last {
			t.Fatalf("%q missing or out of order:\n%s", w, strings.Join(rec, "\n"))
		}
		last = i
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("the host temp file %s was left behind (%v)", tmp, err)
	}
}

// captureVia gives d a RunCapture that answers through d.Run (so the recording and the exit
// codes stay the test's) and prints output[joined] for the command, the way visudo would.
func captureVia(d *Deps, output map[string]string) {
	run := d.Run
	d.RunCapture = func(argv []string) (string, int) {
		return output[strings.Join(argv, " ")], run(argv)
	}
}

const (
	visudoRuleCheck   = "/usr/bin/sudo /usr/sbin/visudo -c -f " + SudoersStagingFile
	visudoConfigCheck = "/usr/bin/sudo /usr/sbin/visudo -c"
)

// indented is s as the refusals print captured text and the rule: each line after two spaces.
func indented(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}

// A passing check stays quiet: visudo's "parsed OK" lines are not the user's business.
func TestSetupDoesNotPrintVisudoOutputWhenTheChecksPass(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	captureVia(&d, map[string]string{
		visudoRuleCheck:   SudoersStagingFile + ": parsed OK\n",
		visudoConfigCheck: "/etc/sudoers: parsed OK\n",
	})
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	if strings.Contains(buf.String(), "parsed OK") {
		t.Errorf("printed visudo's output for a passing check:\n%s", buf.String())
	}
}

// Production wires the capture: without it every check would print through the terminal.
func TestRealDepsCaptureVisudoOutput(t *testing.T) {
	if RealDeps(nil, nil, false).RunCapture == nil {
		t.Fatal("RealDeps has no RunCapture, so visudo's checks are not quiet on success")
	}
}

func TestSetupInstallsNothingWhenVisudoRejectsTheRule(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.Run = func(argv []string) int {
		joined := strings.Join(argv, " ")
		rec = append(rec, "run:"+joined)
		if joined == visudoRuleCheck {
			return 1
		}
		return 0
	}
	reason := ">>> " + SudoersStagingFile + ": syntax error near line 3 <<<\n"
	captureVia(&d, map[string]string{visudoRuleCheck: reason})
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "/bin/mv") >= 0 || recAt(rec, "-n --user=_yolojail") >= 0 {
		t.Errorf("went on after visudo rejected the rule:\n%s", strings.Join(rec, "\n"))
	}
	if slices.Index(rec, "run:/usr/bin/sudo /bin/rm -f "+SudoersStagingFile) < 0 {
		t.Errorf("the rejected staging file was not removed:\n%s", strings.Join(rec, "\n"))
	}
	out := buf.String()
	rule, _ := SudoersRule("matt")
	for _, want := range []string{
		"✗ visudo rejected yolo's sudo rule. Nothing was installed.\n\n" + indented(reason) + "\n",
		"This is a yolo bug. Report it with the output above and this rule:\n\n" + indented(rule) +
			"\n  https://github.com/mschulkind-oss/yolo-jail/issues\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("visudo refusal lacks %q:\n%s", want, out)
		}
	}
}

// wholeCheckRun is mockDeps' Run with the whole-configuration check (`visudo -c`) answering
// withRule while the rule is in place and withoutRule once setup has parked it under the inert
// staging name (or removed it).
func wholeCheckRun(rec *[]string, withRule, withoutRule int) func([]string) int {
	return wholeCheck(rec, withRule, withoutRule, nil, "", "").run
}

type wholeCheckFake struct {
	installed bool
	run       func([]string) int
}

// wholeCheck builds the fake and, when d is non-nil, wires d's Run and RunCapture to it, the
// capture answering withOut while the rule is in place and withoutOut once it is out.
func wholeCheck(rec *[]string, withRule, withoutRule int, d *Deps, withOut, withoutOut string) *wholeCheckFake {
	f := &wholeCheckFake{}
	f.run = func(argv []string) int {
		joined := strings.Join(argv, " ")
		*rec = append(*rec, "run:"+joined)
		switch joined {
		case "/usr/bin/sudo /bin/mv -f " + SudoersStagingFile + " " + SudoersFile:
			f.installed = true
		case "/usr/bin/sudo /bin/mv -f " + SudoersFile + " " + SudoersStagingFile,
			"/usr/bin/sudo /bin/rm -f " + SudoersFile:
			f.installed = false
		case visudoConfigCheck:
			if f.installed {
				return withRule
			}
			return withoutRule
		}
		return 0
	}
	if d != nil {
		d.Run = f.run
		d.RunCapture = func(argv []string) (string, int) {
			installed := f.installed
			rc := f.run(argv)
			if strings.Join(argv, " ") != visudoConfigCheck {
				return "", rc
			}
			if installed {
				return withOut, rc
			}
			return withoutOut, rc
		}
	}
	return f
}

// inOrder fails t unless every entry of want appears in rec, in order (repeats allowed).
func inOrder(t *testing.T, rec, want []string) {
	t.Helper()
	last := -1
	for _, w := range want {
		i := slices.Index(rec[last+1:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order:\n%s", w, strings.Join(rec, "\n"))
		}
		last += 1 + i
	}
}

const (
	parkRule   = "run:/usr/bin/sudo /bin/mv -f " + SudoersFile + " " + SudoersStagingFile
	unparkRule = "run:/usr/bin/sudo /bin/mv -f " + SudoersStagingFile + " " + SudoersFile
)

// The maintainer's Mac (2026-10-16): two sudoers.d files at a mode other than 0440 fail
// `visudo -c` whether or not yolo's rule is there, while sudo itself reads them. yolo's rule adds
// no failure, so setup installs it, goes on to prove it, and warns with the one chmod that fixes
// both files, at the paths visudo printed.
func TestSetupInstallsTheRuleAndWarnsWhenTheConfigFailsTheSameWithoutIt(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	without := "/private/etc/sudoers.d/00-matt: bad permissions, should be mode 0440\n" +
		"/private/etc/sudoers.d/50-nopasswd-for-sandvault-matt: bad permissions, should be mode 0440\n" +
		"/private/etc/sudoers: parsed OK\n"
	with := without + "/private/etc/sudoers.d/yolo-jail: parsed OK\n"
	wholeCheck(&rec, 1, 1, &d, with, without)
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	inOrder(t, rec, []string{
		unparkRule,
		"run:" + visudoConfigCheck,
		parkRule,
		"run:" + visudoConfigCheck,
		unparkRule,
		"run:/usr/bin/sudo -k",
		"run:/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true",
	})
	want := "\n⚠ Installed /etc/sudoers.d/yolo-jail. visudo -c also fails without it:\n\n" +
		"  /private/etc/sudoers.d/00-matt: bad permissions, should be mode 0440\n" +
		"  /private/etc/sudoers.d/50-nopasswd-for-sandvault-matt: bad permissions, should be mode 0440\n\n" +
		"  sudo chmod 0440 /private/etc/sudoers.d/00-matt /private/etc/sudoers.d/50-nopasswd-for-sandvault-matt\n" +
		"  sudo visudo -c\n\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("warning lacks\n%s\ngot:\n%s", want, buf.String())
	}
	if strings.Contains(buf.String(), "✗") {
		t.Errorf("refused or failed a step:\n%s", buf.String())
	}
}

// Moving the parked rule back after the comparison accepted it fails: a stop saying the rule is
// not installed, the pre-existing complaints, their fixes and the re-run, and no probe.
func TestSetupStopsWhenTheParkedRuleCannotMoveBack(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	without := "/private/etc/sudoers.d/00-matt: bad permissions, should be mode 0440\n"
	f := wholeCheck(&rec, 1, 1, &d, without, without)
	moves := 0
	d.Run = func(argv []string) int {
		if "run:"+strings.Join(argv, " ") == unparkRule {
			if moves++; moves == 2 {
				rec = append(rec, unparkRule)
				return 1
			}
		}
		return f.run(argv)
	}
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	if recAt(rec, "-n --user=_yolojail") >= 0 {
		t.Errorf("went on to the probe:\n%s", strings.Join(rec, "\n"))
	}
	if !strings.HasSuffix(strings.Join(rec, "\n"), "run:/usr/bin/sudo /bin/rm -f "+SudoersStagingFile) {
		t.Errorf("the parked rule was not removed last:\n%s", strings.Join(rec, "\n"))
	}
	want := "\n✗ yolo's sudo rule is not installed: " + unparkRule[len("run:"):] + " failed.\n\n" +
		indented(without) + "\n" +
		"  sudo chmod 0440 /private/etc/sudoers.d/00-matt\n" +
		"  yolo yolo-account create\n\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("stop lacks\n%s\ngot:\n%s", want, buf.String())
	}
}

// The first move into place fails: the same stop, with only the re-run.
func TestSetupStopsWhenTheRuleCannotMoveIntoPlace(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.Run = func(argv []string) int {
		joined := strings.Join(argv, " ")
		rec = append(rec, "run:"+joined)
		if "run:"+joined == unparkRule {
			return 1
		}
		return 0
	}
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	want := "\n✗ yolo's sudo rule is not installed: " + unparkRule[len("run:"):] + " failed.\n\n" +
		"  yolo yolo-account create\n\n"
	if !strings.Contains(buf.String(), want) || slices.Contains(rec, "run:"+visudoConfigCheck) {
		t.Errorf("stop lacks\n%s\ngot:\n%s\nran:\n%s", want, buf.String(), strings.Join(rec, "\n"))
	}
}

// Each kind of pre-existing problem gets its own fix: a bad mode one chmod for all its files, a
// wrong owner one chown, anything else a visudo -f per file.
func TestSudoersRemediesPerKindOfProblem(t *testing.T) {
	out := "/private/etc/sudoers.d/a: wrong owner (uid, gid) should be (0, 0)\n" +
		"/private/etc/sudoers.d/b: wrong owner (uid, gid) should be (0, 0)\n" +
		"/private/etc/sudoers.d/c: bad permissions, should be mode 0440\n" +
		">>> /etc/sudoers.d/corp: syntax error near line 3 <<<\n" +
		"/etc/sudoers.d/other:2:5: syntax error\n" +
		"visudo: /etc/sudoers.d/third: bad permissions, should be mode 0440\n" +
		"/etc/sudoers: parsed OK\n"
	got := sudoersRemedies(sudoersComplaints(out))
	want := []string{
		"sudo chmod 0440 /private/etc/sudoers.d/c /etc/sudoers.d/third",
		"sudo chown root:wheel /private/etc/sudoers.d/a /private/etc/sudoers.d/b",
		"sudo visudo -f /etc/sudoers.d/corp",
		"sudo visudo -f /etc/sudoers.d/other",
	}
	if !slices.Equal(got, want) {
		t.Errorf("remedies =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// yolo's rule adds a failure the configuration does not have without it: refused, the rule taken
// back out, and only the added failure shown, as a yolo bug.
func TestSetupRefusesWhenTheRuleAddsAFailure(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	without := "/private/etc/sudoers.d/00-matt: bad permissions, should be mode 0440\n"
	added := ">>> /private/etc/sudoers.d/yolo-jail: syntax error near line 1 <<<\n"
	wholeCheck(&rec, 1, 1, &d, without+added, without)
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	inOrder(t, rec, []string{
		unparkRule,
		"run:" + visudoConfigCheck,
		parkRule,
		"run:" + visudoConfigCheck,
		"run:/usr/bin/sudo /bin/rm -f " + SudoersStagingFile,
	})
	if strings.Count(strings.Join(rec, "\n"), unparkRule) != 1 || recAt(rec, "-n --user=_yolojail") >= 0 {
		t.Errorf("went on after the rule added a failure:\n%s", strings.Join(rec, "\n"))
	}
	out := buf.String()
	want := "✗ visudo rejected your sudo configuration with yolo's rule in place. The rule was " +
		"removed again.\n\n" + indented(added) + "\nThis is a yolo bug."
	if !strings.Contains(out, want) {
		t.Errorf("refusal lacks\n%s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, "00-matt") {
		t.Errorf("listed a pre-existing problem as the rule's:\n%s", out)
	}
}

// With no output to compare (both checks fail with nothing captured), setup cannot tell whether
// the rule adds a failure, so it refuses and names the fix.
func TestSetupRefusesWhenTheChecksFailWithNothingToCompare(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	wholeCheck(&rec, 1, 1, &d, "", "")
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "-n --user=_yolojail") >= 0 {
		t.Errorf("went on:\n%s", strings.Join(rec, "\n"))
	}
	want := "✗ Your sudo configuration is invalid without yolo's rule too. Nothing was installed.\n\n" +
		"Fix it, then run setup again:\n\n  sudo visudo -c\n  yolo yolo-account create\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("refusal lacks\n%s\ngot:\n%s", want, buf.String())
	}
}

// The whole configuration fails only with the rule in place: that IS yolo's bug.
func TestSetupCallsItAYoloBugWhenOnlyTheRuleBreaksTheConfig(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.Run = wholeCheckRun(&rec, 1, 0)
	reason := ">>> " + SudoersFile + ": syntax error near line 3 <<<\n"
	captureVia(&d, map[string]string{visudoConfigCheck: reason})
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if slices.Index(rec, "run:/usr/bin/sudo /bin/rm -f "+SudoersStagingFile) < 0 {
		t.Errorf("the parked rule was not removed:\n%s", strings.Join(rec, "\n"))
	}
	out := buf.String()
	rule, _ := SudoersRule("matt")
	for _, want := range []string{
		"✗ visudo rejected your sudo configuration with yolo's rule in place. The rule was " +
			"removed again.\n\n" + indented(reason) + "\n",
		"This is a yolo bug. Report it with the output above and this rule:\n\n" + indented(rule),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal lacks %q:\n%s", want, out)
		}
	}
}

// When taking a rule the whole check rejected back out fails, the rule is still in place, and
// sudo itself may be what broke: the refusal gives the sudo removal, a fallback that needs no
// sudo, and the re-run, each on its own line.
func TestSetupNamesTheRemovalWhenItCannotTakeTheRuleBackOut(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	inner := wholeCheckRun(&rec, 1, 0)
	d.Run = func(argv []string) int {
		if "run:"+strings.Join(argv, " ") == parkRule {
			rec = append(rec, parkRule)
			return 1
		}
		return inner(argv)
	}
	captureVia(&d, nil)
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	out := buf.String()
	want := "Remove the rule, then run setup again:\n\n" +
		"  sudo rm -f /etc/sudoers.d/yolo-jail\n" +
		"  # or, if sudo itself fails:\n" +
		"  osascript -e 'do shell script \"/bin/rm -f /etc/sudoers.d/yolo-jail\" with administrator privileges'\n" +
		"  yolo yolo-account create\n"
	if !strings.Contains(out, "removing the rule failed") || !strings.Contains(out, want) {
		t.Errorf("refusal lacks\n%s\ngot:\n%s", want, out)
	}
}

// recWriter records each line written to it into rec, so a test can place output among the
// commands run.
type recWriter struct{ rec *[]string }

func (w recWriter) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		*w.rec = append(*w.rec, "out:"+l)
	}
	return len(p), nil
}

// firstPromptingSudo is the index of the first sudo in rec that may prompt (not `sudo -n`).
func firstPromptingSudo(rec []string) int {
	return slices.IndexFunc(rec, func(r string) bool {
		return strings.HasPrefix(r, "run:/usr/bin/sudo ") && !strings.HasPrefix(r, "run:/usr/bin/sudo -n ")
	})
}

// sudoNotCached answers the `sudo -n true` probe with 1: sudo has no cached credentials.
func sudoNotCached(rec *[]string) func([]string) int {
	return func(argv []string) int {
		joined := strings.Join(argv, " ")
		*rec = append(*rec, "run:"+joined)
		if joined == "/usr/bin/sudo -n /usr/bin/true" {
			return 1
		}
		return 0
	}
}

// The password prompt sudo prints is explained by the line before it.
func TestSetupSaysWhySudoPromptsBeforeTheFirstSudo(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.Out = recWriter{&rec}
	d.Run = sudoNotCached(&rec)
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, strings.Join(rec, "\n"))
	}
	line := "out:• sudo needs your password to set up the yolo account and its rule (" +
		SudoersFile + ")."
	at, first := slices.Index(rec, line), firstPromptingSudo(rec)
	if at < 0 || first < 0 || at > first {
		t.Errorf("line at %d, first prompting sudo at %d:\n%s", at, first, strings.Join(rec, "\n"))
	}
}

// Cached credentials mean no prompt, so no line announcing one.
func TestSetupSaysNothingAboutAPromptWhenSudoIsCached(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if strings.Contains(buf.String(), "sudo needs your password") {
		t.Errorf("announced a prompt sudo will not show:\n%s", buf.String())
	}
}

func TestTeardownSaysWhySudoPromptsBeforeTheFirstSudo(t *testing.T) {
	for _, exists := range []bool{true, false} {
		var rec []string
		d := mockDeps(&rec)
		d.Out = recWriter{&rec}
		d.Run = sudoNotCached(&rec)
		d.SandboxUserExists = func() bool { return exists }
		d.StagingRootFacts = func(string) StagingRootFacts {
			return StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o755}
		}
		if rc := AccountRemove(d); rc != 0 {
			t.Fatalf("rc = %d", rc)
		}
		at := slices.IndexFunc(rec, func(r string) bool {
			return strings.HasPrefix(r, "out:• sudo needs your password to remove")
		})
		first := firstPromptingSudo(rec)
		if at < 0 || first < 0 || at > first {
			t.Errorf("account exists %v: line at %d, first prompting sudo at %d:\n%s",
				exists, at, first, strings.Join(rec, "\n"))
		}
	}
}

// A first run has no rule file: the existing-rule check must not print grep's complaint about it.
func TestSetupAsksAboutTheExistingRuleSilently(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "/usr/bin/grep -s -qxF") < 0 {
		t.Errorf("the existing-rule grep is not silenced with -s:\n%s", strings.Join(rec, "\n"))
	}
}

func TestSetupRefusesWhenTheProbeFailsAfterTheInstall(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	probe := strings.Join(RunAsGrantProbeArgv(), " ")
	d.Run = func(argv []string) int {
		joined := strings.Join(argv, " ")
		rec = append(rec, "run:"+joined)
		if joined == probe {
			return 1
		}
		return 0
	}
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "✗ The rule is installed, but sudo still asks for a password to run as "+
		"_yolojail. Check that /etc/sudoers reads /private/etc/sudoers.d "+
		"(`sudo grep -n includedir /etc/sudoers`)") || strings.Contains(out, "backend ready") {
		t.Errorf("probe-failure verdict:\n%s", out)
	}
}

func TestSetupRefusesAUserNameItCannotWriteIntoTheRule(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.HostUser = func() string { return "m att" }
	if rc := AccountCreate(d); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "sudo") >= 0 {
		t.Errorf("ran sudo for a name it cannot use: %v", rec)
	}
	want := "Your user name m att cannot be written into a sudo rule safely. The macos-user backend " +
		"supports names of letters, digits, '_', '.' and '-'."
	if !strings.Contains(buf.String(), want) {
		t.Errorf("unsafe-name refusal:\n%s", buf.String())
	}
}

// The ready verdict says launches need no password, where it used to say sudo prompts per run.
func TestSetupsReadyVerdictPromisesNoPassword(t *testing.T) {
	var buf bytes.Buffer
	d := mockDeps(nil)
	d.Out = &buf
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "Launches start and stop with no password from now on.") ||
		strings.Contains(out, "sudo will prompt per run") {
		t.Errorf("verdict:\n%s", out)
	}
	if !strings.Contains(out, "Granting matt one standing right: run commands as _yolojail, never "+
		"as root, without a password (/etc/sudoers.d/yolo-jail). Launches then start and stop with "+
		"no prompt.") {
		t.Errorf("setup did not disclose the standing right:\n%s", out)
	}
}

// legacyKey is a session key an older yolo's launch would have recorded.
func legacyKey(id string) string { return SessionKey("proj-0123abcd", id) }

// PL-8: setup removes, with sudo, exactly the five legacy files of each session whose record is
// free AND whose legacy profile exists, then the record. A live session's record and files, and a
// gone session that left nothing in the legacy dir, are not setup's to touch.
func TestSetupRemovesOnlyTheLegacyFilesOfEndedOlderSessions(t *testing.T) {
	dir := t.TempDir()
	gone := legacyKey("00000000000000a1")   // ended, older yolo: setup's
	ours := legacyKey("00000000000000b2")   // ended, this yolo: the launch sweep's
	living := legacyKey("00000000000000c3") // still running under its old launcher
	for _, k := range []string{gone, ours} {
		if err := os.WriteFile(filepath.Join(dir, k+sessionRecordSuffix), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	held, err := openSessionRecord(dir, living)
	if err != nil {
		t.Fatal(err)
	}
	defer held.close()

	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.SessionRecordDir = func() string { return dir }
	d.PathExists = func(p string) bool {
		return p == SessionProfilePath(gone, LegacyStateDir()) ||
			p == SessionProfilePath(living, LegacyStateDir())
	}
	if rc := AccountCreate(d); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	want := "run:/usr/bin/sudo /bin/rm -f " + strings.Join(SessionFilePaths(gone, LegacyStateDir()), " ")
	if slices.Index(rec, want) < 0 {
		t.Errorf("the ended older session's legacy files were not removed:\n%s", strings.Join(rec, "\n"))
	}
	for _, k := range []string{ours, living} {
		if recAt(rec, k) >= 0 {
			t.Errorf("touched session %s:\n%s", k, strings.Join(rec, "\n"))
		}
	}
	if recAt(rec, "/var/yolo-jail/bin") >= 0 || recAt(rec, "rm -rf /var/yolo-jail") >= 0 {
		t.Errorf("touched a legacy tree:\n%s", strings.Join(rec, "\n"))
	}
	if _, err := os.Stat(filepath.Join(dir, gone+sessionRecordSuffix)); !os.IsNotExist(err) {
		t.Errorf("the swept record is still there (%v)", err)
	}
	for _, k := range []string{ours, living} {
		if _, err := os.Stat(filepath.Join(dir, k+sessionRecordSuffix)); err != nil {
			t.Errorf("record %s removed: %v", k, err)
		}
	}
	if !strings.Contains(buf.String(), "yolo stores") {
		t.Errorf("setup does not point at `yolo stores` for the legacy trees:\n%s", buf.String())
	}
}

func TestTeardownRemovesTheRuleAndTheStagingRootButNotTheLegacyDir(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.PathIsDir = func(p string) bool { return p == LegacyStateDir() }
	if rc := AccountRemove(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	order := []string{"run:/usr/bin/sudo /bin/rm -f /etc/sudoers.d/yolo-jail"}
	for _, c := range removeSealedCommands(StagingRoot()) {
		order = append(order, "run:"+strings.Join(c, " "))
	}
	last := -1
	for _, w := range order {
		i := slices.Index(rec, w)
		if i <= last {
			t.Fatalf("%q missing or out of order:\n%s", w, strings.Join(rec, "\n"))
		}
		last = i
	}
	if strings.Contains(rec[slices.Index(rec, order[len(order)-1])], "sudo") {
		t.Errorf("the staging root was removed with sudo")
	}
	if recAt(rec, LegacyStateDir()) >= 0 {
		t.Errorf("teardown touched %s:\n%s", LegacyStateDir(), strings.Join(rec, "\n"))
	}
	if !strings.Contains(buf.String(), "yolo stores") {
		t.Errorf("teardown does not say where the legacy dir is listed:\n%s", buf.String())
	}
}

// Teardown never removes a staging root another account owns: it is not this user's enrollment.
func TestTeardownLeavesAStagingRootItDoesNotOwn(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.StagingRootFacts = func(string) StagingRootFacts {
		return StagingRootFacts{Exists: true, IsDir: true, UID: 502, Mode: 0o755}
	}
	if rc := AccountRemove(d); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if recAt(rec, "rm -rf "+StagingRoot()) >= 0 {
		t.Errorf("removed another account's staging root:\n%s", strings.Join(rec, "\n"))
	}
}
