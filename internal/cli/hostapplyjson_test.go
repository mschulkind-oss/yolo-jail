package cli

// hostapplyjson_test.go pins docs/reference/report-tiers.md machine consumers: the DRY RUN
// emits the survey as a document, `--assert` refuses the flag, and the two spellings of the
// verb agree.
//
// EVERY TEST HERE GOES THROUGH A COMMAND ENTRY POINT — hostApply or applyMain, with the real
// argv — never through buildHostApplyDoc. The flag family is parsed in one file and consumed in
// another, so a document builder driven directly would stay green against a `--format` nothing
// routes to it: the callee-pinned/call-site-unpinned shape AGENTS.md records this repo as
// having shipped five times.
//
// Every fixture declares `host_management: "own"`. The unset key is `none` since the `assert`
// retirement (OQ-CO14), and at both entry points a `none` apply refuses before any stage runs,
// so it has no survey to emit (TestHostManagementUnsetIsNoneAndSaysSoOnlyAtTheAct pins that
// refusal).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostApplyJSON runs `yolo host apply <args...>` and returns the parsed document, the raw
// stdout and stderr.
func hostApplyJSON(t *testing.T, args ...string) (hostApplyDoc, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := hostApply(args, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("host apply %v rc=%d\nstdout:\n%s\nstderr:\n%s", args, rc, out.String(), errw.String())
	}
	var doc hostApplyDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not a JSON document (%v):\n%s", err, out.String())
	}
	return doc, out.String(), errw.String()
}

// TestHostApplyDryRunEmitsTheSurveyAsADocument is the call-site pin and the shape assertion at
// once: the flag reaches the emitter, and what comes out is the SURVEY — the outcome token, the
// counts, the destinations with their tiers, and the tier-3 groups with their classes and
// remedy keys.
func TestHostApplyDryRunEmitsTheSurveyAsADocument(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")

	doc, raw, _ := hostApplyJSON(t, "--format", "json")

	if doc.Posture != "dry-run" {
		t.Errorf("posture = %q, want dry-run — it is the only posture that emits one", doc.Posture)
	}
	if doc.Outcome == "" || doc.Verdict == "" {
		t.Errorf("the document must carry both the token and the sentence: %+v", doc)
	}
	if doc.Home == "" {
		t.Error("the document does not say which home it is about")
	}
	if len(doc.Destinations) == 0 {
		t.Fatalf("no destinations in a fresh home's document — the survey reached the builder "+
			"empty:\n%s", raw)
	}
	for _, d := range doc.Destinations {
		if d.Kind == "" || d.Path == "" || d.Action == "" {
			t.Errorf("a destination is missing a field a consumer acts on: %+v", d)
		}
		if d.Tier != int(tierRun) && d.Tier != int(tierLoss) {
			t.Errorf("destination %q carries tier %d, which is not a report tier", d.Path, d.Tier)
		}
	}
	// A clean home loses nothing, so the group list is EMPTY here rather than absent — the
	// `[]`-not-`null` rule, which is what lets a consumer loop over it without a special case.
	// The populated shape is the next test's.
	if !strings.Contains(raw, `"groups": []`) {
		t.Errorf("a clean home's document must still carry an empty group list:\n%s", raw)
	}
	// The document is ANSI-free and on stdout, which is what makes it the form to parse.
	if strings.Contains(raw, "\x1b[") {
		t.Errorf("the document carries ANSI escapes:\n%q", raw)
	}
}

// TestHostApplyJSONCarriesEveryLossWithItsClassAndRemedy is the remedy contract in the machine
// form: grouping compresses the LINES, never the SET, so every name the text report shows is in
// the document — with the class a consumer branches on and the remedy key it was grouped by.
//
// The fixture is the one that produces a real loss: a pack contributing an MCP server the user
// already added by hand, which is the entry-level collision only a wholesale table render can
// see.
func TestHostApplyJSONCarriesEveryLossWithItsClassAndRemedy(t *testing.T) {
	home := hostMCPFixtureUnder(t, mcpContributorPackJSON, "own")
	writeFile(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`)

	doc, raw, _ := hostApplyJSON(t, "--json")

	var dropped *hostApplyDocGroup
	for i, g := range doc.Groups {
		if g.Class == "" {
			t.Errorf("a group carries no class, which is the field a consumer branches on: %+v", g)
		}
		// the remedy contract: a group states a remedy or says why there is none — never both, never
		// neither.
		if (g.Remedy == "") == (g.NoRemedy == "") {
			t.Errorf("group %q states %d of {remedy, no_remedy}, want exactly 1: %+v",
				g.Class, boolsSet(g.Remedy != "", g.NoRemedy != ""), g)
		}
		if g.Class == remedyClassEntryDropped {
			dropped = &doc.Groups[i]
		}
	}
	if dropped == nil {
		t.Fatalf("the user's hand-added MCP entry would be dropped and no group says so:\n%s", raw)
	}
	if !slices.Contains(dropped.Items, "tavily") {
		t.Errorf("the group does not NAME the entry that would go: %+v", dropped.Items)
	}
	if dropped.Key != mcpEntryRemedyKey(home) {
		t.Errorf("group key = %q, want the file the losses share a fix in (%q)",
			dropped.Key, mcpEntryRemedyKey(home))
	}
	if doc.Counts.EntriesDropped != 1 {
		t.Errorf("entries_dropped = %d, want 1", doc.Counts.EntriesDropped)
	}
	// THE USER'S OWN VALUE IS NEVER IN ANY VIEW (the remedy contract's first forbidden thing): a
	// document is pasted into a bug report exactly as a transcript is.
	if strings.Contains(raw, "SECRET") {
		t.Errorf("the document carries the user's existing value:\n%s", raw)
	}
}

func boolsSet(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// TestHostApplyJSONStdoutCarriesTheDocumentAndNothingElse: nothing may be printed beside the
// document, and a whole-stream parse is the only assertion that says so. It was written for
// `--shell-init`, a second stage with its own human output that ran AFTER the report; that
// flag is removed (HE-D1), and the whole-stream parse stays, because any stage added after the
// report would break it the same way — and so would a pack refresh that wrote to stdout.
func TestHostApplyJSONStdoutCarriesTheDocumentAndNothingElse(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")
	t.Setenv("SHELL", "/bin/bash")

	var out, errw bytes.Buffer
	if rc := hostApply([]string{"--json"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	var doc hostApplyDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document (%v) — something printed beside it:\n%s",
			err, out.String())
	}
}

// TestHostApplyJSONAndTextAreOneAnswer: the token, the sentence and the counts are produced
// from ONE observe pass's survey, so the document and the report cannot disagree about how the
// run went. Without this the two forms are two models of the same apply, which is the drift
// machine consumers rules the recording shape to avoid.
func TestHostApplyJSONAndTextAreOneAnswer(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")

	doc, _, _ := hostApplyJSON(t, "--json")
	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("text dry run rc=%d\n%s", rc, report)
	}

	if !strings.Contains(report, doc.Verdict) {
		t.Errorf("the text report does not end in the document's verdict %q:\n%s", doc.Verdict, report)
	}
	if doc.Outcome != outcomeWouldComplete {
		t.Errorf("outcome = %q, want %q for a first apply with work and no blocker",
			doc.Outcome, outcomeWouldComplete)
	}
	// One count, checked against the text form's own sentence: the document's numbers are the
	// survey's, and so are the report's.
	if n := doc.Counts.ConfigFilesChanged; n == 0 ||
		!strings.Contains(report, strconv.Itoa(n)+" config files") {
		t.Errorf("config_files_changed = %d, which the report does not state:\n%s", n, report)
	}
}

// TestHostApplyJSONNamesTheInapplicableKindsAndCarriesNoProse is the report vocabulary's half
// of machine consumers: the document states WHICH kinds do not apply at this notch and none of
// their reasons, because rationale is not data. The prose is read from internal/render rather
// than retyped, so a reason that changes wording cannot quietly start passing.
func TestHostApplyJSONNamesTheInapplicableKindsAndCarriesNoProse(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")

	doc, raw, _ := hostApplyJSON(t, "--format=json")

	if len(doc.InapplicableKinds) == 0 {
		t.Fatal("the six shipped packs declare kinds this notch does nothing with, and the " +
			"document names none of them")
	}
	fields := render.HostFields()
	for _, name := range doc.InapplicableKinds {
		kind := packdecl.Kind(name)
		reason := fields.Refuse(kind)
		if reason == "" {
			if r, unbuilt := render.HostUnimplemented(kind); unbuilt {
				reason = r
			}
		}
		if reason == "" {
			continue // a kind whose reason render does not carry has no prose to leak
		}
		if strings.Contains(raw, reason) {
			t.Errorf("the document carries %s's prose reason — rationale is not data (the report vocabulary):\n%s",
				name, reason)
		}
	}
	if len(doc.AtLaunchKinds) == 0 {
		t.Fatal("the six shipped packs declare kinds `yolo host --` delivers, and the document " +
			"names none of them")
	}
	for _, name := range doc.AtLaunchKinds {
		if reason, ok := render.HostAtLaunch(packdecl.Kind(name)); ok && strings.Contains(raw, reason) {
			t.Errorf("the document carries %s's at-launch prose — rationale is not data:\n%s", name, reason)
		}
	}
}

// TestHostApplyJSONNamesTheAtLaunchKinds: the document's two kind lists are the text line's two
// clauses. pi's env (a plain variable, and aws-auth's credential pointer) is delivered by
// `yolo host --`, so it is an at-launch kind and no longer an inapplicable one, which is the
// document half of the at-launch regression.
func TestHostApplyJSONNamesTheAtLaunchKinds(t *testing.T) {
	home := t.TempDir()
	selectPacksWith(t, home, `"pi"`, `,"host_management":"own"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	doc, raw, _ := hostApplyJSON(t, "--json")
	if !slices.Contains(doc.AtLaunchKinds, string(packdecl.KindEnv)) {
		t.Errorf("at_launch_kinds = %v, want env among them:\n%s", doc.AtLaunchKinds, raw)
	}
	if slices.Contains(doc.InapplicableKinds, string(packdecl.KindEnv)) {
		t.Errorf("inapplicable_kinds = %v names env, which `yolo host --` delivers:\n%s",
			doc.InapplicableKinds, raw)
	}
	// The same survey as the text line: every kind it names is in the document's lists.
	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("text dry run rc=%d\n%s", rc, report)
	}
	clauses := notchClauses(notchKindLines(report)[0])
	for _, k := range doc.AtLaunchKinds {
		if countWord(clauses[atLaunchClause], k) != 1 {
			t.Errorf("at_launch_kinds names %s and the text line's at-launch clause does not: %q",
				k, clauses[atLaunchClause])
		}
	}
}

// TestHostApplyAssertRefusesTheDocumentAndWritesNothing is [OQ-RO4]'s acting half: exit 2,
// stdout EMPTY, and — the claim the exit code alone cannot make — the home untouched, because
// the refusal is above the render rather than after it.
func TestHostApplyAssertRefusesTheDocumentAndWritesNothing(t *testing.T) {
	home := shippedPacksFixtureUnder(t, "own")
	before := linkAwareHashes(t, home)

	for _, args := range [][]string{
		{"--assert", "--format", "json"},
		{"--json", "--assert"},
		{"--assert", "--format=json"},
	} {
		var out, errw bytes.Buffer
		rc := hostApply(args, &out, &errw, false, nil)
		if rc != 2 {
			t.Errorf("host apply %v rc=%d, want 2 (misuse)\nstdout:\n%s\nstderr:\n%s",
				args, rc, out.String(), errw.String())
		}
		if out.Len() != 0 {
			t.Errorf("host apply %v wrote %d bytes to stdout; a refused document must leave "+
				"it empty, or a consumer parses half an answer:\n%s", args, out.Len(), out.String())
		}
		if !strings.Contains(errw.String(), "yolo host apply:") {
			t.Errorf("host apply %v refused without saying why on stderr:\n%s", args, errw.String())
		}
	}
	if after := linkAwareHashes(t, home); !sameHashes(before, after) {
		t.Error("the refused --assert wrote into the home — the refusal must come before the " +
			"render, not after it")
	}
}

// sameHashes compares two linkAwareHashes snapshots.
func sameHashes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestApplyAtHostEmitsTheDocumentAtBothSpellings: `yolo apply --at host` and `yolo host apply`
// are one operation and differ only in how they are typed (OQ-7), so a flag that worked at one
// of them would be a flag an agent has to guess about.
func TestApplyAtHostEmitsTheDocumentAtBothSpellings(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "host", "--format", "json"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --at host --format json rc=%d\nstdout:\n%s\nstderr:\n%s",
			rc, out.String(), errw.String())
	}
	var viaApply hostApplyDoc
	if err := json.Unmarshal(out.Bytes(), &viaApply); err != nil {
		t.Fatalf("stdout is not a JSON document (%v):\n%s", err, out.String())
	}
	viaHost, _, _ := hostApplyJSON(t, "--json")

	if viaApply.Outcome != viaHost.Outcome || viaApply.Verdict != viaHost.Verdict {
		t.Errorf("the two spellings disagree about the outcome: %q/%q vs %q/%q",
			viaApply.Outcome, viaApply.Verdict, viaHost.Outcome, viaHost.Verdict)
	}
	if len(viaApply.Destinations) != len(viaHost.Destinations) {
		t.Errorf("the two spellings report %d and %d destinations",
			len(viaApply.Destinations), len(viaHost.Destinations))
	}
}

// TestApplyRefusesTheDocumentWhereThereIsNoneToEmit: `--sealed` refuses rather than reports,
// the guest notch is unbuilt and the jail notch's apply points at launch. Answering any of the
// three with a human report is the exact failure the flag exists to prevent.
func TestApplyRefusesTheDocumentWhereThereIsNoneToEmit(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")

	for _, args := range [][]string{
		{"--at", "jail", "--format", "json"},
		{"--at", "guest", "--json"},
		{"--sealed", "--json"},
	} {
		var out, errw bytes.Buffer
		rc := applyMain(args, &out, &errw, false, nil)
		if rc != 2 {
			t.Errorf("apply %v rc=%d, want 2\nstdout:\n%s\nstderr:\n%s",
				args, rc, out.String(), errw.String())
		}
		if out.Len() != 0 {
			t.Errorf("apply %v wrote to stdout instead of refusing:\n%s", args, out.String())
		}
	}
}

// TestHostApplyRefusesAFormatItCannotEmit is the front end's own refusal, reached through this
// command: an unknown value exits 2 with nothing on stdout, rather than printing prose to
// something that asked for data.
func TestHostApplyRefusesAFormatItCannotEmit(t *testing.T) {
	shippedPacksFixtureUnder(t, "own")

	var out, errw bytes.Buffer
	if rc := hostApply([]string{"--format", "yaml"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("rc=%d, want 2 for an unknown format\nstdout:\n%s\nstderr:\n%s",
			rc, out.String(), errw.String())
	}
	if out.Len() != 0 {
		t.Errorf("an unknown format still produced stdout:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "yaml") {
		t.Errorf("the refusal does not name the value it refused:\n%s", errw.String())
	}
}

// TestHostApplyJSONWithNoPacksIsStillADocument: *"nothing to report must still be a document"*
// (machine consumers). The zero-packs branch returns early — it is the branch that used to end
// with no verdict, no counts and no footer at all — so it is the one most likely to end with no
// document either.
func TestHostApplyJSONWithNoPacksIsStillADocument(t *testing.T) {
	home := t.TempDir()
	selectPacksWith(t, home, ``, `,"host_management":"own"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	doc, raw, _ := hostApplyJSON(t, "--json")

	if doc.Outcome != outcomeNoPacks {
		t.Errorf("outcome = %q, want %q — an empty `packs` is a different result from a home "+
			"that is already up to date:\n%s", doc.Outcome, outcomeNoPacks, raw)
	}
	// `[]`, never `null`: a consumer looping over a list must not have to special-case the run
	// that found nothing.
	for _, want := range []string{
		`"destinations": []`, `"groups": []`, `"inapplicable_kinds": []`, `"at_launch_kinds": []`,
		`"failed_packs": []`, `"failed_stages": []`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("the empty document is missing %s:\n%s", want, raw)
		}
	}
}

// TestHostApplyJSONNamesAMissingDependencyAsABlocker: the dependency rule's dry-run row, in the
// machine form. The blocker decides the outcome, so a consumer that reads only `outcome` still
// learns that an --assert would not complete — and the group carries the binary as its remedy
// key, which is what a consumer would act on.
func TestHostApplyJSONNamesAMissingDependencyAsABlocker(t *testing.T) {
	home, briefing, _ := depGateFixture(t,
		`{"kind":"program","bin":"jsonbin","via":"npm","package":"jsonbin"}`)

	doc, raw, _ := hostApplyJSON(t, "--json")

	if doc.Outcome != outcomeBlocked {
		t.Errorf("outcome = %q, want %q with a declared dependency missing:\n%s",
			doc.Outcome, outcomeBlocked, raw)
	}
	if doc.Counts.DependenciesMissing != 1 {
		t.Errorf("dependencies_missing = %d, want 1", doc.Counts.DependenciesMissing)
	}
	found := false
	for _, g := range doc.Groups {
		if g.Class == remedyClassDependency && g.Key == "jsonbin" {
			found = true
			if g.Remedy == "" {
				t.Errorf("the blocker group states no remedy: %+v", g)
			}
		}
	}
	if !found {
		t.Errorf("no %s group keyed on the missing binary:\n%s", remedyClassDependency, raw)
	}
	// A dry run writes nothing and installs nothing, whatever form it reports in.
	if _, err := os.Stat(briefing); !os.IsNotExist(err) {
		t.Errorf("%s exists — the dry run wrote", briefing)
	}
	if _, err := os.Stat(filepath.Join(home, ".gate")); !os.IsNotExist(err) {
		t.Error("the dry run created a destination directory")
	}
}
