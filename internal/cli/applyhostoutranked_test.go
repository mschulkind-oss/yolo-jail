package cli

// applyhostoutranked_test.go is the OUTPUT half of finding F4: `yolo host apply` must print an
// outranked overlay key by name, with its cause, and must not print the ⚠ overwrite warning
// in a way that reads as though the overlay won.
//
// The report is what is under test, not the precedence — the guarded posture owning
// `permissions.defaultMode` at the host notch is the autonomy-leak fix and stays. What broke
// was legibility: the overlay was accepted, LISTED as contributing, and then lost, while the
// ⚠ line fired for the same key. Every test writes into a t.TempDir() home with its own
// config; the real $HOME is never read or written.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE FIELD-REPORT INVOCATION: `claude` plus two overlay packs both asking for acceptEdits.
// The report names the key, names both packs, names the posture that owns it, and the file
// still carries the guarded value.
func TestApplyHostNamesTheOutrankedOverlayKey(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{
		"p1": claudeDefaultModeOverlayJSON("p1"),
		"p2": claudeDefaultModeOverlayJSON("p2"),
	})
	// `claude` is embedded, so it joins the fixture's `packs` by bare name.
	addPackToConfig(t, home, `"claude"`)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	// Asserted against the ONE line that names the key, not against the whole report: the
	// `autonomy` census line already prints the word "autonomy" for every claude apply, so a
	// report-wide substring check passes even when the attribution is missing (verified by
	// mutation — it is exactly the false green this narrowing removes).
	line := lineContaining(t, report, "IGNORED")
	for _, want := range []string{
		"permissions.defaultMode", // the key that lost
		"p1", "p2",                // who declared it
		"autonomy posture", // and why it lost — a policy, not a bug
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the outranked line is missing %q — the loss is still illegible while the "+
				"overlay is listed as contributing: %q\nfull output:\n%s", want, line, report)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read the rendered settings: %v", err)
	}
	if strings.Contains(string(data), "acceptEdits") {
		t.Errorf("the guarded posture must still win — this is a reporting fix only:\n%s", data)
	}
}

// The ⚠ line must not name an outranked pack. With a user value in place the managed write IS
// an overwrite, so the warning fires — and attributing it to the overlay is exactly what made
// the output read as a win.
func TestApplyHostOverwriteWarningDoesNotCreditAnOutrankedOverlay(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{"pushy": claudeDefaultModeOverlayJSON("pushy")})
	addPackToConfig(t, home, `"claude"`)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"permissions":{"defaultMode":"plan"}}`)

	var out, errw bytes.Buffer
	// Observe: the honest report has to precede the write.
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("host apply rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, "overwrite") {
			continue
		}
		if strings.Contains(line, "pushy") {
			t.Errorf("the overwrite warning credits the OUTRANKED overlay `pushy` with a value "+
				"it never writes: %q", line)
		}
	}
	if !strings.Contains(report, "permissions.defaultMode") {
		t.Errorf("the managed layer does overwrite the user's value and must still say so:\n%s",
			report)
	}
}

// The negative case at the OUTPUT layer: a winning overlay key must not acquire an IGNORED
// line, or the fix has traded one misleading report for another.
func TestApplyHostDoesNotReportAWinningOverlayKeyAsIgnored(t *testing.T) {
	writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON, // contributes fileSuggestion, which acme does not manage
	})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	if strings.Contains(report, "IGNORED") {
		t.Errorf("no overlay key was outranked here, so nothing may be reported as IGNORED:\n%s",
			report)
	}
	// The R3 contribution line is untouched by any of this.
	if !strings.Contains(report, "config-overlay keys from: acme-fzf") {
		t.Errorf("the R3 contribution line regressed:\n%s", report)
	}
}

// lineContaining returns the single report line containing marker, failing when there is not
// exactly one. Exactly-one is the point: two IGNORED lines for one key would mean the report
// grew a duplicate, which is its own legibility bug.
func lineContaining(t *testing.T, report, marker string) string {
	t.Helper()
	var found []string
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, marker) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one line containing %q, got %d:\n%s", marker, len(found), report)
	}
	return found[0]
}

// claudeDefaultModeOverlayJSON is an overlay pack contesting the SHIPPED claude/settings
// surface's autonomy-owned defaultMode — the field report's exact shape.
func claudeDefaultModeOverlayJSON(name string) string {
	return `{"name":"` + name + `","contributes":[
	  {"kind":"config-overlay","surface":"claude/settings",
	   "config":{"managed":{"permissions":{"defaultMode":"acceptEdits"}}}}]}`
}

// addPackToConfig appends one raw `packs` entry to the fixture's config, so an EMBEDDED pack
// (selected by bare name) can join the file:// packs writeOverlayFixture wrote. Appended
// rather than prepended because the fold order is the config order, and the field report's
// case has the overlays contributed to a pack already in the list.
func addPackToConfig(t *testing.T, home, entry string) {
	t.Helper()
	path := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(data), `"packs":[`, `"packs":[`+entry+`,`, 1)
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}
