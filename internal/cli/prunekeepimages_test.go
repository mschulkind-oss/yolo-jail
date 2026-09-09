package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// prunekeepimages_test.go pins the REMOVAL of `--keep-images` (OQ-LS3,
// docs/design/the-load-sentinel-is-not-a-liveness-oracle.md §6.2).
//
// A removed flag has to REFUSE, and the reason is specific to this one.
// pruneOptions' switch has no default case, so a dropped flag is silently
// ignored — and for a retention count that is the worst available reading:
// someone passing `--keep-images 8` to hold more images back would get a pass
// that removes every image no workspace points at, with no indication that the
// number they typed did nothing. The retired config keys (`agents`, `docker`,
// `journal`, `host_processes` in internal/config/validate.go) set the precedent:
// name the replacement, fail the command.

// TestRemovedKeepImagesFlagIsRefused: both spellings, a non-zero exit, and a
// message that names what replaced it rather than only what is gone.
func TestRemovedKeepImagesFlagIsRefused(t *testing.T) {
	for _, args := range [][]string{
		{"prune", "--keep-images", "8"},
		{"prune", "--keep-images=8"},
		{"prune", "--apply", "--keep-images", "2"},
	} {
		var out bytes.Buffer
		rc := refuseRemovedPruneFlags(args, &out)
		if rc == 0 {
			t.Errorf("%v was accepted — pruneOptions has no default case, so the flag would "+
				"be silently ignored and the user's number would do nothing", args)
			continue
		}
		msg := strings.ToLower(out.String())
		for _, want := range []string{"--keep-images", "removed", "current"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%v: refusal %q does not mention %q — a removal that does not name "+
					"its replacement just moves the confusion", args, msg, want)
			}
		}
	}
}

// TestPruneStillAcceptsItsOtherValueFlags: the refusal must be about this one
// flag, not about anything that looks like it. `--image-cache-keep` is a
// DIFFERENT dial with a different subject (the pre-C3 tar backlog, per-runtime
// default via prune.ResolveImageCacheKeep) and OQ-LS3 does not touch it.
func TestPruneStillAcceptsItsOtherValueFlags(t *testing.T) {
	var out bytes.Buffer
	args := []string{"prune", "--image-cache-keep", "3", "--cache-age", "7"}
	if rc := refuseRemovedPruneFlags(args, &out); rc != 0 {
		t.Fatalf("refused %v: %q", args, out.String())
	}
	opts := pruneOptions(args)
	if opts.ImageCacheKeep != 3 || opts.CacheAge != 7 {
		t.Errorf("pruneOptions(%v) = ImageCacheKeep %d, CacheAge %d; want 3 and 7",
			args, opts.ImageCacheKeep, opts.CacheAge)
	}
}

// TestPruneHelpExplainsTheRemoval: the flag is in the help text — deliberately,
// as a removal rather than as a flag — because the people who need the message
// are the ones with it in a script or in muscle memory, and asking for help is
// the other way they find out. TestUsageListsEveryParsedFlag requires the
// literal to appear somewhere in this text anyway; this asserts it appears as
// what it is.
func TestPruneHelpExplainsTheRemoval(t *testing.T) {
	if !strings.Contains(pruneUsage, "--keep-images") {
		t.Fatal("`yolo prune --help` no longer mentions --keep-images at all — a flag that " +
			"refuses has to be discoverable, or the refusal is the first anyone hears of it")
	}
	line := ""
	for _, l := range strings.Split(pruneUsage, "\n") {
		if strings.Contains(l, "--keep-images") {
			line = l
			break
		}
	}
	if !strings.Contains(line, "REMOVED") {
		t.Errorf("the --keep-images help line is %q — it must say the flag is gone, not "+
			"describe a count yolo no longer has", line)
	}
}

// TestRunPruneRefusesBeforeItRuns is the CALL-SITE pin, without which the three
// tests above are the shape AGENTS.md names: they drive
// refuseRemovedPruneFlags directly and stay green if runPrune stops calling it,
// at which point `--keep-images 8` is silently ignored again and the refusal
// exists only in a test.
//
// The order is load-bearing in both directions. It must come AFTER answerHelp,
// because asking a tool what it does may never fail — that is how someone with
// the old flag in a script finds the explanation. And it must come BEFORE
// prune.Run, or the pass has already reclaimed by the time the user is told
// their number did nothing.
func TestRunPruneRefusesBeforeItRuns(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, 0)
	if err != nil {
		t.Fatalf("parse commands.go: %v", err)
	}
	var helpPos, refusePos, runPos token.Pos
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "runPrune" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			switch name {
			case "answerHelp":
				if helpPos == token.NoPos {
					helpPos = call.Pos()
				}
			case "refuseRemovedPruneFlags":
				if refusePos == token.NoPos {
					refusePos = call.Pos()
				}
			case "Run":
				if runPos == token.NoPos {
					runPos = call.Pos()
				}
			}
			return true
		})
	}
	if refusePos == token.NoPos {
		t.Fatal("runPrune no longer refuses removed flags — pruneOptions has no default case, " +
			"so `yolo prune --keep-images 8` is accepted, ignored, and reclaims on the new " +
			"rule with no indication that the number did nothing")
	}
	if helpPos == token.NoPos || runPos == token.NoPos {
		t.Fatal("runPrune no longer answers help and/or calls prune.Run — this pin cannot " +
			"check the ordering the refusal has to sit in")
	}
	if refusePos < helpPos {
		t.Error("the refusal runs BEFORE the help — `yolo prune --keep-images --help` must " +
			"reach the text that explains the removal, because interrogating a tool never fails")
	}
	if runPos < refusePos {
		t.Error("prune.Run runs BEFORE the refusal — the sweep would reclaim on the new rule " +
			"and only then tell the user their retention count was ignored")
	}
}
