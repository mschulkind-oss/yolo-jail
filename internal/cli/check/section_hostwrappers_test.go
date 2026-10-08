package check

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hostWrappersFixture sets up a temp HOME with a user config and (optionally) generated
// wrappers, then returns Options wired to a controllable PATH.
//
// The opt-in config also DECLARES `host_management: "own"`, the one value under which an apply
// regenerates the wrappers. Since the `assert` retirement (OQ-CO14) an unset key means "none",
// whose row warns and absorbs the generation rows, so a fixture leaving it unset would test the
// ownership contract instead of the wrappers. The tests whose subject IS that contract write
// their own config over this one. `own` with `host_wrappers` written true derives nothing extra:
// host_apply_on_launch derives from host_wrappers, as it did before.
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
		body = `{"host_wrappers": true, "host_management": "own"}`
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
//
// It is ONE row for its one cause (HE-D2). It used to be two — this one, and a
// host_apply_on_launch [WARN] pointing at "the rows below" for the fix — so the maintainer's
// check read two warnings for one missing PATH entry. The merged row must still say everything
// the pair said: the cause, the fix, that a bare command runs unwrapped, that the absolute path
// still works, and that host_apply_on_launch is on and cannot fire.
func TestHostWrappersWarnsWhenNotOnPath(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude", "pi"}, "/bin:/usr/bin")
	o.sectionHostWrappers(r)
	out := buf.String()
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 — one cause, one row, and it must be summary-counted:\n%s",
			r.warned, out)
	}
	if !strings.Contains(out, "[WARN] wrapper directory is not on PATH") {
		t.Errorf("the headline must name the cause:\n%s", out)
	}
	// The FIRST note line is the fix, as the literal line, and it must PREPEND — appending
	// puts the wrapper behind the real binary and it never runs.
	dir := wrapDirIn(t)
	if got := noteLinesAfter(t, out, "[WARN] wrapper directory is not on PATH"); len(got) == 0 ||
		strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(got[0]), "->")) !=
			`export PATH="`+dir+`:$PATH"` {
		t.Errorf("the first note line must be the export line, prepending:\n%s", out)
	}
	for _, want := range []string{
		"a bare claude or pi runs unwrapped",
		"host_apply_on_launch is on but cannot fire",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the merged row must still say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "rows below") || strings.Contains(out, "row below") {
		t.Errorf("a row points at another row for its fix:\n%s", out)
	}
	// The directory is spelled ONCE, inside the line: the old pair spelled it three times.
	if n := strings.Count(out, dir); n != 1 {
		t.Errorf("the wrapper directory is spelled %d times, want 1:\n%s", n, out)
	}
	// And it must say the absolute-path escape hatch still works, which is the whole
	// reason wrappers are generated unconditionally.
	if !strings.Contains(out, "absolute path") {
		t.Errorf("output does not mention the absolute-path escape hatch:\n%s", out)
	}
	// `yolo host apply --shell-init` is removed (HE-D1): the remedy is the line, pasted by hand.
	if strings.Contains(out, "shell-init") {
		t.Errorf("the remedy still offers the removed --shell-init:\n%s", out)
	}
}

func TestHostWrappersPassesWhenOnPath(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "")
	dir := wrapDirIn(t)
	// An EMPTY temp dir ahead of the wrap dir, not /bin: precedence is observed now, and a
	// host with a real /bin/claude would make this fixture's outcome depend on the machine.
	ahead := t.TempDir()
	o.Getenv = func(k string) string {
		if k == "PATH" {
			return ahead + string(os.PathListSeparator) + dir
		}
		return ""
	}
	o.sectionHostWrappers(r)
	// THREE passes: the wrap dir being on PATH, plus the two host-key rows that share this
	// section because they share its coverage boundary — hostManagementRow (who owns the
	// files this apply writes: the fixture's "own") and hostApplyOnLaunchRow (when a re-render
	// is checked).
	if r.passed != 3 || r.warned != 0 {
		t.Errorf("passed=%d warned=%d, want 3/0:\n%s", r.passed, r.warned, buf.String())
	}
	if !strings.Contains(buf.String(), "[PASS]") {
		t.Errorf("no PASS badge:\n%s", buf.String())
	}
	// The other way in: a launcher that never reads the rc is pointed at the wrapper's full path.
	if want := "point an IDE or desktop launcher that does not read your shell rc at " +
		filepath.Join(dir, "claude"); !strings.Contains(buf.String(), want) {
		t.Errorf("the PASS does not point an IDE at the wrapper's full path (%q):\n%s", want, buf.String())
	}
}

// TestHostApplyOnLaunchRowSaysItIsOffAndHowToLearn is §4.2's requirement: a line when the
// feature is AVAILABLE AND OFF, naming where to learn to turn it on.
//
// Available-and-off is the state that needs saying, because with the key off nothing ever
// re-checks a host render — the drift the whole design is about is silent by construction, so
// this row is the only place a user meets it.
func TestHostApplyOnLaunchRowSaysItIsOffAndHowToLearn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"host_wrappers": true, "host_management": "own", "host_apply_on_launch": false}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local", "share", "yolo-jail", "bin", "wrap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	o := &Options{Getenv: func(k string) string {
		switch k {
		case "PATH":
			return "/bin"
		case "YOLO_VERSION":
			return ""
		}
		return ""
	}}
	o.sectionHostWrappers(newReporter(&buf, false))
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

// TestHostApplyOnLaunchRowDefaultsToOnWhenWrappersEnabled verifies that host_wrappers: true
// defaults host_apply_on_launch to on without needing an explicit key. host_management is "own",
// under which the gate runs at all ("none", the unset state, makes it a no-op); host_wrappers is
// written, so the derivation tested is the wrappers -> apply-on-launch link alone.
func TestHostApplyOnLaunchRowDefaultsToOnWhenWrappersEnabled(t *testing.T) {
	// The wrap dir is on PATH: "synchronizes automatically" is a claim the row may only make
	// when a wrapper can actually reach the gate.
	o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "<WRAP>")
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, "host_apply_on_launch is on") {
		t.Errorf("with host_wrappers on, host_apply_on_launch must default to on:\n%s", out)
	}
	if !strings.Contains(out, "synchronizes host configuration automatically") {
		t.Errorf("on message should describe automatic synchronization:\n%s", out)
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
	body := `{"host_wrappers": true, "host_management": "own", "host_apply_on_launch": true}`
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
	// ONE: the missing directory is the cause, and that row also says the sync cannot fire.
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
	// ONE: the empty directory is the cause, and that row also says the sync cannot fire.
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
	// TWO warns, one per cause: this row (which also says host_apply_on_launch does nothing
	// under none), and the not-on-PATH one. The count is the assertion that the row is summary-COUNTED
	// rather than prose nobody tallies.
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
	// ONE warn: the not-on-PATH row, which also says the sync cannot fire — nothing about `own`.
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (not-on-PATH) — `own` renders, so nothing "+
			"about it makes the wrappers inert:\n%s", r.warned, out)
	}
	if strings.Contains(out, "not built yet") {
		t.Errorf("the row still says whole-file host composition is unbuilt:\n%s", out)
	}
}

// TestHostManagementRowSaysNoneWhenUnset: since the `assert` retirement (OQ-CO14) an unset key
// IS "none", and the row says it read no key rather than a key written "none" — the reader who
// never wrote one must learn that the silent default is a decision, and which one. It is a
// [WARN] like a written "none": nothing regenerates the wrappers either way. It used to be a
// [PASS] saying `host_management is "assert"`, when unset meant that.
//
// Both of the none row's branches carry the unset wording: the one that absorbs the generation
// rows (no wrapper on disk) and the one beside wrappers nothing regenerates.
func TestHostManagementRowSaysNoneWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wrappers []string
		headline string
		warned   int
	}{
		// The none row absorbs the missing-directory row, and the directory being off PATH
		// is never reached: one row.
		{"no wrapper on disk", nil,
			`[WARN] host_management is unset ("none"), so ` + "`yolo host apply`" +
				` refuses and no wrapper is generated`, 1},
		// The none row and the not-on-PATH row: two causes.
		{"wrappers on disk", []string{"claude"},
			`[WARN] host_management is unset ("none") — ` + "`yolo host apply`" +
				` refuses, so these wrappers are never regenerated`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, r, buf := hostManagementFixture(t, `{"host_wrappers": true}`, tc.wrappers, "/bin")
			o.sectionHostWrappers(r)
			out := buf.String()
			if !strings.Contains(out, tc.headline) {
				t.Errorf("an unset key must report the contract it means, saying it is unset "+
					"(%q):\n%s", tc.headline, out)
			}
			if strings.Contains(out, `host_management is "none"`) {
				t.Errorf("nobody wrote \"none\", so the row must not say the key is written:\n%s", out)
			}
			if strings.Contains(out, `"assert"`) {
				t.Errorf("unset no longer means the retired \"assert\":\n%s", out)
			}
			if r.warned != tc.warned {
				t.Errorf("warned = %d, want %d:\n%s", r.warned, tc.warned, out)
			}
		})
	}
}

// TestHostManagementNoneRowsRemedyNameOwn: under "none" the row's remedy is the value that
// renders, and since the `assert` retirement (OQ-CO14) that is "own" alone — a remedy naming
// "assert" would send the user to a value every host verb now refuses. Both branches have a
// remedy of their own, so both are pinned, as the first note line (the fix leads).
func TestHostManagementNoneRowsRemedyNameOwn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wrappers []string
		headline string
		// fix is the remedy, after "Set host_management to "own" in <user config>".
		fix string
	}{
		{"no wrapper on disk", nil,
			`[WARN] host_management is "none", so ` + "`yolo host apply`" + ` refuses`,
			" and run `yolo host apply --assert`, or turn host_wrappers off."},
		{"wrappers on disk", []string{"claude"},
			`[WARN] host_management is "none" — ` + "`yolo host apply`" + ` refuses`,
			" to have yolo compose your agents' config files from your packs, or turn " +
				"host_wrappers off."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "none"}`,
				tc.wrappers, "/bin")
			o.sectionHostWrappers(r)
			out := buf.String()
			note := noteLinesAfter(t, out, tc.headline)
			want := `Set host_management to "own" in ` +
				filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc") + tc.fix
			if len(note) == 0 || strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(note[0]), "->")) != want {
				t.Errorf("the none row's first note line must be the remedy naming \"own\" (%q):\n%s",
					want, out)
			}
			if strings.Contains(out, `"assert"`) {
				t.Errorf("a remedy names the retired \"assert\":\n%s", out)
			}
		})
	}
}

// TestHostManagementRowFailsOnTheRetiredAssert: a user config still SAYING "assert" is not read
// as "none" by this row, even though the value resolves to none (config.HostManagementMode): a
// row telling a user whose file says "assert" that the key is "none" or unset would be a
// sentence about a file they did not write. It is a [FAIL] whose note is config's retirement
// message — the one message every host verb prints for the value (HostManagementRetired),
// naming the two values left and the verb that goes with each.
//
// And it absorbs the generation rows, for `none`'s reason: nothing renders under the value, so
// "run `yolo host apply --assert`" beside it is a remedy that refuses. Both generation rows are
// pinned: the missing directory, and the completeness row for a program added since.
func TestHostManagementRowFailsOnTheRetiredAssert(t *testing.T) {
	const headline = `[FAIL] host_management is "assert", which is retired`
	for _, tc := range []struct {
		name     string
		packs    string
		wrappers []string
		pathEnv  string
		// absorbed is the generation row's text the retired row must have taken over.
		absorbed string
	}{
		{"no wrapper directory", `["claude"]`, nil, "/bin", "no wrapper directory exists yet"},
		{"a program added since", `["claude", "pi"]`, []string{"claude"}, "<WRAP>", "have no wrapper: pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _ := hostManagementFixture(t,
				`{"host_wrappers": true, "host_management": "assert", "packs": `+tc.packs+`}`,
				tc.wrappers, tc.pathEnv)
			r, out := runPacksThenWrappers(t, o)
			if r.failed != 1 {
				t.Errorf("failed = %d, want 1 — the retired value is a [FAIL]:\n%s", r.failed, out)
			}
			if r.warned != 0 {
				t.Errorf("warned = %d, want 0 — the retired value is the one cause:\n%s", r.warned, out)
			}
			note := strings.Join(noteLinesAfter(t, out, headline), "\n")
			for _, want := range []string{
				`"assert" is RETIRED`,
				`"none", the default`,
				`"own" has yolo compose them whole`,
				"`yolo config promote`",
				"`yolo host apply --revert`",
				filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc"),
			} {
				if !strings.Contains(note, want) {
					t.Errorf("the retired row's note must carry the retirement message (%q):\n%s",
						want, out)
				}
			}
			if strings.Contains(out, tc.absorbed) {
				t.Errorf("a generation row offered the apply that refuses beside the retired row:\n%s", out)
			}
			for _, bad := range []string{`host_management is "none"`, `host_management is unset`} {
				if strings.Contains(out, bad) {
					t.Errorf("the file says \"assert\", so no row may say %q:\n%s", bad, out)
				}
			}
			// The value resolves to none, so the launch gate is a no-op and nothing syncs.
			if strings.Contains(out, "synchronizes host configuration automatically") {
				t.Errorf("no launch syncs under the retired value:\n%s", out)
			}
		})
	}
}

// hostManagementFixture is hostWrappersFixture with an arbitrary user config body, for the
// DERIVED cases — where `host_wrappers` is absent and `host_management` decides — and for any
// test that needs keys beside the opt-in. A body whose test is not about the ownership contract
// declares `"host_management": "own"`, for the reason hostWrappersFixture's opt-in does.
func hostManagementFixture(t *testing.T, body string, wrappers []string, pathEnv string) (*Options, *reporter, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
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
	// "<WRAP>" in pathEnv stands for the wrapper dir. Only this fixture knows the home it just
	// minted, and a caller that computed the path itself would be testing its own arithmetic — which
	// is exactly the bug the first draft of TestHostManagementOwnPassesOncePathIsRight had.
	pathEnv = strings.ReplaceAll(pathEnv, "<WRAP>",
		filepath.Join(home, ".local", "share", "yolo-jail", "bin", "wrap"))

	var buf bytes.Buffer
	o := &Options{Getenv: func(k string) string {
		switch k {
		case "PATH":
			return pathEnv
		case "YOLO_VERSION":
			return ""
		}
		return ""
	}}
	return o, newReporter(&buf, false), &buf
}

// TestHostManagementOwnIsNoLongerSilent is the reported defect, as a test.
//
// A maintainer had `host_management: "own"` set, no `host_wrappers` key, no wrappers generated —
// and `yolo doctor` was GREEN and said nothing, because this whole section returned early on the
// absent opt-in. That is a half-configured host: `own` means yolo composes the agent's config file
// whole, but a config file cannot carry a credential, so without a wrapper on PATH a bare `claude`
// gets the config and none of the environment it assumes.
//
// The opt-in is now DERIVED from `own`, so the section runs and says what is missing.
func TestHostManagementOwnIsNoLongerSilent(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_management": "own"}`, nil, "/bin")
	o.sectionHostWrappers(r)

	got := buf.String()
	if got == "" {
		t.Fatal("doctor said NOTHING about a host yolo owns the config files of and has no wrappers " +
			"for — this is the reported defect, and it is what the derivation exists to end")
	}
	if r.warned == 0 {
		t.Errorf("a host with no wrappers generated must WARN, not pass silently:\n%s", got)
	}
	// It must name the command that fixes it, or the report diagnoses without resolving.
	if !strings.Contains(got, "yolo host apply --assert") {
		t.Errorf("the warning must name the command that generates them; got:\n%s", got)
	}
}

// And the resolution path's next step: wrappers generated, but the dir is not on PATH. The whole
// point of the section is that this is observable — the wrappers exist and do nothing.
func TestHostManagementOwnWarnsWhenTheDirIsNotOnPath(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_management": "own"}`, []string{"claude"}, "/bin:/usr/bin")
	o.sectionHostWrappers(r)

	got := buf.String()
	if r.warned == 0 {
		t.Errorf("generated wrappers that nothing on PATH reaches must WARN:\n%s", got)
	}
	if !strings.Contains(got, "PATH") {
		t.Errorf("the warning must say the dir is not on PATH; got:\n%s", got)
	}
}

// The end of the resolution path: wrappers generated AND on PATH -> the section passes.
func TestHostManagementOwnPassesOncePathIsRight(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_management": "own"}`, []string{"claude"},
		"<WRAP>:/bin")
	o.sectionHostWrappers(r)

	if r.warned != 0 {
		t.Errorf("a fully configured host must not warn:\n%s", buf.String())
	}
	if r.passed == 0 {
		t.Errorf("a fully configured host must PASS, not stay silent:\n%s", buf.String())
	}
}

// ⚠ Only a WRITTEN "own" derives the section; every other host_management state stays silent with
// host_wrappers unset. The unset key is the one that matters: deriving wrappers from it would turn
// a PATH claim on — and this section on — for every user who has declared nothing. "none" says
// yolo writes nothing, and the retired "assert" resolves to none (validation reports the value
// itself, through its own channel). This test said "assert" stays silent while that was the
// unset default; the retirement (OQ-CO14) made the unset key none, and the rule is unchanged.
func TestHostManagementOnlyOwnDerivesTheSection(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"host_management": "none"}`,
		`{"host_management": "assert"}`,
	} {
		t.Run(body, func(t *testing.T) {
			o, r, buf := hostManagementFixture(t, body, nil, "/bin")
			o.sectionHostWrappers(r)
			if buf.Len() != 0 || r.warned != 0 || r.passed != 0 || r.failed != 0 {
				t.Errorf("only a written \"own\" derives wrappers — anything else would nag "+
					"users who never asked for them:\n%s", buf.String())
			}
		})
	}
}

// setPath points o's PATH at pathEnv, keeping every other variable empty (a host, not a jail).
func setPath(o *Options, pathEnv string) {
	o.Getenv = func(k string) string {
		if k == "PATH" {
			return pathEnv
		}
		return ""
	}
}

// fakeProgram writes an executable named bin into a fresh temp dir and returns the dir. It is
// a stand-in for a real agent binary ahead of the wrappers on PATH; nothing ever runs it.
func fakeProgram(t *testing.T, bin string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runPacksThenWrappers drives the two sections the way Check() does — sectionPacks first, on
// the SAME Options — and returns only the wrappers section's report. Going through the real
// sectionPacks is the point: the completeness row depends on the pack set sectionPacks hands
// forward, so a test that injected the set would pass with the hand-off deleted.
func runPacksThenWrappers(t *testing.T, o *Options) (*reporter, string) {
	t.Helper()
	var packsOut bytes.Buffer
	pr := newReporter(&packsOut, false)
	o.sectionPacks(pr, jsonx.NewOrderedMap())
	if pr.failed > 0 {
		t.Fatalf("the Packs section failed, so Check() would never reach the wrappers:\n%s",
			packsOut.String())
	}
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o.sectionHostWrappers(r)
	return r, buf.String()
}

// TestHostWrappersWarnsWhenASelectedProgramHasNoWrapper is the incomplete-set defect: a pack
// added since the last apply installs a program with no wrapper, and the section used to
// report "wrapper directory is on PATH (claude)" as a PASS while a dry run said a wrapper
// would be added. A program with no wrapper never passes the launch gate, so it never
// self-syncs — the one launch that most needs the gate is the one that skips it.
func TestHostWrappersWarnsWhenASelectedProgramHasNoWrapper(t *testing.T) {
	o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own", "packs": ["claude", "pi"]}`,
		[]string{"claude"}, "<WRAP>")
	r, out := runPacksThenWrappers(t, o)

	if !strings.Contains(out, "1 program(s) have no wrapper: pi") {
		t.Errorf("the missing program must be named, counted:\n%s", out)
	}
	if !strings.Contains(out, "yolo host apply --assert") || !strings.Contains(out, "yolo host -- pi") {
		t.Errorf("the row must name both remedies (apply, or one gated launch):\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (completeness only — claude's wrapper wins):\n%s", r.warned, out)
	}
}

// TestHostWrappersCompleteSetPasses: every selected program has a wrapper and each wins, so
// nothing warns and the section says so.
func TestHostWrappersCompleteSetPasses(t *testing.T) {
	o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own", "packs": ["claude"]}`,
		[]string{"claude"}, "<WRAP>")
	r, out := runPacksThenWrappers(t, o)
	if r.warned != 0 {
		t.Errorf("a complete, winning wrapper set must not warn:\n%s", out)
	}
	if !strings.Contains(out, "wins for every wrapper (claude)") {
		t.Errorf("the PASS must say the wrappers WIN, not merely that the dir is on PATH:\n%s", out)
	}
}

// TestHostWrappersNamesTheSelectedProgramsWhenNoneAreGenerated: with the pack set known, the
// "no wrappers yet" row stops guessing between two causes and names the programs.
func TestHostWrappersNamesTheSelectedProgramsWhenNoneAreGenerated(t *testing.T) {
	o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own", "packs": ["claude"]}`,
		nil, "/bin")
	_, out := runPacksThenWrappers(t, o)
	if !strings.Contains(out, "(claude)") {
		t.Errorf("the no-directory row must name the program it would wrap:\n%s", out)
	}
}

// TestHostWrappersWarnsWhenAWrapperIsShadowed is the precedence defect: the wrap dir is on
// PATH, but APPENDED, and a real `claude` ahead of it wins — the section used to PASS because
// OnPath asks only "is the dir on PATH", never "does it win". The ruled rule is "Prepend, not
// append" (docs/reference/host-agent-environment.md).
func TestHostWrappersWarnsWhenAWrapperIsShadowed(t *testing.T) {
	o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "")
	dir := wrapDirIn(t)
	ahead := fakeProgram(t, "claude")
	setPath(o, ahead+string(os.PathListSeparator)+dir)
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o.sectionHostWrappers(r)
	out := buf.String()

	if !strings.Contains(out, "1 wrapper(s) are shadowed by an earlier PATH entry: claude") {
		t.Errorf("a shadowed wrapper must WARN, naming it:\n%s", out)
	}
	if !strings.Contains(out, "claude runs "+filepath.Join(ahead, "claude")) {
		t.Errorf("the row must name the binary that WINS:\n%s", out)
	}
	if !strings.Contains(out, `export PATH="`+dir+`:$PATH"`) {
		t.Errorf("the row must give the exact prepend fix:\n%s", out)
	}
	if strings.Contains(out, "[PASS] wrapper directory is on PATH") {
		t.Errorf("a shadowed wrapper set must not also PASS the PATH row:\n%s", out)
	}
	// ONE: the shadow row — which, the only wrapper losing, also says the sync cannot fire.
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (the shadow row):\n%s", r.warned, out)
	}
	if got := noteLinesAfter(t, out, "[WARN] 1 wrapper(s) are shadowed"); len(got) == 0 ||
		!strings.Contains(got[0], `export PATH="`+dir+`:$PATH"`) {
		t.Errorf("the shadow row's first note line must be the fix:\n%s", out)
	}
	if !strings.Contains(out, "host_apply_on_launch is on but cannot fire") {
		t.Errorf("with every wrapper shadowed the shadow row must say the sync cannot fire:\n%s", out)
	}
}

// TestHostWrappersPartialShadowStillReachesTheGate: one wrapper shadowed, one winning — the
// shadow warns, and the gate row PASSES naming the wrapper that does reach it, because a
// wrapped launch of that program runs the apply that regenerates the set.
func TestHostWrappersPartialShadowStillReachesTheGate(t *testing.T) {
	o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude", "pi"}, "")
	dir := wrapDirIn(t)
	ahead := fakeProgram(t, "claude")
	setPath(o, ahead+string(os.PathListSeparator)+dir)
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, "shadowed by an earlier PATH entry: claude") {
		t.Errorf("claude's shadow must be reported:\n%s", out)
	}
	if !strings.Contains(out, "synchronizes host configuration automatically (reached through pi)") {
		t.Errorf("the gate row must PASS through the wrapper that wins:\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (the shadow row):\n%s", r.warned, out)
	}
	// pi still reaches the gate, so the shadow is NOT why the sync cannot fire — it can.
	if strings.Contains(out, "cannot fire") {
		t.Errorf("a partial shadow must not claim the sync cannot fire:\n%s", out)
	}
}

// TestHostWrappersSymlinkedPathSpellingWins is the darwin PATH-RESOLUTION class through the
// section: PATH names the wrap dir through a symlinked parent (every macOS temp path, /var
// being a symlink to /private/var). Comparing spellings called that "NOT on PATH", or a
// wrapper shadowed by itself; comparing files is what the shell does.
func TestHostWrappersSymlinkedPathSpellingWins(t *testing.T) {
	o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "")
	home := os.Getenv("HOME")
	alias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	setPath(o, filepath.Join(alias, ".local", "share", "yolo-jail", "bin", "wrap"))
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o.sectionHostWrappers(r)
	if r.warned != 0 {
		t.Errorf("the wrap dir spelled through a symlink is still the wrap dir:\n%s", buf.String())
	}
}

// TestHostApplyOnLaunchRowWarnsWhenNoWrapperCanReachTheGate is the reassurance defect: the row
// PASSed "synchronizes host configuration automatically" whatever the wrappers' state, but the
// re-check runs only inside `yolo host --`, which only a wrapper execs. Each state in which no
// wrapper wins must WARN, and never print the reassurance.
//
// ONE WARN, ON THE CAUSE'S ROW (HE-D2). The first fix gave the gate a [WARN] of its own that
// pointed at "the rows below", so every one of these states counted its one cause twice. Now
// exactly one row is a [WARN], its headline is the cause, and it is that row which says the key
// is on and cannot fire — so deleting any cause row's gateClause call fails its case here. All
// six call sites have a case: the four states above, the wrapper path being unreadable, and the
// selected packs installing nothing (both added after a review found their calls deletable with
// this test green).
func TestHostApplyOnLaunchRowWarnsWhenNoWrapperCanReachTheGate(t *testing.T) {
	cases := []struct {
		name       string
		wrappers   []string
		shadow     bool
		onPath     bool
		unreadable bool   // the wrap path is a regular file, so it cannot be listed
		noPrograms bool   // run the Packs section over a config selecting no pack
		goneYolo   bool   // each wrapper execs a yolo that no longer exists
		headline   string // the cause row's headline, which must carry the gate sentence
	}{
		{"no wrapper directory", nil, false, false, false, false, false,
			"[WARN] host_wrappers is on but no wrapper directory exists yet"},
		{"empty wrapper directory", []string{}, false, true, false, false, false,
			"[WARN] host_wrappers is on but no wrappers are generated"},
		{"directory off PATH", []string{"claude"}, false, false, false, false, false,
			"[WARN] wrapper directory is not on PATH"},
		{"every wrapper shadowed", []string{"claude"}, true, true, false, false, false,
			"[WARN] 1 wrapper(s) are shadowed by an earlier PATH entry: claude"},
		{"wrapper path unreadable", nil, false, true, true, false, false,
			"[WARN] cannot read the wrapper directory"},
		{"selected packs install nothing", nil, false, false, false, true, false,
			"[WARN] host_wrappers is on but no selected pack installs a program"},
		{"every winning wrapper's yolo is gone", []string{"claude"}, false, true, false, false, true,
			"[WARN] 1 wrapper(s) cannot start yolo from every launcher: claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, tc.wrappers, "")
			if tc.unreadable {
				if err := os.MkdirAll(filepath.Dir(wrapDirIn(t)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(wrapDirIn(t), []byte("not a directory\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.goneYolo {
				writeWrapperNaming(t, filepath.Join(t.TempDir(), "gone", "yolo"), tc.wrappers...)
			}
			pathEnv := t.TempDir()
			if tc.shadow {
				pathEnv = fakeProgram(t, "claude")
			}
			if tc.onPath {
				pathEnv += string(os.PathListSeparator) + wrapDirIn(t)
			}
			setPath(o, pathEnv)
			var out string
			var r *reporter
			if tc.noPrograms {
				r, out = runPacksThenWrappers(t, o)
			} else {
				var buf bytes.Buffer
				r = newReporter(&buf, false)
				o.sectionHostWrappers(r)
				out = buf.String()
			}
			if r.warned != 1 {
				t.Errorf("warned = %d, want 1 — one cause, one row:\n%s", r.warned, out)
			}
			note := strings.Join(noteLinesAfter(t, out, tc.headline), "\n")
			if !strings.Contains(note, "host_apply_on_launch is on but cannot fire") {
				t.Errorf("the cause row %q must say the sync cannot fire:\n%s", tc.headline, out)
			}
			if strings.Count(out, "host_apply_on_launch") != 1 {
				t.Errorf("host_apply_on_launch must be named once, on the cause row:\n%s", out)
			}
			if strings.Contains(out, "synchronizes host configuration automatically") {
				t.Errorf("the reassurance must not print when no wrapper reaches the gate:\n%s", out)
			}
			if strings.Contains(out, "rows below") {
				t.Errorf("a row points at another row for its fix:\n%s", out)
			}
		})
	}
}

// writeWrapperNaming overwrites each named wrapper in the fixture's wrap dir with the body a host
// apply writes for it, execing yolo.
func writeWrapperNaming(t *testing.T, yolo string, bins ...string) {
	t.Helper()
	for _, bin := range bins {
		if err := os.WriteFile(filepath.Join(wrapDirIn(t), bin), []byte(hostwrap.BodyFor(yolo, bin)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHostWrappersWarnsWhenAWrapperNamesAYoloThatIsGone: a wrapper execs yolo by the path the
// apply that wrote it ran from, so an upgrade or a move that deletes that file makes it exit 127
// from every launcher. The row names the file and the apply that rewrites it, whatever PATH says,
// and — the only wrapper being the one that wins — is why the launch sync cannot fire.
func TestHostWrappersWarnsWhenAWrapperNamesAYoloThatIsGone(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "<WRAP>")
	gone := filepath.Join(t.TempDir(), "Cellar", "yolo", "0.9", "bin", "yolo")
	writeWrapperNaming(t, gone, "claude")
	o.sectionHostWrappers(r)
	out := buf.String()
	headline := "[WARN] 1 wrapper(s) cannot start yolo from every launcher: claude"
	if !strings.Contains(out, headline) {
		t.Fatalf("a wrapper naming a yolo that is gone must WARN:\n%s", out)
	}
	note := strings.Join(noteLinesAfter(t, out, headline), "\n")
	for _, want := range []string{
		"yolo host apply --assert",
		"claude execs " + gone + ", which no longer exists",
		"host_apply_on_launch is on but cannot fire",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the row must say %q:\n%s", want, out)
		}
	}
	if got := noteLinesAfter(t, out, headline); !strings.Contains(got[0], "yolo host apply --assert") {
		t.Errorf("the row's first note line must be the fix:\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 — one cause, one row:\n%s", r.warned, out)
	}
	if strings.Contains(out, "synchronizes host configuration automatically") {
		t.Errorf("no wrapped launch reaches the gate, so the reassurance must not print:\n%s", out)
	}
}

// TestHostWrappersWarnsWhenAWrapperYoloCannotRun: present but not executable is the same cause.
func TestHostWrappersWarnsWhenAWrapperYoloCannotRun(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "<WRAP>")
	plain := filepath.Join(t.TempDir(), "yolo")
	if err := os.WriteFile(plain, []byte("not a program\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWrapperNaming(t, plain, "claude")
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, "claude execs a yolo that cannot run: "+plain) {
		t.Errorf("a wrapper naming a yolo that cannot run must say so:\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, out)
	}
}

// TestHostWrappersPassesAWrapperNamingAYoloThatRuns: the yolo the wrapper names runs, so there is
// nothing to say about it.
func TestHostWrappersPassesAWrapperNamingAYoloThatRuns(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "<WRAP>")
	writeWrapperNaming(t, filepath.Join(fakeProgram(t, "yolo"), "yolo"), "claude")
	o.sectionHostWrappers(r)
	if r.warned != 0 {
		t.Errorf("a wrapper naming a yolo that runs must not warn:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "synchronizes host configuration automatically (reached through claude)") {
		t.Errorf("the gate row must pass through the wrapper:\n%s", buf.String())
	}
}

// TestHostWrappersGateRowNamesOnlyTheWrappersThatReachYolo: claude's yolo is gone and pi's
// runs, so a wrapped launch of pi still reaches the gate and the gate row passes through pi
// alone, while claude's row says what to fix.
func TestHostWrappersGateRowNamesOnlyTheWrappersThatReachYolo(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude", "pi"}, "<WRAP>")
	writeWrapperNaming(t, filepath.Join(t.TempDir(), "gone", "yolo"), "claude")
	writeWrapperNaming(t, filepath.Join(fakeProgram(t, "yolo"), "yolo"), "pi")
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, "synchronizes host configuration automatically (reached through pi)") {
		t.Errorf("the gate row must pass through pi alone:\n%s", out)
	}
	if !strings.Contains(out, "1 wrapper(s) cannot start yolo from every launcher: claude") {
		t.Errorf("claude's row is missing:\n%s", out)
	}
	if strings.Contains(out, "cannot fire") {
		t.Errorf("pi reaches the gate, so the sync can fire:\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, out)
	}
}

// TestHostWrappersWarnsAboutAWrapperAnOlderYoloWrote: before wrappers named yolo by path, each
// found it through the PATH of whatever started it — the shape an IDE or desktop launcher whose
// PATH lacks yolo cannot start. With yolo on this PATH a bare launch from here still reaches the
// gate, so the row is about launchers only; with none here, it is also why the sync cannot fire.
func TestHostWrappersWarnsAboutAWrapperAnOlderYoloWrote(t *testing.T) {
	headline := "[WARN] 1 wrapper(s) cannot start yolo from every launcher: claude"
	t.Run("yolo on this PATH", func(t *testing.T) {
		o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "")
		writeWrapperNaming(t, "yolo", "claude")
		yoloDir := fakeProgram(t, "yolo")
		setPath(o, wrapDirIn(t)+string(os.PathListSeparator)+yoloDir)
		o.sectionHostWrappers(r)
		out := buf.String()
		note := strings.Join(noteLinesAfter(t, out, headline), "\n")
		if !strings.Contains(note, "claude finds yolo through PATH ("+filepath.Join(yoloDir, "yolo")+
			" here), so a launcher whose PATH lacks it cannot start it") {
			t.Errorf("the row must say a launcher without yolo cannot start it:\n%s", out)
		}
		if strings.Contains(note, "cannot fire") {
			t.Errorf("a bare launch from here still reaches the gate:\n%s", out)
		}
		if !strings.Contains(out, "reached through claude") {
			t.Errorf("the gate row must still pass through claude:\n%s", out)
		}
		if r.warned != 1 {
			t.Errorf("warned = %d, want 1:\n%s", r.warned, out)
		}
	})
	t.Run("no yolo on this PATH", func(t *testing.T) {
		o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude"}, "<WRAP>")
		writeWrapperNaming(t, "yolo", "claude")
		o.sectionHostWrappers(r)
		out := buf.String()
		note := strings.Join(noteLinesAfter(t, out, headline), "\n")
		if !strings.Contains(note, "claude finds yolo through PATH, and this PATH has none") {
			t.Errorf("the row must say this PATH has no yolo:\n%s", out)
		}
		if !strings.Contains(note, "host_apply_on_launch is on but cannot fire") {
			t.Errorf("no launch from here reaches the gate, and this row is why:\n%s", out)
		}
		if r.warned != 1 {
			t.Errorf("warned = %d, want 1:\n%s", r.warned, out)
		}
	})
}

// TestHostWrappersStaleYoloUnderNoneNamesTheKeyItWaitsOn: under host_management "none" the apply
// refuses, so the stale row's fix says what the apply needs first instead of offering one that
// refuses — and the value it needs is "own", the one that renders since the `assert` retirement
// (OQ-CO14). The fix named "assert" until then. "none" is also the unset state, so an unset key
// takes the same fix.
func TestHostWrappersStaleYoloUnderNoneNamesTheKeyItWaitsOn(t *testing.T) {
	for _, body := range []string{
		`{"host_wrappers": true, "host_management": "none"}`,
		`{"host_wrappers": true}`,
	} {
		t.Run(body, func(t *testing.T) {
			o, _, buf := hostManagementFixture(t, body, []string{"claude"}, "<WRAP>")
			writeWrapperNaming(t, filepath.Join(t.TempDir(), "gone", "yolo"), "claude")
			o.sectionHostWrappers(newReporter(buf, false))
			note := noteLinesAfter(t, buf.String(),
				"[WARN] 1 wrapper(s) cannot start yolo from every launcher: claude")
			want := "once host_management in " +
				filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc") +
				` is "own" (under "none", the default, it refuses); or turn host_wrappers off.`
			if len(note) == 0 || !strings.Contains(note[0], want) {
				t.Errorf("under none the fix must lead, naming \"own\" as the value the apply "+
					"waits on (%q):\n%s", want, buf.String())
			}
			if strings.Contains(buf.String(), `"assert"`) {
				t.Errorf("a fix names the retired \"assert\":\n%s", buf.String())
			}
		})
	}
}

// TestHostWrappersOKRowNamesTheDirectorysProgramSlot: with several wrappers, the IDE pointer
// names the directory's <program> slot and the programs in it.
func TestHostWrappersOKRowNamesTheDirectorysProgramSlot(t *testing.T) {
	o, r, buf := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own"}`, []string{"claude", "pi"}, "<WRAP>")
	o.sectionHostWrappers(r)
	want := "point an IDE or desktop launcher that does not read your shell rc at " +
		filepath.Join(wrapDirIn(t), "<program>") + " (claude or pi)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("want %q in:\n%s", want, buf.String())
	}
}

// floorOn is an Options.HostFloor seam answering for an empty prefix on goos: on darwin the floor
// holds no installer agent (claude has no floor entry), on linux it does.
func floorOn(t *testing.T, goos string) func([]hostfloor.Program) *hostfloor.Floor {
	t.Helper()
	dir := t.TempDir()
	return func(progs []hostfloor.Program) *hostfloor.Floor {
		return &hostfloor.Floor{Dir: dir, GOOS: goos, GOARCH: "arm64",
			NodeFloor: hostfloor.HighestNodeFloor(progs)}
	}
}

// floorLeavingOut is floorOn with the user-scope host_floor leaving the named packs out, which makes
// their programs not the floor's to hold (hostfloor.Floor.OutsideTheFloor): `yolo host -- <bin>` looks
// such a program up on the launch's PATH (host-notch-readiness.md HNR-D4), so it is the only kind a
// launcher can need host_path for. A no-entry program the floor does answer for is refused instead
// (HNR-D2), which no folder changes.
func floorLeavingOut(t *testing.T, goos string, packs ...string) func([]hostfloor.Program) *hostfloor.Floor {
	t.Helper()
	base := floorOn(t, goos)
	return func(progs []hostfloor.Program) *hostfloor.Floor {
		f := base(progs)
		f.Include = func(pack string) bool { return !slices.Contains(packs, pack) }
		return f
	}
}

// TestHostWrappersOKRowSaysALauncherNeedsHostPathForAProgramYoloKeepsNoCopyOf: on macOS the floor
// holds no installer agent, so `yolo host -- claude` finds claude on the PATH it was started with,
// then host_path. An IDE pointed at the wrapper hands it the IDE's PATH, which the rc that put
// claude's folder on this shell's PATH never built, so the OK row says that launcher also needs
// host_path naming the folder. Not on linux, where the floor holds claude; not when host_path
// already names the folder.
func TestHostWrappersOKRowSaysALauncherNeedsHostPathForAProgramYoloKeepsNoCopyOf(t *testing.T) {
	const lead = "yolo keeps no copy of claude on this machine, so that launcher also needs host_path"
	run := func(t *testing.T, floor func(*testing.T, string) func([]hostfloor.Program) *hostfloor.Floor, goos, userConfig string) (string, string) {
		t.Helper()
		t.Setenv("YOLO_VERSION", "")
		claudeDir := fakeProgram(t, "claude")
		if userConfig != "" {
			userConfig = strings.ReplaceAll(userConfig, "<CLAUDE>", claudeDir)
		} else {
			userConfig = `{"host_wrappers": true, "host_management": "own", "packs": ["claude"]}`
		}
		o, _, _ := hostManagementFixture(t, userConfig, []string{"claude"}, "")
		setPath(o, wrapDirIn(t)+string(os.PathListSeparator)+claudeDir)
		o.HostFloor = floor(t, goos)
		r, out := runPacksThenWrappers(t, o)
		if r.warned != 0 {
			t.Errorf("warned = %d, want 0:\n%s", r.warned, out)
		}
		return out, claudeDir
	}
	leftOut := func(t *testing.T, goos string) func([]hostfloor.Program) *hostfloor.Floor {
		return floorLeavingOut(t, goos, "claude")
	}
	t.Run("darwin, host_floor leaves claude out", func(t *testing.T) {
		out, claudeDir := run(t, leftOut, "darwin", "")
		want := lead + " in " + filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc") +
			" to name " + claudeDir + ", the folder this PATH finds it in"
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})
	t.Run("linux", func(t *testing.T) {
		if out, _ := run(t, floorOn, "linux", ""); strings.Contains(out, lead) {
			t.Errorf("the floor holds claude here, so no launcher needs host_path for it:\n%s", out)
		}
	})
	// With no floor entry the floor still answers for, `yolo host -- claude` refuses (HNR-D2), so
	// no folder on host_path would start it and the row must not ask for one.
	t.Run("darwin, the floor answers for claude", func(t *testing.T) {
		if out, _ := run(t, floorOn, "darwin", ""); strings.Contains(out, lead) {
			t.Errorf("yolo host refuses claude here, so no launcher needs host_path for it:\n%s", out)
		}
	})
	t.Run("darwin, host_path names the folder", func(t *testing.T) {
		out, _ := run(t, leftOut, "darwin", `{"host_wrappers": true, "host_management": "own", "packs": ["claude"], "host_path": ["<CLAUDE>"]}`)
		if strings.Contains(out, lead) {
			t.Errorf("host_path already names claude's folder:\n%s", out)
		}
	})
}

// TestHostWrappersOffPathAndShadowedRowsSayALauncherNeedsHostPath: a wrapper is reached by its
// absolute path whatever PATH says, so the user an IDE or desktop launcher is pointed at it for is
// as likely as not one whose wrapper directory is off PATH, or behind claude's own folder. Those
// rows end the section, so they must carry the full-path pointer and, for a program yolo keeps no
// copy of (claude on macOS), the host_path that launcher also needs, as the PASS row does. The
// off-PATH row used to say only that "each wrapper works by absolute path", which is false for
// claude from such a launcher until host_path names its folder. Neither row respells the wrapper
// directory (HE-D2's merged row spells it once, in the PATH line).
func TestHostWrappersOffPathAndShadowedRowsSayALauncherNeedsHostPath(t *testing.T) {
	const lead = "yolo keeps no copy of claude on this machine, so that launcher also needs host_path"
	const pointer = "A wrapper still starts by its absolute path, which is what to give an IDE or " +
		"desktop launcher that does not read your shell rc."
	run := func(t *testing.T, floor func(*testing.T, string) func([]hostfloor.Program) *hostfloor.Floor, goos string, pathFor func(wrap, claude string) string) (string, string) {
		t.Helper()
		t.Setenv("YOLO_VERSION", "")
		claudeDir := fakeProgram(t, "claude")
		o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own", "packs": ["claude"]}`,
			[]string{"claude"}, "")
		setPath(o, pathFor(wrapDirIn(t), claudeDir))
		o.HostFloor = floor(t, goos)
		r, out := runPacksThenWrappers(t, o)
		if r.warned != 1 {
			t.Errorf("warned = %d, want 1 — one cause, one row:\n%s", r.warned, out)
		}
		return out, claudeDir
	}
	sep := string(os.PathListSeparator)
	for _, tc := range []struct {
		name, headline string
		pathFor        func(wrap, claude string) string
	}{
		{"off PATH", "[WARN] wrapper directory is not on PATH",
			func(_, claude string) string { return claude }},
		{"shadowed", "[WARN] 1 wrapper(s) are shadowed by an earlier PATH entry: claude",
			func(wrap, claude string) string { return claude + sep + wrap }},
	} {
		t.Run(tc.name+", darwin", func(t *testing.T) {
			out, claudeDir := run(t, func(t *testing.T, goos string) func([]hostfloor.Program) *hostfloor.Floor {
				return floorLeavingOut(t, goos, "claude")
			}, "darwin", tc.pathFor)
			note := strings.Join(noteLinesAfter(t, out, tc.headline), "\n")
			want := lead + " in " + filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc") +
				" to name " + claudeDir + ", the folder this PATH finds it in"
			for _, w := range []string{pointer, want} {
				if !strings.Contains(note, w) {
					t.Errorf("the row must say %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "each wrapper works by absolute path") {
				t.Errorf("the row still claims every wrapper works from any launcher:\n%s", out)
			}
			if n := strings.Count(out, wrapDirIn(t)); n != 1 {
				t.Errorf("the wrapper directory is spelled %d times, want 1:\n%s", n, out)
			}
		})
		t.Run(tc.name+", linux", func(t *testing.T) {
			out, _ := run(t, floorOn, "linux", tc.pathFor)
			if !strings.Contains(strings.Join(noteLinesAfter(t, out, tc.headline), "\n"), pointer) {
				t.Errorf("the row must still point a launcher at the wrapper's absolute path:\n%s", out)
			}
			if strings.Contains(out, lead) {
				t.Errorf("the floor holds claude here, so no launcher needs host_path for it:\n%s", out)
			}
		})
	}
}

// TestHostWrappersPointsNoLauncherAtAWrapperThatCannotStartYolo: the stale-yolo row says a wrapper
// cannot start yolo from every launcher, so no later row may tell an IDE or desktop launcher to use
// that wrapper — one section giving both answers is the defect. Every row that ends the section with
// wrappers on disk (PASS, off PATH, shadowed) leaves out the pointer, and the host_path line that
// rides on it, when the only wrapper is stale: the stale row's `yolo host apply --assert` comes
// first, and the next check points the launcher once it has run. Both kinds of stale wrapper are
// covered: one naming a yolo that is gone, and one an older yolo wrote that finds yolo through PATH.
func TestHostWrappersPointsNoLauncherAtAWrapperThatCannotStartYolo(t *testing.T) {
	const passPointer = "point an IDE or desktop launcher"
	const notePointer = "still starts by its absolute path"
	const hostPathLead = "yolo keeps no copy of claude on this machine"
	staleHeadline := "[WARN] 1 wrapper(s) cannot start yolo from every launcher: claude"
	sep := string(os.PathListSeparator)
	for _, tc := range []struct {
		name string
		// bare is a wrapper an older yolo wrote, naming `yolo` bare; otherwise it names a yolo
		// that is gone.
		bare    bool
		pathFor func(wrap, claude, yolo string) string
		// row is the row that ends the section.
		row string
	}{
		{name: "gone yolo, on PATH", row: "[PASS] wrapper directory is on PATH",
			pathFor: func(wrap, claude, _ string) string { return wrap + sep + claude }},
		{name: "gone yolo, off PATH", row: "[WARN] wrapper directory is not on PATH",
			pathFor: func(_, claude, _ string) string { return claude }},
		{name: "gone yolo, shadowed", row: "[WARN] 1 wrapper(s) are shadowed by an earlier PATH entry: claude",
			pathFor: func(wrap, claude, _ string) string { return claude + sep + wrap }},
		{name: "bare yolo, on PATH", bare: true, row: "[PASS] wrapper directory is on PATH",
			pathFor: func(wrap, claude, yolo string) string { return wrap + sep + claude + sep + yolo }},
		{name: "bare yolo, off PATH", bare: true, row: "[WARN] wrapper directory is not on PATH",
			pathFor: func(_, claude, yolo string) string { return claude + sep + yolo }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_VERSION", "")
			o, _, _ := hostManagementFixture(t, `{"host_wrappers": true, "host_management": "own", "packs": ["claude"]}`,
				[]string{"claude"}, "")
			if tc.bare {
				writeWrapperNaming(t, "yolo", "claude")
			} else {
				writeWrapperNaming(t, filepath.Join(t.TempDir(), "gone", "yolo"), "claude")
			}
			claudeDir, yoloDir := fakeProgram(t, "claude"), fakeProgram(t, "yolo")
			setPath(o, tc.pathFor(wrapDirIn(t), claudeDir, yoloDir))
			// With host_floor leaving claude out, `yolo host` looks claude up on PATH, so a launcher
			// pointed at a wrapper that could start yolo would also be told it needs host_path.
			o.HostFloor = floorLeavingOut(t, "darwin", "claude")
			_, out := runPacksThenWrappers(t, o)
			if !strings.Contains(out, staleHeadline) {
				t.Fatalf("the stale-yolo row is missing:\n%s", out)
			}
			if !strings.Contains(out, tc.row) {
				t.Fatalf("the row %q that ends the section is missing:\n%s", tc.row, out)
			}
			for _, bad := range []string{passPointer, notePointer, hostPathLead} {
				if strings.Contains(out, bad) {
					t.Errorf("a launcher is pointed at the wrapper the row above says cannot start "+
						"yolo (%q):\n%s", bad, out)
				}
			}
		})
	}
}

// TestHostWrappersPointsALauncherOnlyAtTheWrappersThatStartYolo: claude's yolo is gone and pi's
// runs, so an IDE or desktop launcher can be pointed at pi's wrapper and not at claude's — on the
// PASS row by name, and on the off-PATH row by naming the wrapper that is the exception.
func TestHostWrappersPointsALauncherOnlyAtTheWrappersThatStartYolo(t *testing.T) {
	setUp := func(t *testing.T, body, pathEnv string) (*Options, *reporter, *bytes.Buffer) {
		t.Helper()
		o, r, buf := hostManagementFixture(t, body, []string{"claude", "pi"}, pathEnv)
		writeWrapperNaming(t, filepath.Join(t.TempDir(), "gone", "yolo"), "claude")
		writeWrapperNaming(t, filepath.Join(fakeProgram(t, "yolo"), "yolo"), "pi")
		return o, r, buf
	}
	t.Run("on PATH", func(t *testing.T) {
		o, r, buf := setUp(t, `{"host_wrappers": true, "host_management": "own"}`, "<WRAP>")
		o.sectionHostWrappers(r)
		out := buf.String()
		want := "point an IDE or desktop launcher that does not read your shell rc at " +
			filepath.Join(wrapDirIn(t), "pi")
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
		if strings.Contains(out, "<program>") || strings.Contains(out, filepath.Join(wrapDirIn(t), "claude")) {
			t.Errorf("claude's wrapper cannot start yolo, so the pointer must not name it:\n%s", out)
		}
	})
	t.Run("off PATH", func(t *testing.T) {
		o, r, buf := setUp(t, `{"host_wrappers": true, "host_management": "own"}`, t.TempDir())
		o.sectionHostWrappers(r)
		out := buf.String()
		note := strings.Join(noteLinesAfter(t, out, "[WARN] wrapper directory is not on PATH"), "\n")
		want := "Every wrapper but claude still starts by its absolute path, which is what to give an " +
			"IDE or desktop launcher that does not read your shell rc."
		if !strings.Contains(note, want) {
			t.Errorf("want %q in the off-PATH row:\n%s", want, out)
		}
	})
	// The host_path line is about "that launcher", the one the pointer sends to a wrapper, so it
	// follows the pointer: with pi's wrapper pointable beside a stale claude, darwin's floor keeping
	// no copy of claude must still not produce claude's line.
	t.Run("on PATH, darwin", func(t *testing.T) {
		t.Setenv("YOLO_VERSION", "")
		o, _, _ := setUp(t, `{"host_wrappers": true, "host_management": "own", "packs": ["claude", "pi"]}`, "")
		claudeDir := fakeProgram(t, "claude")
		setPath(o, wrapDirIn(t)+string(os.PathListSeparator)+claudeDir)
		o.HostFloor = floorLeavingOut(t, "darwin", "claude")
		_, out := runPacksThenWrappers(t, o)
		if !strings.Contains(out, "point an IDE or desktop launcher that does not read your shell rc at "+
			filepath.Join(wrapDirIn(t), "pi")) {
			t.Fatalf("the pointer must name pi's wrapper:\n%s", out)
		}
		if strings.Contains(out, "yolo keeps no copy of claude on this machine") {
			t.Errorf("claude's wrapper is not pointable, so no host_path line may be about it:\n%s", out)
		}
	})
}

// TestHostApplyOnLaunchOffKeepsTheCauseRowToItself: with the key OFF, the sync is opted out of
// rather than broken, so the cause row must not claim it cannot fire — the off row already says
// what is true.
func TestHostApplyOnLaunchOffKeepsTheCauseRowToItself(t *testing.T) {
	o, r, buf := hostManagementFixture(t,
		`{"host_wrappers": true, "host_management": "own", "host_apply_on_launch": false}`, []string{"claude"}, "/bin")
	o.sectionHostWrappers(r)
	out := buf.String()
	if !strings.Contains(out, "host_apply_on_launch is off") {
		t.Errorf("the off row must still print:\n%s", out)
	}
	if strings.Contains(out, "cannot fire") {
		t.Errorf("a key that is off cannot be reported as failing to fire:\n%s", out)
	}
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (not-on-PATH):\n%s", r.warned, out)
	}
}

// TestHostManagementNoneAbsorbsTheGenerationRows: under `none` the apply that generates a
// wrapper refuses, and so does the launch gate's, so a missing wrapper has `none` as its cause.
// A separate row saying "run `yolo host apply --assert`" was a remedy that refuses, printed
// beside the row saying so. The `none` row names the programs left unwrapped instead, and —
// when no wrapper exists at all — that the sync cannot fire.
func TestHostManagementNoneAbsorbsTheGenerationRows(t *testing.T) {
	t.Run("no wrapper at all", func(t *testing.T) {
		o, _, _ := hostManagementFixture(t,
			`{"host_wrappers": true, "host_management": "none", "packs": ["claude"]}`, nil, "/bin")
		r, out := runPacksThenWrappers(t, o)
		if r.warned != 1 {
			t.Errorf("warned = %d, want 1 — `none` is the one cause:\n%s", r.warned, out)
		}
		if !strings.Contains(out, `host_management is "none", so `+"`yolo host apply`"+
			` refuses and no wrapper is generated for claude`) {
			t.Errorf("the none row must name what is left unwrapped:\n%s", out)
		}
		if strings.Contains(out, "no wrapper directory exists yet") {
			t.Errorf("a generation row offered the apply that refuses:\n%s", out)
		}
		if note := strings.Join(noteLinesAfter(t, out, `[WARN] host_management is "none"`), "\n"); !strings.Contains(note, noneGateSentence) {
			t.Errorf("the none row must say the key does nothing under none:\n%s", out)
		}
	})
	t.Run("a program added since", func(t *testing.T) {
		o, _, _ := hostManagementFixture(t,
			`{"host_wrappers": true, "host_management": "none", "packs": ["claude", "pi"]}`,
			[]string{"claude"}, "<WRAP>")
		r, out := runPacksThenWrappers(t, o)
		if r.warned != 1 {
			t.Errorf("warned = %d, want 1 — `none` is the one cause:\n%s", r.warned, out)
		}
		if !strings.Contains(out, "no wrapper is generated for pi") {
			t.Errorf("the none row must name the program with no wrapper:\n%s", out)
		}
		if strings.Contains(out, "have no wrapper: pi") {
			t.Errorf("a completeness row offered the apply that refuses:\n%s", out)
		}
		// claude's wrapper wins, but under none the launch gate is a no-op
		// (hostapplygate.go), so the key does nothing, and the none row is where that is said.
		if strings.Contains(out, "synchronizes host configuration automatically") {
			t.Errorf("under none no launch synchronizes anything; the PASS must not print:\n%s", out)
		}
		if note := strings.Join(noteLinesAfter(t, out, `[WARN] host_management is "none"`), "\n"); !strings.Contains(note, noneGateSentence) {
			t.Errorf("the none row must say the key does nothing under none:\n%s", out)
		}
	})
}

// noneGateSentence is what the host_management "none" row says about host_apply_on_launch.
const noneGateSentence = `host_apply_on_launch is on but does nothing under "none": there is no render for a launch to re-check.`

// TestHostManagementNoneIsWhyTheSyncCannotFire is review finding F3. Under "none" the launch
// gate returns before it looks at anything (hostapplygate.go), so host_apply_on_launch can
// never fire, whatever PATH says. The section used to PASS "synchronizes host configuration
// automatically" with the wrappers on PATH, and with them off PATH it put "cannot fire" on the
// not-on-PATH row, blaming PATH for what "none" causes. Fixing PATH fixes neither. The none
// row is the cause, so it carries the sentence, and no PATH row does.
func TestHostManagementNoneIsWhyTheSyncCannotFire(t *testing.T) {
	for _, tc := range []struct {
		name   string
		onPath bool
		warned int
	}{
		{"wrappers on PATH", true, 1},
		{"wrappers off PATH", false, 2}, // none, and the not-on-PATH row: two causes
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _ := hostManagementFixture(t,
				`{"host_wrappers": true, "host_management": "none"}`, []string{"claude"}, "")
			pathEnv := t.TempDir()
			if tc.onPath {
				pathEnv = wrapDirIn(t) + string(os.PathListSeparator) + pathEnv
			}
			setPath(o, pathEnv)
			var buf bytes.Buffer
			r := newReporter(&buf, false)
			o.sectionHostWrappers(r)
			out := buf.String()
			if r.warned != tc.warned {
				t.Errorf("warned = %d, want %d:\n%s", r.warned, tc.warned, out)
			}
			if strings.Contains(out, "synchronizes host configuration automatically") {
				t.Errorf("under none no launch synchronizes anything:\n%s", out)
			}
			note := strings.Join(noteLinesAfter(t, out, `[WARN] host_management is "none"`), "\n")
			if !strings.Contains(note, noneGateSentence) {
				t.Errorf("the none row must carry the key's state:\n%s", out)
			}
			if strings.Count(out, "host_apply_on_launch") != 1 {
				t.Errorf("host_apply_on_launch must be named once, on the none row:\n%s", out)
			}
			if strings.Contains(out, "cannot fire") {
				t.Errorf("no PATH row may blame PATH for what none causes:\n%s", out)
			}
		})
	}
}

// noteLinesAfter returns the note lines ("-> …" and their continuations) printed under the
// row whose line contains headline, or fails the test when no such row printed.
func noteLinesAfter(t *testing.T, out, headline string) []string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if !strings.Contains(l, headline) {
			continue
		}
		var note []string
		for _, n := range lines[i+1:] {
			trimmed := strings.TrimSpace(n)
			if trimmed == "" || strings.HasPrefix(trimmed, "[") {
				break
			}
			note = append(note, n)
		}
		return note
	}
	t.Fatalf("no row %q in:\n%s", headline, out)
	return nil
}
