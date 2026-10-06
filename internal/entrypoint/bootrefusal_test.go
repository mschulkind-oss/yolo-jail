package entrypoint

// bootrefusal_test.go pins a refused boot's STRUCTURED CAUSE (bootrefusal.go, PPX-D42): a failed
// surface's record names its pack and its file in plain words and the path its error names,
// home-relative, and whether that path was read-only; Main writes the record in its refusal branch
// and removes the last one first; the record reads back with the jail's control characters gone;
// the hold offer is told apart from what went wrong; and the jail's gate says one shared cause once.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A SURFACE THAT FAILS TO RENDER is recorded with what it was doing, whose it is and the path its
// error names. Red with ConfigurePackSurfaces' step about (surfaceStepsAbout) deleted.
func TestAFailedSurfaceIsRecordedWithItsPackAndItsFile(t *testing.T) {
	e, _ := jailAdoptionHome(t, "")
	e.Stderr = &bytes.Buffer{}
	// A FILE WHERE THE SURFACE'S DIRECTORY GOES fails its write as root too.
	if err := os.RemoveAll(filepath.Join(e.Home, ".acme")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(e.Home, ".acme"), "not a directory")
	ConfigurePackSurfaces(e, []*packload.Pack{archiveJailPack(t)})
	if len(e.genRecords) != 1 {
		t.Fatalf("records %+v, want the surface's one", e.genRecords)
	}
	g := e.genRecords[0]
	if g.Step != "configure_acme_settings" || g.Pack != "acme" ||
		g.Doing != "writing acme's settings file (~/.acme/settings.json)" || !strings.HasPrefix(g.Path, "~/.acme") || g.ReadOnly {
		t.Errorf("the record is %+v", g)
	}
	lines := g.Lines()
	if len(lines) != 2 || lines[0] != "writing acme's settings file (~/.acme/settings.json), which pack acme declares, failed:" ||
		!strings.HasPrefix(lines[1], "  ") || strings.Contains(strings.Join(lines, "\n"), "configure_acme_settings") {
		t.Errorf("the plain words are %q", lines)
	}
}

// A READ-ONLY PATH is said as one: the error is the jail's mount, not the pack's content.
func TestAReadOnlyPathIsSaidAsOne(t *testing.T) {
	e := &Env{Home: "/home/agent"}
	err := &fs.PathError{Op: "mkdir", Path: "/home/agent/.pi/agent/extensions/pi-automode", Err: syscall.EROFS}
	g := genFailureOf(e, "configure_pi_automode", genAbout{doing: "writing pi's automode file (~/.pi/agent/extensions/pi-automode/settings.json)",
		pack: "matt"}, err)
	want := []string{"writing pi's automode file (~/.pi/agent/extensions/pi-automode/settings.json), which pack matt declares, failed:",
		"  ~/.pi/agent/extensions/pi-automode is mounted read-only in that jail"}
	if !g.ReadOnly || g.Path != "~/.pi/agent/extensions/pi-automode" || !slices.Equal(g.Lines(), want) {
		t.Errorf("the record is %+v, lines %q; want %q", g, g.Lines(), want)
	}
	// A step with no words for it still names itself, and a failure with no step is its error.
	if got := (GenFailure{Step: "configure_git", Error: "boom"}).Lines(); !slices.Equal(got,
		[]string{"the boot step configure_git failed:", "  boom"}) {
		t.Errorf("an unnamed step reads %q", got)
	}
	if got := (GenFailure{Error: "host services unusable"}).Lines(); !slices.Equal(got, []string{"host services unusable"}) {
		t.Errorf("a stepless failure reads %q", got)
	}
}

// THE RECORD ROUND-TRIPS, beside boot.log, its strings stripped of what a jail could use to drive a
// terminal; clearing it leaves none.
func TestTheRecordReadsBackAsText(t *testing.T) {
	e := &Env{Home: "/home/agent", Workspace: t.TempDir()}
	e.genRecords = []GenFailure{{Step: "configure_x_y", Doing: "writing x's y file\x1b[2J", Pack: "p\x07", Error: "e\nmore"}}
	recordBootRefusal(e)
	r := ReadBootRefusal(e.Workspace)
	if r == nil || len(r.Failures) != 1 {
		t.Fatalf("the record did not read back: %+v", r)
	}
	if g := r.Failures[0]; g.Doing != "writing x's y file[2J" || g.Pack != "p" || g.Error != "emore" {
		t.Errorf("the record's strings carry control characters: %+v", g)
	}
	clearBootRefusal(e)
	if ReadBootRefusal(e.Workspace) != nil {
		t.Error("a cleared record still reads")
	}
}

// MAIN WRITES THE RECORD IN ITS REFUSAL, and removes the last one before its steps: an AST pin,
// as TestMainHoldsInsideTheGeneratorRefusalAndStillReturnsTheError pins the hold, since Main runs a
// whole boot. Red with either call deleted.
func TestMainRecordsItsRefusalAndClearsTheLastOne(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "boot.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	branch := mainRefusalBranch(t, f)
	if !callsFunc(branch.Body, "recordBootRefusal") {
		t.Error("Main's generator-refusal branch does not write the refusal's record (recordBootRefusal): a build " +
			"jail's host act then relays the boot's prose, hint lines and step names, not its cause")
	}
	if !callsFunc(mainDecl(t, f), "clearBootRefusal") {
		t.Error("Main never removes the last refused boot's record, so one on disk can be an older boot's")
	}
}

// THE HOLD OFFER IS TOLD APART: its lines, as a terminal shows them, and nothing else.
func TestTheHoldOfferIsToldApart(t *testing.T) {
	for _, l := range strings.Split(holdOffer, "\n") {
		if strings.TrimSpace(l) != "" && !IsHoldOfferLine("  "+l+"  ") {
			t.Errorf("the hold offer's line %q is not told apart", l)
		}
	}
	for _, l := range []string{"", "yolo-entrypoint: refusing to start the jail: 1 config generator(s) failed:",
		"  - configure_pi_automode: mkdir /x: read-only file system"} {
		if IsHoldOfferLine(l) {
			t.Errorf("%q is taken for the hold offer", l)
		}
	}
}

// THE GATE SAYS ONE SHARED CAUSE ONCE (PPX-D42): one short line per extension, then the reason and
// its cause once, then the next step once; extensions whose causes differ each say their own reason.
// Red with treeGateFor's sharing deleted.
func TestTheGateSaysASharedCauseOnce(t *testing.T) {
	cause := &BuildCause{Lines: []string{"writing pi's automode file (~/.pi/agent/extensions/pi-automode/settings.json), " +
		"which pack matt declares, failed:", "  ~/.pi/agent/extensions/pi-automode is mounted read-only in that jail"},
		YoloBug: true, Packs: []string{"matt"}}
	reason := "its build jail refused to start on the host — the next fresh launch tries again"
	d := map[string]TreeDelivery{}
	for _, k := range []string{"matt/pi-subagents", "matt/pi-archimedes", "matt/pi-background-tasks"} {
		d[k] = TreeDelivery{Into: ".pi/agent/yolo-patched/" + strings.TrimPrefix(k, "matt/"), Reason: reason,
			Cause: &BuildCause{Lines: cause.Lines, YoloBug: true, Packs: cause.Packs}, Owner: "pi", Stop: true}
	}
	gate := treeGateFor(d, "pi")
	lines := strings.Split(gate, "\n")
	if len(lines) != 3+1+2+1+1 {
		t.Fatalf("the gate is %d lines, want one per extension, the reason, its cause, who and the step:\n%s", len(lines), gate)
	}
	for i, k := range []string{"matt/pi-archimedes", "matt/pi-background-tasks", "matt/pi-subagents"} {
		if want := "  ⚠ extension " + k + " (~/.pi/agent/yolo-patched/" + strings.TrimPrefix(k, "matt/") +
			") has no build in this jail"; lines[i] != want {
			t.Errorf("line %d is %q, want %q", i, lines[i], want)
		}
	}
	if strings.Count(gate, "is mounted read-only in that jail") != 1 || strings.Count(gate, "refused to start") != 1 ||
		!strings.Contains(gate, "    Why: their build jail refused to start on the host:\n") || !strings.Contains(gate, IssuesURL) {
		t.Errorf("the shared cause is not said once:\n%s", gate)
	}
	// DIFFERENT CAUSES: each extension's own reason on its line, and no shared why.
	d["matt/pi-archimedes"] = TreeDelivery{Into: ".pi/agent/yolo-patched/pi-archimedes", Reason: "its own reason",
		Owner: "pi", Stop: true}
	gate = treeGateFor(d, "pi")
	if !strings.Contains(gate, "(~/.pi/agent/yolo-patched/pi-archimedes) has no build in this jail: its own reason") ||
		strings.Contains(gate, "Why:") {
		t.Errorf("distinct causes are not said each on its own line:\n%s", gate)
	}
}

// mainDecl is boot.go's Main.
func mainDecl(t *testing.T, f *ast.File) *ast.FuncDecl {
	t.Helper()
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Main" && fn.Body != nil {
			return fn
		}
	}
	t.Fatal("boot.go has no Main")
	return nil
}

// mainRefusalBranch is Main's `if err := genFailuresError(e); err != nil` branch.
func mainRefusalBranch(t *testing.T, f *ast.File) *ast.IfStmt {
	t.Helper()
	var branch *ast.IfStmt
	ast.Inspect(mainDecl(t, f).Body, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok && ifs.Init != nil && callsFunc(ifs.Init, "genFailuresError") {
			branch = ifs
			return false
		}
		return branch == nil
	})
	if branch == nil {
		t.Fatal("Main has no genFailuresError branch")
	}
	return branch
}
