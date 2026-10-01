package cli

// hintcommands_test.go enforces rule 4 of docs/reference/happy-path-principle.md ("hints are
// tested so they can't go stale") for messages: every `yolo …` command a message names in
// backticks is one this CLI runs. A hint naming a deleted command or flag is a dead end that looks
// like a next step (the doc coins both terms), and the tests that read a hint used to pin only its
// string, so `yolo host codex` and `yolo host check-deps` shipped though neither is a command.
//
// THE COMMAND TREE IS READ FROM THE DISPATCHERS' OWN SOURCE, not from help text, which would let a
// hint and a usage line agree with each other about a verb that does not exist:
//
//   - The first word routes through routeArgv, the front door Main dispatches with, so a flag
//     before it, `--`, `--at host` and the implicit `run` resolve as they do for a user.
//   - A command that takes verbs (`yolo pack install`) has its dispatcher named in
//     verbDispatchers, and its verbs are the string cases of that dispatcher's switch (and the
//     keys of config's verb map). TestEveryVerbDispatcherIsListed fails for a command whose
//     refusal says it has verbs and is not listed.
//   - A flag (`--assert`) must be one the command parses: a long-flag literal, a flag.FlagSet
//     definition or an entry of a flag table, in its handler or in a function that handler hands
//     its argv to (as TestUsageListsEveryParsedFlag reads them), across packages of this module.
//     After a verb, the verb's own branch and what it calls, plus what the command parses before
//     it picks the verb. Main's global flags count for every command, and so does `--help`, which
//     TestEveryRegisteredCommandAnswersHelp pins.
//
// What it does not read: a word after the verb (`yolo pack lint <dir>`, `yolo broker restart
// <name>`), which a command takes as an argument; a placeholder (`<agent>`, `%s`), after which
// nothing in that position is checked; short flags; and a command spelled in a message without
// backticks. A removed flag a handler still refuses by name (prune's `--keep-images`) reads as
// parsed, and the check does not run a hint, so it cannot say the command does what the hint
// promises. TestTheHintCheckRefusesWhatIsNotACommand keeps the resolver from passing everything.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// verbDispatcher names the function whose switch picks a command's verb: its package (a
// directory under the module root), the function, the expression the switch reads the verb from,
// and, for config, the map of further verbs.
type verbDispatcher struct {
	pkg, fn, on, verbMap string
}

// verbDispatchers is every command that takes a verb, by registry name, and where it picks it.
// The verbs themselves are read from that source; this table only says where to look.
var verbDispatchers = map[string]verbDispatcher{
	"pack":        {"internal/cli", "packMain", "args[0]", ""},
	"host":        {"internal/cli", "hostMain", "args[0]", ""},
	"programs":    {"internal/cli", "programsMain", "args[0]", ""},
	"config":      {"internal/cli", "configRunW", "verb", "configColorVerbs"},
	"loopholes":   {"internal/cli", "runLoopholes", "sub", ""},
	"broker":      {"internal/cli", "hostDaemonDispatch", "sub", ""},
	"host-daemon": {"internal/cli", "hostDaemonDispatch", "sub", ""},
	"claude-auth": {"internal/cli", "claudeAuthMain", "args[0]", ""},
	"openai-auth": {"internal/openaiauthhost", "runOperator", "args[0]", ""},
	// Not in the registry: Main routes `yolo internal` before the front door.
	"internal": {"internal/cli", "runInternal", "args[0]", ""},
}

// hintPattern is a backticked `yolo …` invocation inside a string literal.
var hintPattern = regexp.MustCompile("`(yolo [^`]*)`")

// longFlag is a long flag spelled at the start of a string literal: "--assert", "--store=".
var longFlag = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]*`)

func TestEveryHintedYoloCommandExists(t *testing.T) {
	tree := loadCommandTree(t)
	hints := scanHints(t, tree.src.root)
	if len(hints) < 100 {
		t.Fatalf("found %d backticked yolo commands under internal/ and cmd/; the scan has lost its input", len(hints))
	}
	for _, h := range hints {
		if problem := tree.check(h.text); problem != "" {
			t.Errorf("%s: `%s` %s", h.pos, h.text, problem)
		}
	}
}

// TestEveryHelpExampleExists: each command's `Examples:` lines are hints too, the first a reader
// copies. TestEveryCommandShowsACopyableExample checks that one of them routes to the command;
// this checks every one of them all the way down, so `yolo programs remove-undeclared`, an
// example for a verb `yolo programs` does not have, cannot ship again.
func TestEveryHelpExampleExists(t *testing.T) {
	tree := loadCommandTree(t)
	for _, sub := range slices.Sorted(maps.Keys(subcommandUsage)) {
		block, _ := exampleBlock(subcommandUsage[sub].text)
		for _, line := range block {
			if i := strings.Index(line, " #"); i >= 0 {
				line = line[:i]
			}
			if !strings.HasPrefix(line, "yolo ") || strings.ContainsAny(line, "|&;$") {
				continue // a pipeline or a shell wrapper around the command, not one command
			}
			if problem := tree.check(line); problem != "" {
				t.Errorf("`yolo %s --help` example `%s` %s", sub, line, problem)
			}
		}
	}
}

// TestTheHintCheckRefusesWhatIsNotACommand keeps the resolver honest: each line here names
// something this CLI does not run, and a resolver that passed everything would pass the scan.
func TestTheHintCheckRefusesWhatIsNotACommand(t *testing.T) {
	tree := loadCommandTree(t)
	for _, bad := range []string{
		"yolo builder",
		"yolo host codex",
		"yolo host check-deps",
		"yolo pack bogus",
		"yolo pack ls/bogus",
		"yolo host apply --bogus",
		"yolo host env --assert",
		"yolo pack install --from-plugin",
		"yolo loopholes status --apply",
		"yolo prune --keep-everything",
		"yolo --bogus -- bash",
		"yolo internal bogus",
		"yolo openai-auth bogus",
		"yolo config bogus",
	} {
		if tree.check(bad) == "" {
			t.Errorf("`%s` resolved, but it is not a command", bad)
		}
	}
	for _, good := range []string{
		"yolo -- <cmd>",
		"yolo --version",
		"yolo --at host -- <cmd>",
		"yolo -p %s -- %s",
		"yolo host -- codex",
		"yolo host apply --assert",
		"yolo pack ls/status",
		"yolo loopholes list --format json",
		"yolo config dump",
		"yolo config reset claude/settings",
		"yolo openai-auth import --from <auth.json>",
		"yolo broker restart",
		"yolo capture codex",
		"yolo check --no-build --format json",
		"yolo stores --format json",
		"yolo internal capture-materialize",
	} {
		if problem := tree.check(good); problem != "" {
			t.Errorf("`%s` is a command, but the check says it %s", good, problem)
		}
	}
}

// TestEveryVerbDispatcherIsListed: a command whose refusal names a verb it does not have takes
// verbs, so its dispatcher must be in verbDispatchers, or the check above would read its verbs as
// arguments and pass any of them. And every listed dispatcher yields verbs, so an entry cannot
// go vacuous through a rename.
func TestEveryVerbDispatcherIsListed(t *testing.T) {
	tree := loadCommandTree(t)
	for _, k := range slices.Sorted(maps.Keys(verbDispatchers)) {
		if len(tree.verbs(k)) == 0 {
			t.Errorf("verbDispatchers[%q] names %+v, which yields no verbs", k, verbDispatchers[k])
		}
	}
	refusal := regexp.MustCompile(`^yolo ([a-z-]+): unknown (verb|subcommand|command)|Usage: yolo ([a-z-]+) \{`)
	for _, fn := range tree.src.load("internal/cli").funcs {
		ast.Inspect(fn.decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if m := refusal.FindStringSubmatch(v); m != nil {
				k := m[1] + m[3]
				if _, ok := verbDispatchers[k]; !ok {
					t.Errorf("%s refuses an unknown verb of `yolo %s` (%q), but verbDispatchers does not "+
						"name its dispatcher", fn.decl.Name.Name, k, v)
				}
			}
			return true
		})
	}
}

// hint is one backticked `yolo …` command in a string literal, and where it is.
type hint struct{ pos, text string }

// scanHints reads every non-test Go file under internal/ and cmd/ for hints in string literals.
// Comments are not read: a backticked command in a comment is the code's own documentation, and
// rule 4 is about what a user is told.
func scanHints(t *testing.T, root string) []hint {
	t.Helper()
	var out []hint
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for _, m := range hintPattern.FindAllStringSubmatch(v, -1) {
					out = append(out, hint{pos: fmt.Sprintf("%s:%d", rel, fset.Position(lit.Pos()).Line), text: m[1]})
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// commandTree is the CLI as its dispatchers' source spells it.
type commandTree struct {
	src *srcIndex
	// handlers is the registry's name → handler, read from dispatch.go's literal.
	handlers map[string]*srcFunc
	global   map[string]bool
}

func loadCommandTree(t *testing.T) *commandTree {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module := ""
	for _, l := range strings.Split(string(gomod), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
			module = strings.TrimSpace(m)
		}
	}
	src := &srcIndex{t: t, root: root, module: module, pkgs: map[string]*srcPkg{}}
	cli := src.load("internal/cli")
	tree := &commandTree{src: src, handlers: map[string]*srcFunc{}}
	reg, ok := cli.vars["registry"]
	if !ok || len(reg.Values) != 1 {
		t.Fatal("no `registry` literal in internal/cli")
	}
	for _, elt := range reg.Values[0].(*ast.CompositeLit).Elts {
		kv := elt.(*ast.KeyValueExpr)
		name, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
		if fn := cli.funcs[kv.Value.(*ast.Ident).Name]; fn != nil {
			tree.handlers[name] = fn
		}
	}
	if len(tree.handlers) != len(registry) {
		t.Fatalf("read %d handlers from the registry literal, and the registry has %d", len(tree.handlers), len(registry))
	}
	tree.handlers["internal"] = cli.funcs["runInternal"]
	// Main consumes these before any command sees its argv (cli.go).
	tree.global = map[string]bool{"--help": true}
	src.flagsFrom([]*srcFunc{cli.funcs["applyUserLayerFlag"], cli.funcs["applyVerboseFlag"]}, tree.global, nil)
	return tree
}

// check resolves one hint, and says what in it is not a command ("" when all of it is).
func (c *commandTree) check(text string) string {
	fields := strings.Fields(text)[1:]
	if len(fields) == 0 || placeholder(fields[0]) {
		return ""
	}
	switch fields[0] {
	case "--version", "--help", "-h", "help":
		return "" // Main answers these before routing (cli.go)
	}
	k, at := "internal", 0
	if fields[0] != "internal" {
		sub, _, _ := routeArgv(append([]string(nil), fields...))
		if sub == "" {
			return "names a command this CLI does not have"
		}
		k, at = sub, positionalIndex(fields, sub)
	}
	before, rest := fields, []string(nil)
	if at >= 0 {
		before, rest = fields[:at], fields[at+1:]
	}
	if p := c.checkFlags(k, "", before); p != "" {
		return p
	}
	verb := ""
	if verbs := c.verbs(k); verbs != nil && len(rest) > 0 && bareWord(rest[0]) {
		for _, alt := range strings.Split(rest[0], "/") {
			if _, ok := verbs[alt]; !ok {
				return fmt.Sprintf("names `yolo %s %s`, and yolo %s has no verb %q (it has %s)", k, alt, k, alt,
					strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
			}
		}
		verb, rest = strings.Split(rest[0], "/")[0], rest[1:]
	}
	return c.checkFlags(k, verb, rest)
}

// checkFlags checks every long flag in args, up to a `--`, against what k (after verb, when one
// was named) parses.
func (c *commandTree) checkFlags(k, verb string, args []string) string {
	var flags map[string]bool
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "--") || len(a) <= 2 || placeholder(a) {
			continue
		}
		name, _, _ := strings.Cut(a, "=")
		if c.global[name] {
			continue
		}
		if flags == nil {
			flags = c.flags(k, verb)
		}
		if !flags[name] {
			where := "yolo " + k
			if verb != "" {
				where += " " + verb
			}
			return fmt.Sprintf("names %s, which `%s` does not parse", name, where)
		}
	}
	return ""
}

// verbs is k's verbs, each with the nodes that run it, or nil when k takes no verb.
func (c *commandTree) verbs(k string) map[string]*verbBranch {
	d, ok := verbDispatchers[k]
	if !ok {
		return nil
	}
	pkg := c.src.load(d.pkg)
	fn := pkg.funcs[d.fn]
	if fn == nil {
		return map[string]*verbBranch{}
	}
	out := map[string]*verbBranch{}
	add := func(v string, from *srcFunc, n ast.Node) {
		if v == "" || v == "help" || strings.HasPrefix(v, "-") {
			return
		}
		if out[v] == nil {
			out[v] = &verbBranch{}
		}
		out[v].nodes = append(out[v].nodes, srcNode{from, n})
	}
	ast.Inspect(fn.decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SwitchStmt:
			if n.Tag == nil || types.ExprString(n.Tag) != d.on {
				return true
			}
			for _, s := range n.Body.List {
				cc := s.(*ast.CaseClause)
				for _, e := range cc.List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						add(v, fn, cc)
					}
				}
			}
		case *ast.IfStmt:
			if b, ok := n.Cond.(*ast.BinaryExpr); ok && b.Op == token.EQL && types.ExprString(b.X) == d.on {
				if lit, ok := b.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					add(v, fn, n.Body)
				}
			}
		}
		return true
	})
	if d.verbMap != "" {
		if vs, ok := pkg.vars[d.verbMap]; ok && len(vs.Values) == 1 {
			for _, elt := range vs.Values[0].(*ast.CompositeLit).Elts {
				kv := elt.(*ast.KeyValueExpr)
				v, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
				add(v, fn, kv.Value)
			}
		}
	}
	return out
}

// verbBranch is the code one verb runs: its case clause, if body or map value.
type verbBranch struct{ nodes []srcNode }

type srcNode struct {
	in *srcFunc
	n  ast.Node
}

// flags is every long flag k parses, or with a verb, what it parses before picking the verb and
// what that verb's branch parses.
func (c *commandTree) flags(k, verb string) map[string]bool {
	out := map[string]bool{}
	h := c.handlers[k]
	if h == nil {
		return out
	}
	verbs := c.verbs(k)
	if verb == "" || verbs == nil {
		c.src.flagsFrom([]*srcFunc{h}, out, nil)
		return out
	}
	// Before the verb: the handler's own parse, skipping every verb's branch.
	skip := map[ast.Node]bool{}
	for _, b := range verbs {
		for _, n := range b.nodes {
			skip[n.n] = true
		}
	}
	c.src.flagsFrom([]*srcFunc{h}, out, skip)
	// The verb's branch, and what it calls, whatever their signatures: a verb's handler may take
	// no argv and parse nothing, which is then the truth about it.
	for _, n := range verbs[verb].nodes {
		next := c.src.scan(n.in, n.n, nil, out)
		c.src.flagsFrom(next, out, nil)
	}
	return out
}

// positionalIndex is where sub stands in fields as the command name, skipping the values of value
// flags as Subcommand does, or -1 when no token names it (an implicit `run`, or `--at host`).
func positionalIndex(fields []string, sub string) int {
	for i := 0; i < len(fields); i++ {
		a := fields[i]
		if a == "--" {
			return -1
		}
		if valueTakingFlags[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == sub {
			return i
		}
		return -1
	}
	return -1
}

// placeholder is a word a message fills in or a reader replaces: `<agent>`, `%s`, `[name]`, `…`.
func placeholder(w string) bool { return strings.ContainsAny(w, "<>%{}[]…") || w == "..." }

func bareWord(w string) bool { return !strings.HasPrefix(w, "-") && !placeholder(w) }

// srcIndex parses this module's packages on demand.
type srcIndex struct {
	t            *testing.T
	root, module string
	pkgs         map[string]*srcPkg
}

type srcPkg struct {
	funcs map[string]*srcFunc
	vars  map[string]*ast.ValueSpec
	// scanned is each variable scanVar has read, and the flags it found there.
	scanned map[string]map[string]bool
}

// srcFunc is one package-level function and the imports of the file that declares it.
type srcFunc struct {
	decl    *ast.FuncDecl
	pkg     string
	imports map[string]string
}

// load parses the non-test files of the package at dir (relative to the module root, or a full
// import path of this module).
func (x *srcIndex) load(dir string) *srcPkg {
	dir = strings.TrimPrefix(strings.TrimPrefix(dir, x.module), "/")
	if p, ok := x.pkgs[dir]; ok {
		return p
	}
	p := &srcPkg{funcs: map[string]*srcFunc{}, vars: map[string]*ast.ValueSpec{},
		scanned: map[string]map[string]bool{}}
	x.pkgs[dir] = p
	entries, err := os.ReadDir(filepath.Join(x.root, dir))
	if err != nil {
		x.t.Fatalf("read package %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(x.root, dir, name), nil, 0)
		if err != nil {
			x.t.Fatalf("parse %s/%s: %v", dir, name, err)
		}
		imports := map[string]string{}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			local := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				local = imp.Name.Name
			}
			imports[local] = path
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					p.funcs[d.Name.Name] = &srcFunc{decl: d, pkg: dir, imports: imports}
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if vs, ok := s.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							p.vars[n.Name] = vs
						}
					}
				}
			}
		}
	}
	return p
}

// callee is the function of this module call calls, or nil.
func (x *srcIndex) callee(from *srcFunc, call *ast.CallExpr) *srcFunc {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return x.load(from.pkg).funcs[fn.Name]
	case *ast.SelectorExpr:
		id, ok := fn.X.(*ast.Ident)
		if !ok {
			return nil
		}
		path, ok := from.imports[id.Name]
		if !ok || !strings.HasPrefix(path, x.module+"/") {
			return nil
		}
		return x.load(path).funcs[fn.Sel.Name]
	}
	return nil
}

// scan adds the long flags spelled at the start of string literals under n to flags, and returns
// the functions of this module n calls. A node skip holds is not entered.
func (x *srcIndex) scan(from *srcFunc, n ast.Node, skip map[ast.Node]bool, flags map[string]bool) []*srcFunc {
	var callees []*srcFunc
	ast.Inspect(n, func(m ast.Node) bool {
		if m == nil || skip[m] {
			return false
		}
		switch m := m.(type) {
		case *ast.BasicLit:
			if m.Kind == token.STRING {
				if v, err := strconv.Unquote(m.Value); err == nil {
					if f := longFlag.FindString(v); f != "" {
						flags[f] = true
					}
				}
			}
		case *ast.Ident:
			// A package-level table a parser reads its flags from (launchValueFlags).
			x.scanVar(x.load(from.pkg), m.Name, flags)
		case *ast.SelectorExpr:
			if id, ok := m.X.(*ast.Ident); ok {
				if path, ok := from.imports[id.Name]; ok && strings.HasPrefix(path, x.module+"/") {
					x.scanVar(x.load(path), m.Sel.Name, flags)
				}
			}
		case *ast.CallExpr:
			if c := x.callee(from, m); c != nil {
				callees = append(callees, c)
			}
			// A flag.FlagSet definition names its flag without the dashes: fs.String("from", …),
			// fs.StringVar(&v, "from", …).
			if sel, ok := m.Fun.(*ast.SelectorExpr); ok && flagSetMethods[sel.Sel.Name] {
				for _, a := range m.Args[:min(2, len(m.Args))] {
					if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil && v != "" {
							flags["--"+v] = true
						}
						break
					}
				}
			}
		}
		return true
	})
	return callees
}

// scanVar adds the long flags spelled in the initializer of pkg's package-level variable name:
// the literals of a table, not what its functions do. Each variable is read once.
func (x *srcIndex) scanVar(pkg *srcPkg, name string, flags map[string]bool) {
	if found, ok := pkg.scanned[name]; ok {
		maps.Copy(flags, found)
		return
	}
	vs, ok := pkg.vars[name]
	if !ok {
		return
	}
	found := map[string]bool{}
	pkg.scanned[name] = found
	for _, v := range vs.Values {
		ast.Inspect(v, func(m ast.Node) bool {
			if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					if f := longFlag.FindString(s); f != "" {
						found[f] = true
					}
				}
			}
			return true
		})
	}
	maps.Copy(flags, found)
}

// flagSetMethods are the flag.FlagSet methods that define a flag; each names it in its first or
// second argument.
var flagSetMethods = map[string]bool{
	"String": true, "StringVar": true, "Bool": true, "BoolVar": true, "Int": true, "IntVar": true,
	"Int64": true, "Int64Var": true, "Uint": true, "UintVar": true, "Uint64": true, "Uint64Var": true,
	"Float64": true, "Float64Var": true, "Duration": true, "DurationVar": true, "Func": true,
	"BoolFunc": true, "TextVar": true, "Var": true,
}

// flagsFrom adds the flags each of starts parses, following each callee that takes a []string,
// the shape of an argv parser, for up to four calls.
func (x *srcIndex) flagsFrom(starts []*srcFunc, flags map[string]bool, skip map[ast.Node]bool) {
	seen := map[*srcFunc]bool{}
	level := starts
	for depth := 0; depth <= 4 && len(level) > 0; depth++ {
		var next []*srcFunc
		for _, fn := range level {
			if fn == nil || seen[fn] {
				continue
			}
			seen[fn] = true
			for _, c := range x.scan(fn, fn.decl, skip, flags) {
				if takesArgv(c.decl) {
					next = append(next, c)
				}
			}
		}
		level = next
	}
}
