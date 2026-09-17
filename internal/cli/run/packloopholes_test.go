package run

// packloopholes_test.go covers the JAIL-EXECUTION disclosure class: a wrapped plugin's
// hooks and servers, which reached no launch banner until trust-paths.md OQ-TP10 ruled (a)
// on 2026-09-14. The host read/exec classes are covered by packhostdisclosure_test.go, which
// this file deliberately does not duplicate.
//
// Every fixture loads a real pack through packload and takes its claims from
// packload.FootprintOf, never from a hand-built Claim: the target prefix that identifies a
// plugin claim ("plugin:") is a contract between two files, and a test that built the claim
// itself would keep passing after a rename on the producer's side — which is the shape that
// left this hole open in the first place.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE LINE PRINTS, AT THE REAL CALL SITE.
//
// Driven through startLoopholesDisclosed rather than through notePackJailCode, because the
// callee is not the thing in doubt: OQ-TP10's whole finding is that the classification was
// right about plugins being jail-internal and WRONG that jail-internal meant silence, so a
// test that called the printer directly would pin a function the pipeline need never reach.
// Delete the call in startLoopholesDisclosed and this fails.
func TestWrappedPluginCodeIsDisclosedAtTheSpawnBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	p := writePluginPack(t, "acme", map[string]string{
		"acme-tools": `{"name":"acme-tools","skills":["./"],` +
			`"hooks":{"PreToolUse":[]},"mcpServers":{"acme":{}}}`,
	})

	cname := "yolo-jailcode-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false }

	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p})

	got := errBuf.String()
	if !strings.Contains(got, "runs code in the jail") {
		t.Fatalf("a pack whose plugin declares hooks and an MCP server produced no "+
			"jail-execution disclosure. OQ-TP9 deleted the approval gate and KEPT this "+
			"banner as the compensating disclosure, so code that arrives with nothing said "+
			"is strictly worse than the gate that was removed. The launch said:\n%s", got)
	}
	if !strings.Contains(got, "acme:") {
		t.Errorf("the disclosure does not name the pack the code came from:\n%s", got)
	}
	// The COUNTS, which are what makes one line enough (report-tiers.md P6).
	for _, want := range []string{"hooks", "mcpServers"} {
		if !strings.Contains(got, want) {
			t.Errorf("the disclosure never names the %q component, so a reader cannot tell "+
				"what kind of code this is:\n%s", want, got)
		}
	}
}

// ONE COUNTED LINE PER PACK — the rendering OQ-TP10 took from report-tiers.md rather than
// inventing, and the reason (a) is compatible with the startup-density work instead of in
// tension with it. Two plugins, four code-bearing components, ONE line.
//
// The second half is the one that decays first: the line must NOT itemize. A per-plugin line
// today becomes a per-hook line the next time someone "improves" it, and then the disclosure
// is the wallpaper option (c) was rejected for being.
func TestWrappedPluginCodeIsOneCountedLinePerPack(t *testing.T) {
	p := writePluginPack(t, "acme", map[string]string{
		"acme-tools":  `{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]}}`,
		"acme-search": `{"name":"acme-search","skills":["./"],"hooks":{"Stop":[]},"mcpServers":{"s":{}}}`,
	})

	lines := packJailCodeLines([]*packload.Pack{p})
	if len(lines) != 1 {
		t.Fatalf("two plugins in one pack produced %d lines, want 1 — P1 states a property "+
			"of the pack set once per set, and P5's invariant is NAMED, not itemized:\n%s",
			len(lines), renderLines(lines))
	}
	got := lines[0].claim
	if !strings.Contains(got, "2 wrapped plugins") {
		t.Errorf("the line does not count the plugins: %q", got)
	}
	if !strings.Contains(got, "hooks (2)") || !strings.Contains(got, "mcpServers (1)") {
		t.Errorf("the line does not count by component kind, which is the count P6 says a "+
			"reader cares about: %q", got)
	}
	for _, itemized := range []string{"acme-tools", "acme-search", "PreToolUse"} {
		if strings.Contains(got, itemized) {
			t.Errorf("the launch line itemizes %q. The itemization belongs in the report the "+
				"header points at; a line that grows with the pack is how a disclosure "+
				"surface becomes wallpaper: %q", itemized, got)
		}
	}
}

// A SKILLS-ONLY PLUGIN IS SILENT, and this is the control that keeps the class meaningful.
//
// Option (c) — classifying `skills` itself — was rejected because it "would announce every
// skill file and bury the hooks in the noise that made disclosureSkip right here". If prose
// reaches this line, the rejected option has been built under a different name.
func TestSkillsOnlyPluginIsNotDisclosedAsJailCode(t *testing.T) {
	p := writePluginPack(t, "acme", map[string]string{
		// commands and output styles are DECLARED and run nothing: the fixture has to be
		// something the producer emits a claim for, or the assertion passes vacuously.
		"acme-prose": `{"name":"acme-prose","skills":["./"],"commands":"cmds",` +
			`"outputStyles":"styles"}`,
	})
	if lines := packJailCodeLines([]*packload.Pack{p}); len(lines) != 0 {
		t.Errorf("a plugin that runs nothing was announced as jail code:\n%s\n"+
			"That is option (c) by another route — every prose tree on the banner, with the "+
			"hooks buried in it", renderLines(lines))
	}
	// And the claim really is there to be misclassified, so the check above means something.
	var found bool
	for _, c := range packload.FootprintOf(p).Claims {
		if strings.HasPrefix(c.Target, pluginClaimTargetPrefix) {
			found = true
		}
	}
	if !found {
		t.Fatal("the fixture produced no plugin claim at all, so the assertion above cannot " +
			"fail — repoint it at whatever the producer emits today")
	}
}

// THE JAIL CLASS IS NOT A HOST CLASS, in either direction.
//
// Both failures are real and they are opposite. Routed to a host block, the line would claim
// a crossing that does not happen — the fail-closed default OQ-TP10 warned against, which
// "would announce 'runs code on the host' about code that does not". Left in disclosureSkip,
// it prints nowhere, which is the hole the ruling closed.
func TestWrappedPluginCodeIsDisclosedOnNeitherHostAxis(t *testing.T) {
	p := writePluginPack(t, "acme", map[string]string{
		"acme-tools": `{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]}}`,
	})
	packs := []*packload.Pack{p}
	if lines := packHostExecClaims(packs); len(lines) != 0 {
		t.Errorf("a plugin hook is in the pre-spawn HOST block, whose whole value is that "+
			"every line in it is about to run on the user's machine:\n%s", renderLines(lines))
	}
	if lines := disclosedClaims(packs, disclosureRead); len(lines) != 0 {
		t.Errorf("a plugin hook is disclosed as a host READ — nothing of the user's is "+
			"read:\n%s", renderLines(lines))
	}
	if lines := packJailCodeLines(packs); len(lines) != 1 {
		t.Fatalf("...and it is not in the jail block either, so it is disclosed nowhere: %d "+
			"lines", len(lines))
	}
}

// P4 AS A GATE: the disclosure cannot be suppressed, and nothing may be added that could
// suppress it.
//
// The runtime half alone would not catch the regression it is written for — a future `if
// o.Quiet` around the call site fails it, but an author adding that flag would notice a
// failing test and gate the test too. So the structural half asks the AST instead: the call
// must be an unconditional statement of the spawn boundary, and neither the printer nor the
// renderer may consult a dial. `TestTheLaunchHasNoQuietFlag` is the same principle applied to
// runFlags; this is it applied to the one line runFlags does not know about.
func TestWrappedPluginCodeDisclosureCannotBeSuppressed(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "packloopholes.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the file under test: %v", err)
	}

	var boundary *ast.FuncDecl
	gated := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "startLoopholesDisclosed":
			boundary = fn
		case "notePackJailCode", "packJailCodeLines", "jailCodeSummary":
			// A dial read anywhere in the disclosure's own body is a quiet mode with no
			// flag — the shape P4 forbids, reached without touching runFlags at all.
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				for _, dial := range []string{"Verbose", "Quiet", "Timing", "DryRun", "Getenv"} {
					if sel.Sel.Name == dial {
						gated[fn.Name.Name+" reads "+dial] = true
					}
				}
				return true
			})
		}
	}
	if boundary == nil {
		t.Fatal("startLoopholesDisclosed is gone — this guard has lost its subject and is " +
			"now weaker than it reads; repoint it at whatever discloses before the spawn")
	}
	for g := range gated {
		t.Errorf("%s: the jail-execution disclosure consults a dial. Disclosures are never "+
			"suppressible (report-tiers.md P4; AGENTS.md: a launch has no quiet mode) — the "+
			"one-line-per-pack compression IS the density control", g)
	}

	// The call site itself: a STATEMENT of the function body, not nested in anything.
	called := false
	for _, stmt := range boundary.Body.List {
		es, ok := stmt.(*ast.ExprStmt)
		if ok && callsMethod(es.X, "notePackJailCode") {
			called = true
		}
	}
	if !called {
		var nested bool
		ast.Inspect(boundary, func(n ast.Node) bool {
			if callsMethod(n, "notePackJailCode") {
				nested = true
			}
			return true
		})
		if nested {
			t.Fatal("the jail-execution disclosure is called CONDITIONALLY from the spawn " +
				"boundary — a condition is a quiet mode without a flag to name it (P4)")
		}
		t.Fatal("the spawn boundary no longer discloses jail execution at all: a wrapped " +
			"plugin's hooks reach the agent's lifecycle with nothing on the banner, which " +
			"is the gap OQ-TP10 closed")
	}

	// And the runtime half, against the quietest launch this build can express: no colour,
	// no tty, and every suppression-shaped variable set.
	p := writePluginPack(t, "acme", map[string]string{
		"acme-tools": `{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]}}`,
	})
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.Getenv = func(k string) string {
		switch k {
		case "YOLO_NO_BANNER", "YOLO_QUIET", "NO_COLOR":
			return "1"
		}
		return ""
	}
	o.notePackJailCode([]*packload.Pack{p})
	if !strings.Contains(errBuf.String(), "runs code in the jail") {
		t.Errorf("an environment that asks for quiet suppressed the disclosure. "+
			"YOLO_NO_BANNER is narrow on purpose — the version line, nothing else — and a "+
			"hidden disclosure deletes what OQ-TP9 kept when it deleted the approval "+
			"gate:\n%s", errBuf.String())
	}
}

// --- helpers ---

// writePluginPack writes a pack whose `skills` tree wraps one plugin per entry, keyed by
// directory name, and loads it through packload. Manifest bodies are written verbatim so a
// fixture can declare exactly the components it means to.
func writePluginPack(t *testing.T, packName string, plugins map[string]string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	for dir, manifest := range plugins {
		md := filepath.Join(root, "skills", dir, ".claude-plugin")
		if err := os.MkdirAll(md, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(md, "plugin.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, packName)
	if len(probs) > 0 {
		t.Fatalf("the plugin pack fixture does not load: %v", probs)
	}
	return p
}
