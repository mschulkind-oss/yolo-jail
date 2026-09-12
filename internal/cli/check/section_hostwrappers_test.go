package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostWrappersFixture sets up a temp HOME with a user config and (optionally) generated
// wrappers, then returns Options wired to a controllable PATH.
func hostWrappersFixture(t *testing.T, optIn bool, wrappers []string, pathEnv string) (*Options, *reporter, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"host_wrappers": false}`
	if optIn {
		body = `{"host_wrappers": true}`
	}
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if wrappers != nil {
		dir := filepath.Join(home, ".local", "share", "yolo-jail", "bin", "wrap")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, w := range wrappers {
			if err := os.WriteFile(filepath.Join(dir, w), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	var buf bytes.Buffer
	o := &Options{Getenv: func(k string) string {
		switch k {
		case "PATH":
			return pathEnv
		case "YOLO_VERSION":
			return "" // host
		}
		return ""
	}}
	return o, newReporter(&buf, false), &buf
}

func wrapDirIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "bin", "wrap")
}

// TestHostWrappersSilentWhenNotOptedIn: not opted in means no row at all. This is what
// keeps the feature from being a nag for everyone who never asked for it.
func TestHostWrappersSilentWhenNotOptedIn(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, false, nil, "/bin")
	o.sectionHostWrappers(r)
	if buf.Len() != 0 {
		t.Errorf("printed something without the opt-in:\n%s", buf.String())
	}
	if r.warned != 0 || r.passed != 0 {
		t.Errorf("counted a row without the opt-in: warned=%d passed=%d", r.warned, r.passed)
	}
}

// TestHostWrappersSilentInJail: the key is host-only, and a jail has neither a user shell
// nor the host's PATH.
func TestHostWrappersSilentInJail(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "/bin")
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return "/bin"
	}
	o.sectionHostWrappers(r)
	if buf.Len() != 0 {
		t.Errorf("printed something in-jail:\n%s", buf.String())
	}
}

// TestHostWrappersWarnsWhenNotOnPath is the row this section exists for: generated
// wrappers that nothing on PATH can reach are inert configuration, and it must be in the
// summary-COUNTED channel so it cannot be scrolled past.
func TestHostWrappersWarnsWhenNotOnPath(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude", "pi"}, "/bin:/usr/bin")
	o.sectionHostWrappers(r)
	out := buf.String()
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (it must be summary-counted)", r.warned)
	}
	if !strings.Contains(out, "[WARN]") {
		t.Errorf("no WARN badge:\n%s", out)
	}
	if !strings.Contains(out, "NOT on PATH") {
		t.Errorf("output does not say the dir is not on PATH:\n%s", out)
	}
	// The remedy must be present and must PREPEND — appending puts the wrapper behind
	// the real binary and it never runs.
	if !strings.Contains(out, `:$PATH"`) {
		t.Errorf("the remedy does not prepend:\n%s", out)
	}
	// And it must say the absolute-path escape hatch still works, which is the whole
	// reason wrappers are generated unconditionally.
	if !strings.Contains(out, "absolute path") {
		t.Errorf("output does not mention the absolute-path escape hatch:\n%s", out)
	}
}

func TestHostWrappersPassesWhenOnPath(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "")
	dir := wrapDirIn(t)
	o.Getenv = func(k string) string {
		if k == "PATH" {
			return "/bin:" + dir
		}
		return ""
	}
	o.sectionHostWrappers(r)
	// THREE passes: the wrap dir being on PATH, plus the two host-key rows that share this
	// section because they share its coverage boundary — hostManagementRow (who owns the
	// files this apply writes) and hostApplyOnLaunchRow (when a re-render is checked).
	if r.passed != 3 || r.warned != 0 {
		t.Errorf("passed=%d warned=%d, want 3/0:\n%s", r.passed, r.warned, buf.String())
	}
	if !strings.Contains(buf.String(), "[PASS]") {
		t.Errorf("no PASS badge:\n%s", buf.String())
	}
}

// TestHostApplyOnLaunchRowSaysItIsOffAndHowToLearn is §4.2's requirement: a line when the
// feature is AVAILABLE AND OFF, naming where to learn to turn it on.
//
// Available-and-off is the state that needs saying, because with the key off nothing ever
// re-checks a host render — the drift the whole design is about is silent by construction, so
// this row is the only place a user meets it.
func TestHostApplyOnLaunchRowSaysItIsOffAndHowToLearn(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "/bin")
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, "host_apply_on_launch is off") {
		t.Errorf("the section must say the feature exists and is off:\n%s", out)
	}
	if !strings.Contains(out, "config-ref") {
		t.Errorf("the row must name where to learn what the key does:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(".config", "yolo-jail")) {
		t.Errorf("the row must name the USER config — the only scope the key is read from:\n%s", out)
	}
}

// TestHostApplyOnLaunchRowSaysItIsOn is the other half, and it is not symmetry for its own
// sake: an enabled key means a launch can STOP AND ASK, which is a behaviour change to
// `claude` that someone debugging a paused terminal has to be able to find in `yolo check`.
func TestHostApplyOnLaunchRowSaysItIsOn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"host_wrappers": true, "host_apply_on_launch": true}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	o := &Options{Getenv: func(k string) string {
		if k == "PATH" {
			return "/bin"
		}
		return ""
	}}
	o.sectionHostWrappers(newReporter(&buf, false))
	out := buf.String()
	if !strings.Contains(out, "host_apply_on_launch is on") {
		t.Errorf("an enabled key must be reported as on:\n%s", out)
	}
	if strings.Contains(out, "is off") {
		t.Errorf("the off wording must not appear with the key on:\n%s", out)
	}
}

// TestHostWrappersWarnsWhenOptedInButNothingGenerated catches the "I set the key and
// never ran apply" state, which would otherwise look identical to working.
func TestHostWrappersWarnsWhenOptedInButNothingGenerated(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, nil, "/bin")
	o.sectionHostWrappers(r)
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, buf.String())
	}
	if !strings.Contains(buf.String(), "yolo host apply") {
		t.Errorf("the remedy does not name the command:\n%s", buf.String())
	}
}

func TestHostWrappersWarnsOnEmptyDir(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{}, "/bin")
	o.sectionHostWrappers(r)
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, buf.String())
	}
}

// TestHostManagementRowWarnsWhenTheApplyCannotRun is why this row rides the WRAPPERS section
// rather than one of its own (docs/design/config-ownership-and-promotion.md §4.1).
//
// `none` refuses `yolo host apply`, and that same command is what GENERATES the wrappers this
// section is about — so a home with host_wrappers on and host_management "none" has a wrapper
// directory nothing will ever regenerate. That combination is invisible everywhere else, and
// it is precisely this section's WARN criterion: configuration that is not in effect.
//
// It pins the CALL SITE. Deleting `hostManagementRow(r)` from sectionHostWrappers leaves
// hostManagementRow itself perfectly testable and this test red.
//
// ⚠ `own` WAS IN THIS LOOP and is deliberately not any more: it warned only because the apply
// refused while whole-file host composition was unbuilt, and it is built now
// (docs/design/config-ownership-and-promotion.md §10's last step). Its replacement is
// TestHostManagementRowPassesUnderOwn — the same fixture asserting the opposite outcome, so
// "own no longer warns" is something a test says rather than a row that quietly vanished.
func TestHostManagementRowWarnsWhenTheApplyCannotRun(t *testing.T) {
	const mode = "none"
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "/bin")
	cfg := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.WriteFile(cfg,
		[]byte(`{"host_wrappers": true, "host_management": "`+mode+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, `host_management is "`+mode+`"`) {
		t.Errorf("the section never names the declared ownership contract:\n%s", out)
	}
	// TWO warns: this row, and the pre-existing not-on-PATH one. The count is the
	// assertion that the row is summary-COUNTED rather than prose nobody tallies.
	if r.warned != 2 {
		t.Errorf("warned = %d, want 2 (host_management + not-on-PATH):\n%s", r.warned, out)
	}
	if !strings.Contains(out, "refuses") {
		t.Errorf("the row must say the apply refuses, which is what makes the "+
			"wrappers inert:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(".config", "yolo-jail")) {
		t.Errorf("the row must name the USER config — the only scope the key is "+
			"read from:\n%s", out)
	}
}

// TestHostManagementRowPassesUnderOwn: `own` renders, so the wrappers this section is about do
// get regenerated and there is nothing inert to warn about. The row still SPEAKS — a contract
// under which yolo composes the user's real config files whole is exactly the thing a reader
// should see stated — it just says so as a [PASS], which is this section's criterion: warn for
// configuration that is not in effect, report configuration that is.
func TestHostManagementRowPassesUnderOwn(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "/bin")
	cfg := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.WriteFile(cfg,
		[]byte(`{"host_wrappers": true, "host_management": "own"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, `host_management is "own"`) {
		t.Errorf("the section never names the declared ownership contract:\n%s", out)
	}
	// ONE warn: the pre-existing not-on-PATH row alone.
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (the not-on-PATH row only) — `own` renders, so nothing "+
			"about it makes the wrappers inert:\n%s", r.warned, out)
	}
	if strings.Contains(out, "not built yet") {
		t.Errorf("the row still says whole-file host composition is unbuilt:\n%s", out)
	}
}

// TestHostManagementRowSaysAssertWhenUnset: an unset key IS "assert" by ruling (OQ-CO2), and
// saying so is how a reader learns the silent default is a decision rather than an absence.
// [PASS], not silence — yolo is writing into their real home either way.
func TestHostManagementRowSaysAssertWhenUnset(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "/bin")
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, `host_management is "assert"`) {
		t.Errorf("an unset key must still report the contract it means:\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (only the pre-existing not-on-PATH row):\n%s",
			r.warned, out)
	}
}
