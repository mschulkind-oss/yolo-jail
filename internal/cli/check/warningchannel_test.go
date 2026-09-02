package check

// THE WARNING CHANNEL — step 1 of docs/design/reference-mismatch-diagnostics.md §7.
//
// `yolo check` used to have TWO diagnostic channels and a summary that aggregated one:
// graded [WARN] rows incremented the count behind the summary line, while the bare
// "Warning: <msg>" lines that config resolution and pack loading emitted did not. The
// doc's §3 measured it — three bogus env_sources entries printed FIVE Warning lines
// under a summary reading "2 warnings" — and named the cost: the best mismatch
// diagnostic in the tree (an unmatched `supersedes`, with its did-you-mean and its
// candidate set) was invisible to the one line a user reads.
//
// Every config.Warn sink in this package is now r.configWarn, which is r.warn. These
// tests pin that at the CALL SITE, in the shape AGENTS.md demands: each one drives the
// PRODUCTION path (Check(), or the section method Check() calls) with a real config that
// makes a loader warn, and asserts the SUMMARY LINE's number moved. Deleting the
// r.configWarn argument from check.go / packs.go / entrypoint.go — or handing those
// loaders a discarding func, which is what the old ungraded channel amounted to as far
// as the summary was concerned — turns each of them red. Verified by mutation, both
// directions; see the header comments on the individual tests.
//
// What these deliberately do NOT assert: any change in EXIT CODE. Check() returns 1 on
// r.failed alone (its three gates), and r.warned is read only by the two summary
// renderers — so grading these findings counts them without starting to refuse
// anything. That is why step 1 needed no ruling on OQ-RM1, and
// TestGradedConfigWarningsDoNotChangeTheExitCode below is the assertion that keeps it
// that way.
//
// TWO OF THE THREE CALL SITES HAVE NO END-TO-END TEST, and it is not an oversight.
// (This header said ONE until an adversarial review re-measured it on 2026-09-02 and
// found the packs sink has the identical defect — the first version had done the
// measurement for check.go and assumed packs.go differed.)
//
// Both LoadCacheRelocations' sink (check.go) and LoadPacks' (packs.go) are UNREACHABLE
// from Check() today, for the same structural reason: every problem either can report
// through warn is ALSO a ValidateConfig error in the same words, because validation and
// the loader run the same checker (config's checkCacheRelocations; validatePacks calling
// the same checkPacks at config/packs.go:477). So sectionMergedConfig emits [FAIL] and
// the accumulated-fail gate returns before either loader runs — measured across five
// reportable shapes each. There is therefore no config that makes those two warnings
// appear, and a test claiming to exercise them end-to-end would be asserting on a branch
// it never reached. TestSkippedPackEntryWarningReachesTheSummary calls sectionPacks
// DIRECTLY for exactly that reason: it is a section test, not an end-to-end one.
//
// ResolveEnvSources (entrypoint.go) is the reachable one, and it is the case
// reference-mismatch-diagnostics.md §3 actually measured — a missing env_sources file is
// portability, never a validation error (validate.go rejects only shape), so nothing
// short-circuits ahead of it. TestEnvSourcesWarningsReachTheSummary is the end-to-end
// proof; TestEveryConfigWarnSinkIsGraded is what covers the other two, structurally,
// which is as strong as their reachability permits.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// summaryWarnCount extracts N from the summary line's "N warnings" fragment. It reads
// the RENDERED LINE rather than r.warned because the summary is the surface the doc is
// about: a finding that increments a field nothing prints is exactly the defect in the
// other direction.
//
// ok=false means no summary warning fragment was rendered at all (the summary omits the
// fragment when the count is zero), which the callers treat as zero-and-say-so.
func summaryWarnCount(t *testing.T, out string) (int, bool) {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*(?:\d+ passed, )?(?:\d+ failed, )?(\d+) warnings\s*$`).
		FindStringSubmatch(stripANSI(out))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unparseable warning count %q in:\n%s", m[1], out)
	}
	return n, true
}

// warnChannelFixture builds a Check() fixture that reaches the Entrypoint Dry-Run
// (i.e. past every fail gate) with a workspace config of the caller's choosing, and
// returns the workspace dir so a test can add files to it.
//
// Reaching that far matters: the env_sources call site lives in
// runEntrypointPreflight, which Check() only calls after the accumulated-fail gate and
// the Packs section, so a fixture that fails earlier cannot see it at all.
func warnChannelFixture(t *testing.T, out *bytes.Buffer, workspaceConfig string) Options {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	opts := baseOptions(t, out)

	ws := t.TempDir()
	opts.Workspace = ws
	if workspaceConfig != "" {
		must(t, os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(workspaceConfig), 0o644))
	}

	repo := t.TempDir()
	must(t, os.WriteFile(filepath.Join(repo, "flake.nix"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test\n"), 0o644))
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
	}
	opts.PathExists = func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	// In-jail, so the host-only sections skip with a PASS instead of failing on a
	// fixture host that has no podman socket.
	opts.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.1.0-test"
		}
		return ""
	}
	opts.LookPath = func(name string) (string, bool) {
		switch name {
		case "podman", "nix":
			return "/usr/bin/" + name, true
		}
		return "", false
	}
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"podman info":      {Stdout: "host: {}", Ran: true, RC: 0},
		"nix --version":    {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"nix config show":  {Stdout: "", Ran: true, RC: 0},
		"podman images":    {Stdout: "", Ran: true, RC: 0},
		"podman ps":        {Stdout: "", Ran: true, RC: 0},
	})
	return opts
}

// TestEnvSourcesWarningsReachTheSummary is the doc's §3 measurement, inverted into an
// assertion: TWO env_sources entries naming files that are not on this machine must move
// the summary's warning count BY TWO.
//
// It runs the whole of Check(), so the wiring under test is the production argument at
// entrypoint.go — `config.ResolveEnvSources(workspace, merged, r.configWarn)`. MUTATION
// VERIFIED: replacing that third argument with `func(string) {}` (the discarding sink,
// which is behaviourally what the old ungraded warningLine was to the summary) drops the
// delta to 0 and this test fails with "the summary's warning count did not move".
//
// The delta is measured against a control run over the SAME fixture with no env_sources
// key, rather than against a hardcoded number, because the fixture's other warnings
// (missing global-storage dirs, no packs, --no-build) are incidental to this contract and
// would make an absolute assertion break on every unrelated change.
func TestEnvSourcesWarningsReachTheSummary(t *testing.T) {
	var control bytes.Buffer
	Check(warnChannelFixture(t, &control, `{}`))
	before, ok := summaryWarnCount(t, control.String())
	if !ok {
		t.Fatalf("control run rendered no warning count at all:\n%s", control.String())
	}

	var out bytes.Buffer
	opts := warnChannelFixture(t, &out, `{"env_sources": ["ghost-a.env", "ghost-b.env"]}`)
	Check(opts)
	got := stripANSI(out.String())

	after, ok := summaryWarnCount(t, got)
	if !ok {
		t.Fatalf("run rendered no warning count at all:\n%s", got)
	}
	if after-before != 2 {
		t.Errorf("the summary's warning count did not move by the two unresolvable "+
			"env_sources entries: %d -> %d (delta %d, want 2). This is the two-channel "+
			"defect of reference-mismatch-diagnostics.md §3 — the findings printed and "+
			"the summary did not count them:\n%s", before, after, after-before, got)
	}
	// Both findings must still be VISIBLE, and as graded rows: counting them while
	// dropping the text would trade one half of the defect for the other.
	for _, name := range []string{"ghost-a.env", "ghost-b.env"} {
		if !strings.Contains(got, name) {
			t.Errorf("the finding for %s is not in the output at all:\n%s", name, got)
		}
	}
	if n := strings.Count(got, "[WARN] env_sources file not found"); n != 2 {
		t.Errorf("want 2 graded [WARN] rows for the missing env_sources files, got %d — "+
			"an ungraded 'Warning:' line is the channel this step retired:\n%s", n, got)
	}
	if strings.Contains(got, "Warning: env_sources") {
		t.Errorf("an env_sources finding is still on the retired ungraded channel:\n%s", got)
	}
}

// TestGradedConfigWarningsDoNotChangeTheExitCode is the boundary this step must not
// cross. Grading a previously-ungraded finding is a REPORTING change; if it also made
// `yolo check` exit non-zero it would be a refusal, which is OQ-RM1's territory and
// unruled. Check() returns 1 on r.failed alone — this pins that against the exact
// fixture above, whose only findings are warnings.
func TestGradedConfigWarningsDoNotChangeTheExitCode(t *testing.T) {
	var out bytes.Buffer
	exit := Check(warnChannelFixture(t, &out, `{"env_sources": ["ghost-a.env", "ghost-b.env"]}`))
	got := stripANSI(out.String())
	if !strings.Contains(got, "[WARN] env_sources file not found") {
		t.Fatalf("fixture did not produce the graded warning it exists to produce:\n%s", got)
	}
	if strings.Contains(got, "[FAIL]") {
		t.Fatalf("fixture produced a FAIL, so this says nothing about warnings:\n%s", got)
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0. A counted warning must not become a refusal — that is "+
			"OQ-RM1 in reference-mismatch-diagnostics.md and it is UNRULED:\n%s", exit, got)
	}
}

// TestSkippedPackEntryWarningReachesTheSummary pins the packs.go call site —
// `config.LoadPacks(r.configWarn)`.
//
// The finding matters on its own terms: a `packs` entry the loader could not lower is a
// pack the user asked for and did not get, and until step 1 it was announced on the
// channel the summary could not see. The assertion is on r.warned here (sectionPacks is
// called directly, so there is no summary line in this buffer) PLUS the graded badge in
// the text; the end-to-end summary arithmetic for this same channel is covered by
// TestEnvSourcesWarningsReachTheSummary above, and TestEverySectionIsWired covers the
// section's own call from Check().
//
// MUTATION VERIFIED: changing the argument to `func(string) {}` leaves warned=1 (the
// no-packs notice alone) and this test fails on both the count and the missing badge.
//
// The two-warning expectation is deliberate and load-bearing in the other direction: the
// section must NOT suppress the empty-packs notice just because an entry was skipped —
// "these entries were skipped, and what is left is nothing" is the honest pair.
func TestSkippedPackEntryWarningReachesTheSummary(t *testing.T) {
	// 123 is not a source string and not an object, so checkPacks rejects the entry and
	// LoadPacks reports it through warn. It is not a FAIL: LoadPacks skips the entry and
	// returns the rest.
	packsFixture(t, `{"packs": [123]}`)

	var buf bytes.Buffer
	r := newReporter(&buf, false)
	(&Options{}).sectionPacks(r)
	got := stripANSI(buf.String())

	if r.failed != 0 {
		t.Fatalf("a skipped entry must not FAIL the section (the loader drops it and "+
			"carries on):\n%s", got)
	}
	if r.warned != 2 {
		t.Errorf("warned = %d, want 2 (the skipped entry AND the no-agent notice). A "+
			"skipped `packs` entry is a pack the user asked for and did not get; it must "+
			"reach the count, not scroll past it:\n%s", r.warned, got)
	}
	if !strings.Contains(got, "[WARN] config.packs[0]") {
		t.Errorf("the skipped entry is not a graded [WARN] row naming the config path:\n%s", got)
	}
	if strings.Contains(got, "Warning: config.packs") {
		t.Errorf("the skipped entry is still on the retired ungraded channel:\n%s", got)
	}
}

// TestNoUngradedWarningChannelSurvives is the CLASS assertion, and the one that stops
// this from being re-introduced somewhere new.
//
// The defect was never "these three call sites"; it was that a SECOND channel existed at
// all, so any future section could reach for it and pick up an uncounted finding for
// free. reporter.go no longer has a printer that emits "Warning: " without grading, and
// this walks the package's non-test source to keep it that way — a hand-written list
// would silently miss the fourth call site somebody adds tomorrow.
//
// It is intentionally about the REPORTER, not about the word, and it walks the AST
// rather than the file bytes for exactly that reason: prose is allowed to name the
// retired method (the comments explaining this change do), and a graded row's message
// text is allowed to contain the word "warning". Only real code counts.
func TestNoUngradedWarningChannelSurvives(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Parsed WITHOUT ParseComments, so doc comments naming the retired channel are
		// not in the tree at all.
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			// The retired method itself, under any receiver — a declaration, a call, or a
			// value passed as a Warn sink.
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "warningLine" {
				t.Errorf("%s:%d references .warningLine — the ungraded second channel of "+
					"reference-mismatch-diagnostics.md §3. Route the finding through "+
					"r.configWarn (or r.warn) so the summary counts it.",
					name, fset.Position(sel.Pos()).Line)
			}
			if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "warningLine" {
				t.Errorf("%s:%d re-declares warningLine — the ungraded second channel is "+
					"back. Grade the finding instead.", name, fset.Position(fn.Pos()).Line)
			}
			// A hand-rolled respelling of it: r.line("Warning: …") — or r.line(r.style(
			// "Warning: …", …)) — which reaches the terminal without touching a count.
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "line" || len(call.Args) == 0 {
				return true
			}
			if startsWithWarningLiteral(call.Args[0]) {
				t.Errorf("%s:%d prints a bare \"Warning:\" line through .line — that is the "+
					"uncounted channel again under a different name. Use r.warn so the "+
					"summary sees it.", name, fset.Position(call.Pos()).Line)
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no source files — the scan is broken, not the code")
	}
	t.Logf("scanned %d non-test source files for an ungraded warning channel", scanned)
}

// startsWithWarningLiteral reports whether expr's leftmost string literal begins with
// "Warning" — reaching through a `+` concatenation and through a wrapping call such as
// r.style(...), which is how the retired printer spelled it.
func startsWithWarningLiteral(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING && strings.HasPrefix(strings.Trim(e.Value, "`\""), "Warning")
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return false
		}
		return startsWithWarningLiteral(e.X)
	case *ast.CallExpr:
		if len(e.Args) == 0 {
			return false
		}
		return startsWithWarningLiteral(e.Args[0])
	}
	return false
}

// TestEveryConfigWarnSinkIsGraded is the other half of the class test: it is not enough
// that no ungraded PRINTER exists — a call site could hand a loader `nil` or
// `func(string) {}` and discard the findings entirely, which is what the summary saw
// before this step and is invisible to the scan above.
//
// So: every config-loader call in this package that takes a Warn sink must pass
// r.configWarn. The three the doc's step 1 names are LoadPacks, LoadCacheRelocations and
// ResolveEnvSources; this walks the AST for the ARGUMENT SHAPE rather than a list of
// function names, so a fourth loader wired with a discarding sink tomorrow is caught
// without an edit here.
//
// MUTATION VERIFIED: changing any of the three call sites to `func(string) {}` or `nil`
// makes this fail and names the file, the line, and the loader.
func TestEveryConfigWarnSinkIsGraded(t *testing.T) {
	// The FOUR loaders whose warnings this package deliberately discards — every one
	// verified against the tree, not guessed: config.LoadConfig (check.go:250,
	// helpers.go:91), config.UserScopeConfig (check.go:388),
	// config.LoadJSONCWithIncludes (check.go:400), config.LoadWorkspaceConfig
	// (check.go:446).
	//
	// Each is exempt for the same reason: it is a RE-READ of a file whose problems the
	// Config Files and Merged Configuration sections have already reported, by name, as
	// [FAIL] rows. Counting them again through a second sink would double-report one
	// problem — the mirror image of the defect this file is about, and just as misleading
	// in the summary.
	reReported := map[string]bool{
		"LoadConfig":            true,
		"UserScopeConfig":       true,
		"LoadJSONCWithIncludes": true,
		"LoadWorkspaceConfig":   true,
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	sinks := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "configWarn" {
				sinks++
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "config" || reReported[sel.Sel.Name] {
				return true
			}
			if isDiscardingSink(call.Args[len(call.Args)-1]) {
				t.Errorf("%s:%d discards config.%s's warnings. Hand it r.configWarn — a "+
					"finding nothing counts is the two-channel defect of "+
					"reference-mismatch-diagnostics.md §3, whether it is printed uncounted "+
					"or not printed at all. (If this loader's findings are genuinely "+
					"re-reported elsewhere, add it to reReported and say where.)",
					name, fset.Position(call.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}
	// Anti-vacuity: if the wiring were deleted wholesale the scan above would pass by
	// finding nothing to complain about. There must be sinks to protect.
	if sinks < 3 {
		t.Errorf("found %d r.configWarn call sites, want at least 3 (LoadPacks in packs.go, "+
			"LoadCacheRelocations in check.go, ResolveEnvSources in entrypoint.go) — the "+
			"warning channel has been unwired, not simplified", sinks)
	}
}

// isDiscardingSink reports whether expr is a Warn sink that throws its findings away:
// a bare `nil`, or an empty `func(string) {}` / `func(msg string) {}` literal.
func isDiscardingSink(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "nil"
	case *ast.FuncLit:
		return e.Body != nil && len(e.Body.List) == 0
	}
	return false
}
