package cli

// hostapplyremedy_test.go pins docs/design/report-tiers.md §9 step 4: tier 3 is grouped by
// REMEDY KEY, each group states its remedy ONCE, and every group is represented in the verdict.
//
// The three assertions are three different failures, and the middle one is the defect that
// prompted the step: three surfaces dropping one hand-added MCP server printed three `⚠` lines
// carrying three copies of one fix, and a Contains-style test passes against every one of them.
// So the counts here are counts, never Contains.
//
// All of it goes through applyHostSurveyed, which is the call site: deleting printRemedyGroups
// from apply.go leaves the remedies unprinted and fails the first two, and dropping a class from
// hostApplyCounts fails the third.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostApplyGroupsOneEntryLossAcrossAgentsUnderOneRemedy is the grouping proper. One server,
// three agent surfaces, one fix — and the fix appears once.
func TestHostApplyGroupsOneEntryLossAcrossAgentsUnderOneRemedy(t *testing.T) {
	home, surfaces := multiAgentMCPFixture(t)

	survey, report := surveyApply(t)

	// The fixture really does produce the repetition being collapsed: the per-surface loss
	// line still fires once per surface, and it is the REMEDY that is stated once.
	if got := strings.Count(report, "would damage your existing entry"); got != surfaces {
		t.Fatalf("fixture bug: %d per-surface loss lines, want %d\n%s", got, surfaces, report)
	}
	groups := hostApplyRemedyGroups(survey, home, false)
	entryGroups := groupsWithKey(groups, mcpEntryRemedyKey)
	if len(entryGroups) != 1 {
		t.Fatalf("one server dropped from %d surfaces is ONE group keyed on the config key "+
			"that keeps it; got %d groups: %+v\n%s", surfaces, len(entryGroups), groups, report)
	}
	remedy := mcpEntryRemedy(home)
	if n := strings.Count(report, remedy); n != 1 {
		t.Errorf("the remedy is stated once for the group, not once per surface; it appears "+
			"%d times:\n%s", n, report)
	}
	// P2: the remedy names the FILE the declaration goes in and the SCOPE it covers. The
	// per-surface copy this replaced had neither, and the scope is the half that turns three
	// problems into one.
	if !strings.Contains(remedy, filepath.Join(home, ".config", "yolo-jail", "config.jsonc")) {
		t.Errorf("the remedy must name the file the declaration goes in: %q", remedy)
	}
	if !strings.Contains(remedy, "every agent") {
		t.Errorf("the remedy must name the scope it covers — one entry reaching every agent "+
			"is why this is one group: %q", remedy)
	}
	// §4.4: grouping compresses the LINES, never the SET.
	for _, name := range survey.DroppedEntryNames() {
		if !strings.Contains(report, name) {
			t.Errorf("entry %q is in no line of the default view — grouping may not drop a "+
				"name from a loss group:\n%s", name, report)
		}
	}
}

// TestHostApplyStatesTheMCPRemedyFromOnePlace is the three-copies half. The per-surface line,
// the not-confirmed abort and confirmHostLosses' trailer were three strings that had already
// drifted; they are one now, so a decline reaches the SAME sentence the report does.
func TestHostApplyStatesTheMCPRemedyFromOnePlace(t *testing.T) {
	// The abort path: an --assert into a home whose ~/.claude.json already holds an entry a
	// pack is about to regenerate prompts, and a NO stops it. Reached only on a FIRST apply,
	// so it needs its own unsettled home.
	fresh := hostMCPFixture(t, mcpContributorPackJSON)
	writeFile(t, filepath.Join(fresh, ".claude.json"),
		`{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`)
	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, strings.NewReader("n\n")); rc == 0 {
		t.Fatalf("a declined first apply must not proceed\n%s%s", out.String(), errw.String())
	}
	declined := out.String() + errw.String()
	// BOTH halves of the decline path — confirmHostLosses' own trailer and the abort message
	// after it — read the one string, so the reader is told the same thing twice at most and
	// never two different things. It used to be two sentences that had already diverged.
	if n := strings.Count(declined, mcpEntryRemedy(fresh)); n < 2 {
		t.Errorf("the prompt trailer and the abort message must both carry the ONE remedy — "+
			"three copies of this sentence had already drifted apart (§3.3); it appears %d "+
			"time(s):\n%s", n, declined)
	}
	// And the per-surface line no longer carries its own copy: that is what made three
	// surfaces three fixes.
	if strings.Contains(declined, "declare the entry under") {
		t.Errorf("the per-surface line must not carry a second copy of the remedy:\n%s", declined)
	}
}

// TestHostApplyVerdictRepresentsEveryRemedyGroup is §4.3's rule that grouping may compress the
// lines above the verdict and may never leave the verdict silent about a class.
//
// Asserted over whatever groups the run produced rather than a written-down list, because the
// list is the thing that goes stale: a class added later joins this test by existing, and one
// that stops being counted fails it.
func TestHostApplyVerdictRepresentsEveryRemedyGroup(t *testing.T) {
	home, _ := multiAgentMCPFixture(t)
	// A second class, so "every group" is a claim about more than one: a hand-edited managed
	// key is a REPLACED VALUE, which is the §4.4 class with no remedy at all — the one most
	// at risk of being dropped from a verdict, since nothing about it is actionable.
	settings := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	edited := strings.Replace(string(data), `"defaultMode": "default"`,
		`"defaultMode": "plan"`, 1)
	if edited == string(data) {
		t.Fatalf("fixture bug: no managed key to edit in %s:\n%s", settings, data)
	}
	if err := os.WriteFile(settings, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	// And a THIRD: a comment above a managed key whose value this run changes. That is the
	// class with no remedy POSSIBLE, and it was the one the verdict had no term for at all
	// while configResultTier was already counting it as a tier-3 loss — the exact shape of a
	// class that compresses out of the report and is never missed.
	handEditForCommentLoss(t, home)

	survey, report := surveyApply(t)
	groups := hostApplyRemedyGroups(survey, home, false)
	if len(groups) < 2 {
		t.Fatalf("fixture bug: %d tier-3 group(s), so \"every group\" is nearly free\n%s",
			len(groups), report)
	}
	verdict := hostApplyVerdict(survey, false) + "\n" +
		strings.Join(hostApplyCounts(survey, false), "\n")
	for _, g := range groups {
		if !strings.Contains(verdict, g.VerdictTerm) {
			t.Errorf("the %q group is not represented in the verdict block — a reader who "+
				"stops at the result would never learn this class happened (§4.3).\ngroup: "+
				"%s\nverdict block:\n%s", g.Key, g.Headline, verdict)
		}
	}
	// A group states a remedy OR says there is none; never neither, and never both. §3.5: a
	// loss with no remedy says so rather than wearing a `⚠` it cannot cash.
	for _, g := range groups {
		switch {
		case g.Remedy != "" && g.NoRemedy != "":
			t.Errorf("group %q carries both a remedy and a reason there is none: %+v", g.Key, g)
		case g.Remedy == "" && g.NoRemedy == "":
			t.Errorf("group %q carries neither a remedy nor a reason there is none — that is "+
				"the `⚠` with nothing behind it §3.5 names: %+v", g.Key, g)
		case g.Remedy == "" && g.Warn:
			t.Errorf("group %q has no remedy and still warns: %+v", g.Key, g)
		}
	}
}

// TestHostApplyGroupsOneMissingBinaryAcrossPacks is the blocker half of the remedy key: two
// packs declaring one binary are one group with one install command, where the old report
// printed the command under each declaration.
func TestHostApplyGroupsOneMissingBinaryAcrossPacks(t *testing.T) {
	fakeBinDir(t, "apt")
	home := twoPacksOneMissingBinFixture(t)

	survey, report := surveyApply(t)

	// THE FIXTURE'S OWN EVIDENCE — two declarations of one binary — is read off the
	// per-contribution lines, which §4.5 moved behind the flag. So it is measured in the
	// verbose view and everything below is measured in the DEFAULT one: the whole claim is
	// that two lines up there become one group down here, and reading both from the compressed
	// view would leave the "two" unproven.
	func() {
		verboseReport(t)
		var out, errw bytes.Buffer
		if rc := applyHostSurveyed(&out, &errw, false, false, nil, &hostApplySurvey{}); rc != 0 {
			t.Fatalf("verbose observe rc=%d\n%s%s", rc, out.String(), errw.String())
		}
		if got := strings.Count(out.String()+errw.String(), "MISSING"); got < 2 {
			t.Fatalf("fixture bug: %d per-contribution MISSING lines, want one per declaring "+
				"pack\n%s", got, out.String()+errw.String())
		}
	}()
	groups := groupsWithKey(hostApplyRemedyGroups(survey, home, false), "sharedbin")
	if len(groups) != 1 {
		t.Fatalf("two packs declaring one binary are ONE blocker on ONE host; got %d "+
			"groups\n%s", len(groups), report)
	}
	if n := strings.Count(report, "sudo apt install -y shared-pkg"); n != 1 {
		t.Errorf("the install command is stated once for the binary, not once per declaring "+
			"pack; it appears %d times:\n%s", n, report)
	}
	if !strings.Contains(report, "sharedbin") {
		t.Errorf("the missing binary must be named:\n%s", report)
	}
}

// handEditForCommentLoss puts a comment above a managed TOML key and moves that key off its
// managed value, so the next render must change the key and drop the comment with it (the
// comment would otherwise be left lying about a value that is gone).
func handEditForCommentLoss(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	var out []string
	var edited bool
	for _, line := range strings.Split(string(data), "\n") {
		if !edited && strings.HasPrefix(line, "approval_policy = ") {
			out = append(out, "# I want to be prompted for everything",
				`approval_policy = "never"`)
			edited = true
			continue
		}
		out = append(out, line)
	}
	if !edited {
		t.Fatalf("fixture bug: no approval_policy line to comment in %s:\n%s", path, data)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// twoPacksOneMissingBinFixture points a throwaway $HOME at two local packs that both declare
// the same missing `requires` binary, with the same install hint.
func twoPacksOneMissingBinFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	var sources []string
	for _, name := range []string{"deponeA", "deponeB"} {
		dir := filepath.Join(t.TempDir(), name)
		writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"`+name+`","contributes":[`+
			`{"kind":"requires","bin":"sharedbin","install_hints":{"apt":"shared-pkg"}}]}`)
		sources = append(sources, `"file://`+dir+`"`)
	}
	selectPacks(t, home, strings.Join(sources, ","))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// groupsWithKey selects the groups formed on one remedy key. The KEY is what the grouping is
// on, so counting by key is the assertion — counting lines would pass for a report that
// happened to print one line about two groups.
func groupsWithKey(groups []remedyGroup, key string) []remedyGroup {
	var out []remedyGroup
	for _, g := range groups {
		if g.Key == key {
			out = append(out, g)
		}
	}
	return out
}
