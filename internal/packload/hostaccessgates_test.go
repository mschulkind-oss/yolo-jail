package packload_test

// hostaccessgates_test.go pins an ABSENCE: there is no fetched-pack host-access gate
// anywhere in yolo's production code, and reintroducing one is a design decision that has
// to be made deliberately rather than by a commit.
//
// # What this file used to be, and why the inversion is the point
//
// It asserted that the two host-access GATES — `internal/cli/pack.go`'s
// resolveHostApproval (which prompted and recorded the lockfile) and
// `internal/cli/run/packs.go`'s packMayAccessHost (which checked the lockfile at launch) —
// both built their claim set through one merged helper, so they could not disagree about
// what a pack asked of the host. That invariant was real and the drift it caught was real.
// It is also moot: OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) deleted BOTH gates as
// theatre, on gate-placement-principle.md's Test 1 — selecting a pack means writing `packs`
// in ~/.config/yolo-jail/config.jsonc as the host user, which is strictly more authority
// than the gate withheld, so it refused an actor who had already passed a stronger one.
//
// The old file's own analysis left a trap for whoever comes next, and it is the reason this
// one exists rather than nothing: it pinned that exactly TWO gates existed, naming them, so
// a THIRD gate copied into `yolo check` would have satisfied the scan vacuously. A scan that
// enumerates the gates is only as good as the enumeration. A scan for ZERO gates has no such
// hole — every reintroduction is a new name, and every new name is a hit.
//
// # Why a source scan and not a behavioural test
//
// The behavioural half lives beside the launch path
// (internal/cli/run/packnohostgate_test.go): a genuinely fetched pack, never approved, whose
// host claims are all honored by `stagePacks`. That is what fails if the launch is re-gated.
//
// It cannot cover the places that have no launch — `yolo check`, `yolo pack install`,
// `yolo pack footprint`, the entrypoint — and a gate reintroduced at any of those is exactly
// the shape the retired file's own "third gate" warning was about. So this half asks a
// question no behavioural test can: does the identifier exist in production code AT ALL.
//
// # Comments are deliberately exempt
//
// Every deletion this ruling made is recorded in prose that NAMES what it deleted — the
// lockfile's `ApprovedHostAccess`, `config.PackEntry.MayGrantHostFiles`,
// `packload.Pack.MayAccessHost`. That prose is the whole reason the next reader does not
// re-derive the gate, so a text grep would forbid the documentation of the ruling it is
// enforcing. The scan therefore runs over the AST and sees IDENTIFIERS only.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retiredGateIdents are the identifiers that MADE UP the fetched-pack host-access gate.
// Every one is deleted; each row says what it was, so a hit reads as a specific
// reintroduction rather than as a banned word.
var retiredGateIdents = map[string]string{
	"MayGrantHostFiles": "config.PackEntry's origin predicate — the input to every gate",
	"MayAccessHost":     "packload.Pack's per-pack gate decision, set by the caller",
	"packMayAccessHost": "internal/cli/run's LAUNCH gate (origin, else the lockfile)",
	"resolveHostApproval": "internal/cli's INSTALL gate — the y/N prompt and its " +
		"non-tty refusal",
	"ApprovedHostAccess": "packsrc.LockEntry's recorded approval set",
	"HostAccessApproved": "packsrc.LockEntry's superset check over that set",
	"HostAccessClaims": "the claim set the prompt showed and the lockfile stored " +
		"(on packdecl.Manifest, packload.Pack and pluginpack.Plugin)",
	"PluginHostAccessClaims":       "a wrapped plugin's contribution to that set",
	"LoopholeHostAccessClaims":     "a shipped loophole's contribution to that set",
	"RefusedBriefingOverlays":      "the briefing-overlay refusal reporter",
	"packRefusals":                 "the fold of every refusal into one launch fatal",
	"refusedLaunchError":           "the fatal itself",
	"NeedsHostAccess":              "packdecl's origin-gate predicate over a manifest",
	"NeedsHostAccessContributions": "the same predicate expressed over contributions",
}

// TestNoFetchedPackHostAccessGateExists fails if any production file names one of the
// retired gate identifiers in CODE.
//
// A hit is not automatically a bug — it is a claim that the ruling was reversed, and that
// belongs in docs/design/trust-paths.md before it belongs in a .go file.
func TestNoFetchedPackHostAccessGateExists(t *testing.T) {
	root := repoRootFor(t)
	var hits []string
	for _, dir := range []string{"internal", "cmd", "packs"} {
		walkProductionGo(t, filepath.Join(root, dir), func(rel string, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if why, retired := retiredGateIdents[id.Name]; retired {
					hits = append(hits, rel+": "+id.Name+" — "+why)
				}
				return true
			})
		})
	}
	if len(hits) == 0 {
		return
	}
	t.Errorf("a fetched-pack host-access gate is back in production code:\n  %s\n\n"+
		"OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) deleted every one of these as "+
		"THEATRE: naming a pack in `packs` means editing ~/.config/yolo-jail/config.jsonc "+
		"as the host user, which already grants strictly more than any of them withheld, so "+
		"a gate here refuses an actor who has already passed a stronger one "+
		"(gate-placement-principle.md Test 1). What replaced them is DISCLOSURE — "+
		"packload.FootprintOf, run.notePackHostAccess, run.startLoopholesDisclosed — and "+
		"disclosure is not subject to that test at all.\n\n"+
		"If a gate genuinely belongs here now, RULE ON IT FIRST and then delete the row "+
		"from retiredGateIdents. Do not delete the row to make this pass: the trap the "+
		"retired version of this file left behind was an enumeration that could be "+
		"satisfied vacuously, and quietly shortening a list is how that happens.",
		strings.Join(hits, "\n  "))
}

// TestTheDisclosureThatReplacedTheGateIsStillWired is the other half of the ruling, and it
// is here because deleting a gate and deleting the thing that replaced it are one edit
// apart. OQ-TP9 keeps the transparency banner explicitly — "disclosure is not consent, and
// it is now the ONLY place a user sees what a pack reaches" — so an empty FootprintOf on
// this side would make the scan above pass over a strictly worse system.
func TestTheDisclosureThatReplacedTheGateIsStillWired(t *testing.T) {
	root := repoRootFor(t)
	found := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		walkProductionGo(t, filepath.Join(root, dir), func(_ string, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					found[id.Name] = true
				}
				return true
			})
		})
	}
	for name, why := range map[string]string{
		"FootprintOf":             "the per-pack claim enumeration every disclosure reads",
		"notePackHostAccess":      "the launch banner: what each pack READS this launch",
		"startLoopholesDisclosed": "the pre-spawn block: what RUNS on your machine",
		"packHostExecClaims":      "the host-execution lines that block prints",
	} {
		if !found[name] {
			t.Errorf("%s is gone — %s. OQ-TP9 deleted the approval gate ON CONDITION that "+
				"the disclosure stays, because it is now the only thing that tells a user "+
				"what a pack reaches. Losing both is not what was ruled", name, why)
		}
	}
}

// walkProductionGo parses every non-test .go file under dir and hands each to fn.
func walkProductionGo(t *testing.T, dir string, fn func(rel string, file *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// parser.SkipObjectResolution and no ParseComments: the scan is over identifiers,
		// and comments recording the deletion must not read as the deletion being undone.
		parsed, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parsing %s: %v", path, perr)
		}
		fn(path, parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
}

// repoRootFor walks up to the dir holding go.mod. (A copy of the package-internal
// findRepoRoot: this file is in packload_test, the external test package.)
func repoRootFor(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
