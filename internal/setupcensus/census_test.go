package setupcensus

// census_test.go is the census's drift gate, in render/configkeys_test.go's three directions:
// every live key and kind is classified on every setup, nothing classified is a key or kind
// the schema lacks, and every cell says why. The enumerations are the code's own authorities,
// config.TopLevelConfigKeys() and packdecl.KnownKinds(), never a list kept here, so the gate
// fails the moment the schema grows: that is the call site this test pins.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// gapMessage is what a missing cell tells the next author to do.
const gapMessage = "Every live top-level config key and every pack contribution kind needs a " +
	"census entry in internal/setupcensus (configkeys.go or kinds.go) with a cell for each of " +
	"podman/Linux, podman/macOS, container/macOS and macos-user/macOS: one of Honored, " +
	"HonoredBy (name the mechanism), Warned (name the launch line), Dropped, Refused or " +
	"NotApplicable, and a reason naming the code path. Then give it a row in " +
	"userguide/reference/settings-per-setup.md and name that row in the entry's Guide list."

// missingCells lists "<subject> on <setup>" for every setup the entry leaves unclassified, or
// every setup when there is no entry at all.
func missingCells(subject string, e Entry, ok bool) []string {
	var out []string
	for _, s := range Setups() {
		if !ok || e.Cell(s).Disposition == Unclassified {
			out = append(out, subject+" on "+s.String())
		}
	}
	return out
}

// TestEveryConfigKeyHasACellOnEverySetup is the forcing function: a key added to the schema
// fails the build until the census decides what each setup does with it.
func TestEveryConfigKeyHasACellOnEverySetup(t *testing.T) {
	var gaps []string
	for _, key := range config.TopLevelConfigKeys() {
		e, ok := ConfigKey(key)
		gaps = append(gaps, missingCells("config key `"+key+"`", e, ok)...)
	}
	if len(gaps) > 0 {
		t.Fatalf("the setup census has no answer for:\n  %s\n\n%s", strings.Join(gaps, "\n  "), gapMessage)
	}
}

// TestEveryPackKindHasACellOnEverySetup is the same gate over the manifest decoder's kinds.
func TestEveryPackKindHasACellOnEverySetup(t *testing.T) {
	var gaps []string
	for _, k := range packdecl.KnownKinds() {
		e, ok := Kind(k)
		gaps = append(gaps, missingCells("pack kind `"+string(k)+"`", e, ok)...)
	}
	if len(gaps) > 0 {
		t.Fatalf("the setup census has no answer for:\n  %s\n\n%s", strings.Join(gaps, "\n  "), gapMessage)
	}
}

// TestTheCensusHasNoPhantomEntries is the other direction: a key retired from the schema (or a
// kind retired from the decoder) must not leave an entry nothing reads.
func TestTheCensusHasNoPhantomEntries(t *testing.T) {
	live := map[string]bool{}
	for _, k := range config.TopLevelConfigKeys() {
		live[k] = true
	}
	var phantom []string
	for _, k := range ConfigKeys() {
		if !live[k] {
			phantom = append(phantom, "config key `"+k+"`")
		}
	}
	liveKinds := map[packdecl.Kind]bool{}
	for _, k := range packdecl.KnownKinds() {
		liveKinds[k] = true
	}
	for _, k := range Kinds() {
		if !liveKinds[k] {
			phantom = append(phantom, "pack kind `"+string(k)+"`")
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("the census classifies what the schema does not have: %v — drop the entry, or "+
			"add the key or kind to its authority", phantom)
	}
}

// eachCell visits every cell of every entry and aspect, naming each by its path.
func eachCell(visit func(path string, s Setup, c Cell)) {
	var walk func(path string, e Entry)
	walk = func(path string, e Entry) {
		for _, s := range Setups() {
			visit(path, s, e.Cell(s))
		}
		for name, a := range e.Aspects {
			walk(path+"."+name, a)
		}
	}
	for _, k := range ConfigKeys() {
		e, _ := ConfigKey(k)
		walk("config key "+k, e)
	}
	for _, k := range Kinds() {
		e, _ := Kind(k)
		walk("pack kind "+string(k), e)
	}
}

// TestEveryCellHasADispositionAndAReason: a disposition with no reason cannot be re-decided,
// which is inherit.go's argument and holds for Honored cells too. Aspects are held to it as
// well, since a reader reaches them by the same lookups.
func TestEveryCellHasADispositionAndAReason(t *testing.T) {
	eachCell(func(path string, s Setup, c Cell) {
		if c.Disposition == Unclassified {
			t.Errorf("%s on %s has no disposition", path, s)
		}
		if strings.TrimSpace(c.Reason) == "" {
			t.Errorf("%s on %s has no reason", path, s)
		}
	})
}

// TestAnAspectDiffersFromItsParent: an aspect exists to say where a sub-mechanism parts from
// its key, so one that agrees with its parent on every setup is a second copy of the parent's
// answer, free to drift from it.
func TestAnAspectDiffersFromItsParent(t *testing.T) {
	check := func(path string, parent Entry) {
		for name, a := range parent.Aspects {
			differs := false
			for _, s := range Setups() {
				if a.Cell(s).Disposition != parent.Cell(s).Disposition {
					differs = true
				}
			}
			if !differs {
				t.Errorf("%s.%s has its parent's disposition on every setup: drop the aspect "+
					"and let the parent's Guide list claim its rows", path, name)
			}
			if len(a.Aspects) > 0 {
				t.Errorf("%s.%s has aspects of its own; the census is two levels deep", path, name)
			}
		}
	}
	for _, k := range ConfigKeys() {
		e, _ := ConfigKey(k)
		check("config key "+k, e)
	}
	for _, k := range Kinds() {
		e, _ := Kind(k)
		check("pack kind "+string(k), e)
	}
}

// A HonoredBy cell must name its mechanism — §3's warning is that the un-named version of the
// sentence is what hid #39 — and a Warned cell must say where the launch says so. The cheapest
// check that catches an empty gesture at either is that the reason names some code: a function,
// a file, or a measurement on record.
func TestHonoredByAndWarnedCellsNameWhere(t *testing.T) {
	eachCell(func(path string, s Setup, c Cell) {
		if c.Disposition != HonoredBy && c.Disposition != Warned {
			return
		}
		if !namesCode(c.Reason) {
			t.Errorf("%s on %s is %s and its reason names no code path, file or measurement: %q",
				path, s, c.Disposition, c.Reason)
		}
	})
}

// namesCode reports whether a reason cites something a reader can go and look at: a Go
// identifier spelled with a call or a dot, a file name, or the word "measured".
func namesCode(reason string) bool {
	for _, tell := range []string{".go", "()", "measured", "Measured"} {
		if strings.Contains(reason, tell) {
			return true
		}
	}
	for _, w := range strings.Fields(reason) {
		w = strings.Trim(w, "(),;:`'\"")
		if i := strings.Index(w, "."); i > 0 && i < len(w)-1 && isIdentStart(w[0]) && isIdentStart(w[i+1]) {
			return true
		}
		if hasInnerUpper(w) {
			return true
		}
	}
	return false
}

func isIdentStart(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// hasInnerUpper spots a camelCase Go identifier such as noteMacosUserPortKeys.
func hasInnerUpper(w string) bool {
	if len(w) < 4 {
		return false
	}
	for i := 1; i < len(w); i++ {
		if w[i] >= 'A' && w[i] <= 'Z' && w[i-1] >= 'a' && w[i-1] <= 'z' {
			return true
		}
	}
	return false
}

// The vocabulary is §3's six, in §3's order, and each spells the word a `// parity:` marker
// carries — internal/cli/run's annotation census reads this list.
func TestTheDispositionsAreSection3s(t *testing.T) {
	var got []string
	for _, d := range Dispositions() {
		got = append(got, d.String())
	}
	want := "Honored HonoredBy Warned Dropped Refused NotApplicable"
	if strings.Join(got, " ") != want {
		t.Errorf("Dispositions() = %v, want %s", got, want)
	}
	if Unclassified.Works() || Warned.Works() || !Honored.Works() || !HonoredBy.Works() {
		t.Error("Works() must be true for Honored and HonoredBy only")
	}
}

// Find and Paths are the census's addresses for readers outside the package; every path Paths
// lists must resolve, and a kind path must not resolve to the config key of the same name.
func TestEveryPathResolves(t *testing.T) {
	for _, p := range Paths() {
		if _, ok := Find(p); !ok {
			t.Errorf("Paths() lists %q and Find cannot resolve it", p)
		}
	}
	key, _ := Find("profile")
	kind, _ := Find(KindPathPrefix + "profile")
	if key.PodmanLinux.Reason == kind.PodmanLinux.Reason {
		t.Error("Find(\"profile\") and Find(\"kind:profile\") gave the same entry")
	}
	if _, ok := Find("resources.no_such_aspect"); ok {
		t.Error("Find resolved an aspect the census does not have")
	}
}

// noticePackages maps a Notice's By prefix to the package whose printer reads it. A printer
// elsewhere is a reader no package's tests drive, so it is refused here until one does.
var noticePackages = map[string]string{
	"run":        "internal/cli/run",
	"macosuser":  "internal/macosuser",
	"entrypoint": "internal/entrypoint",
}

// TestEveryMacosUserWarnedKeyCarriesItsLine is the ruling's "the macos-user notice block reads
// it" made total: every config key, and every aspect of one, that the census marks Warned on
// macos-user holds the line its launch prints, so the printer reads its words from the table
// and a Warned cell cannot stand without the line that makes it true. An aspect may lean on its
// parent's line where the parent is Warned there with one, because that line names the aspect
// among its entries (`resources.pids_limit` in the resources line). A pack KIND's line names
// the pack's own item — a fork's program, an extension — which is not the census's to hold, so
// a kind's Warned cell is routed to its printer by internal/cli/run's tests instead.
func TestEveryMacosUserWarnedKeyCarriesItsLine(t *testing.T) {
	for _, k := range ConfigKeys() {
		e, _ := ConfigKey(k)
		check := func(path string, c Cell, parent *Cell) {
			if c.Disposition != Warned || c.Notice.Says != "" {
				return
			}
			if parent != nil && parent.Disposition == Warned && parent.Notice.Says != "" {
				return
			}
			t.Errorf("%s is Warned on macos-user (%s) and holds no Notice: give the cell the line "+
				"its printer prints (warnedSaying), and have the printer read it with "+
				"setupcensus.Warning", path, c.Reason)
		}
		check(k, e.MacosUser, nil)
		for name, a := range e.Aspects {
			check(k+"."+name, a.MacosUser, &e.MacosUser)
		}
	}
}

// TestEveryNoticeIsReadByItsPrinter: a notice sits on a Warned cell, says something, renders
// cleanly through the launch printers' markup, and the printer its By names READS it — its body
// calls setupcensus.Warning with this setup and this path — so the line a launch prints is the
// table's and not a copy that agrees with it today. Each printing package's own tests then drive
// the printer through its call site (internal/cli/run's setupcensusnotices_test.go,
// internal/macosuser's setupcensus_test.go, internal/entrypoint's darwin tests).
func TestEveryNoticeIsReadByItsPrinter(t *testing.T) {
	root := repoRoot(t)
	reads := map[string]map[string]bool{} // package dir -> "<By ident> <Setup> <path>" read
	readsIn := func(pkg string) map[string]bool {
		if r, ok := reads[pkg]; ok {
			return r
		}
		r, err := censusReads(filepath.Join(root, pkg))
		if err != nil {
			t.Fatalf("parse %s: %v", pkg, err)
		}
		reads[pkg] = r
		return r
	}
	seen := 0
	eachCell(func(path string, s Setup, c Cell) {
		n := c.Notice
		if n == (Notice{}) {
			return
		}
		seen++
		where := path + " on " + s.String()
		if c.Disposition != Warned {
			t.Errorf("%s holds a Notice and is %s: only a Warned cell has a launch line", where, c.Disposition)
		}
		if n.Says == "" {
			t.Errorf("%s holds a Notice with no headline", where)
		}
		if strings.ContainsAny(n.Says+n.Then, "[]") {
			t.Errorf("%s's notice holds a bracket, which the launch printers read as markup: %q", where, n)
		}
		pkgName, ident, ok := strings.Cut(n.By, ".")
		pkg := noticePackages[pkgName]
		if !ok || pkg == "" {
			t.Errorf("%s's notice names its printer %q: name it as one of %v, then the identifier", where,
				n.By, noticePackages)
			return
		}
		censusPath := strings.TrimPrefix(path, "config key ")
		if rest, isKind := strings.CutPrefix(path, "pack kind "); isKind {
			censusPath = KindPathPrefix + rest
		}
		if want := ident + " " + setupConst(s) + " " + censusPath; !readsIn(pkg)[want] {
			t.Errorf("%s's notice names printer %s, and %s does not call setupcensus.Warning(setupcensus.%s, %q) "+
				"inside it: the printer must read its line from the census, not keep its own copy",
				where, n.By, pkg, setupConst(s), censusPath)
		}
	})
	if seen == 0 {
		t.Fatal("no census cell holds a Notice: the launch reads nothing from the table")
	}
}

// setupConst is the setup's identifier in this package, as a reader spells it.
func setupConst(s Setup) string {
	switch s {
	case PodmanLinux:
		return "PodmanLinux"
	case PodmanMac:
		return "PodmanMac"
	case AppleContainer:
		return "AppleContainer"
	case MacosUser:
		return "MacosUser"
	}
	return ""
}

// censusReads parses one package's non-test sources and lists every setupcensus.Warning call it
// makes, as "<enclosing printer> <Setup> <path>". The enclosing printer is the function the call
// sits in, or, for a call inside a composite literal carrying `name: "<step>"` (a boot step),
// that step's name, so the darwin boot's mcp_presets_declined step is a printer by its own name.
func censusReads(dir string) (map[string]bool, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			var walk func(n ast.Node, owner string)
			walk = func(n ast.Node, owner string) {
				ast.Inspect(n, func(m ast.Node) bool {
					if m == n {
						return true
					}
					if cl, ok := m.(*ast.CompositeLit); ok {
						if step := stepName(cl); step != "" {
							walk(cl, step)
							return false
						}
					}
					if call, ok := m.(*ast.CallExpr); ok {
						if setup, path, ok := warningCall(call); ok {
							out[owner+" "+setup+" "+path] = true
						}
					}
					return true
				})
			}
			walk(fd.Body, fd.Name.Name)
		}
	}
	return out, nil
}

// stepName is a composite literal's `name: "<step>"` field, or "".
func stepName(cl *ast.CompositeLit) string {
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "name" {
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				return strings.Trim(lit.Value, `"`)
			}
		}
	}
	return ""
}

// warningCall reads `setupcensus.Warning(setupcensus.<Setup>, "<path>")`.
func warningCall(call *ast.CallExpr) (setup, path string, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "Warning" || len(call.Args) != 2 {
		return "", "", false
	}
	if pkg, isIdent := sel.X.(*ast.Ident); !isIdent || pkg.Name != "setupcensus" {
		return "", "", false
	}
	s, isSel := call.Args[0].(*ast.SelectorExpr)
	lit, isLit := call.Args[1].(*ast.BasicLit)
	if !isSel || !isLit || lit.Kind != token.STRING {
		return "", "", false
	}
	return s.Sel.Name, strings.Trim(lit.Value, `"`), true
}

func TestANoticeRendersAsTheLaunchPrintsIt(t *testing.T) {
	n := Notice{Says: "`kvm` is not read on macos-user", Then: "it asks for /dev/kvm."}
	if got, want := n.Line(""), "[yellow]Warning: `kvm` is not read on macos-user[/yellow] — it asks for /dev/kvm."; got != want {
		t.Errorf("Line(\"\") = %q, want %q", got, want)
	}
	if got, want := n.Plain("a, b"), "Warning: `kvm` is not read on macos-user — a, b. it asks for /dev/kvm."; got != want {
		t.Errorf("Plain(\"a, b\") = %q, want %q", got, want)
	}
	if got, want := (Notice{Says: "x"}).Line(""), "[yellow]Warning: x[/yellow]"; got != want {
		t.Errorf("a bare headline renders %q, want %q", got, want)
	}
	// A path the census holds no notice for still yields a true line.
	if got := Warning(MacosUser, "no_such_key").Says; !strings.Contains(got, "no_such_key") {
		t.Errorf("Warning for an unknown path = %q, want it to name the path", got)
	}
}
