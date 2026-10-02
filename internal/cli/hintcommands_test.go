package cli

// hintcommands_test.go enforces rule 4 of docs/reference/happy-path-principle.md ("hints are
// tested so they can't go stale") for messages: every `yolo …` command a message names, in
// backticks or where a command stands without them, is one this CLI runs. A hint naming a deleted
// command or flag is a dead end that looks like a next step (the doc coins both terms), and the
// tests that read a hint used to pin only its string, so `yolo host codex` and
// `yolo host check-deps` shipped though neither is a command.
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
//     After a verb, the verb's own branch and what it calls (for config, the function its verb map
//     names), plus what the command parses outside every verb's branch; with no verb, only the
//     latter. Main's global flags count for every command, and so does `--help`, which
//     TestEveryRegisteredCommandAnswersHelp pins.
//
// THE HINTS READ are the `yolo …` commands in a string literal, or in a chain of literals joined
// with `+`, where each piece that is not a literal (a variable, a call, a constant) reads as a
// placeholder: "`yolo pack lint " + dir + "`" is read as `yolo pack lint <…>`. A fmt verb is a
// placeholder too, so `yolo %s` checks nothing and `yolo pack install %s` checks `pack install`.
// A command is read in either of two spellings (hintsIn):
//
//   - In backticks, anywhere: `yolo …`.
//   - Without backticks (bareHints), in the text as a user reads it, its console markup ([dim],
//     [cyan]) removed, where a command stands on its line. Indented ("  yolo check-deps  # check
//     again", and the lines of usage text), or in the column a colon and two spaces open ("Check
//     the edit:  yolo check"), it must fill the rest of the line, up to a gap of two spaces before
//     a comment or a description. Right after a lead-in this codebase writes before a next step
//     ("then: ", "fix: ", "run: ", "run ", "next: ", in any case, and "→ ", "-> "), or after a
//     shell operator chaining one ("switch && yolo check", "||"), it may also end at a quote, a
//     parenthesis, a sentence end, a dash or a shell operator ("Run yolo update, then
//     relaunch."). An indented line whose `yolo` runs on into a sentence ("  yolo could not get
//     this host's stack…") is prose with yolo as its subject, and is not read; nor is an indented
//     line that fills its line but carries on the paragraph above it, text at the same indent that
//     neither ends in a colon nor is itself a command line.
//     Nothing that is read is skipped: a word after `yolo` that is not a command fails the test,
//     which is also how a misread of prose shows. Prose naming the program writes `yolo` in
//     backticks ("Run `yolo` as your normal user"); a form the scan misreads is fixed in
//     bareHints; neither is fixed by an exception for the message.
//
// What it does not read: a command at the start of an unindented line or literal (a field holding
// "yolo prune --apply"), which has the shape of a sentence or an error beginning with yolo ("yolo
// could not create it", "yolo config render: unknown flag"); one after a colon and one space ("in
// a new terminal: yolo check"), the shape of prose about yolo ("it is empty: yolo was started with
// no PATH"); an indented one that ends anywhere but at a two-space gap or its line's end, such as
// at a period, a comma or a pipe ("  yolo audit --json | jq .argv"); a usage line's synopsis
// ("Usage: yolo audit [flags]"); one after any other word, a quote or a parenthesis ("use yolo
// host", "'yolo config-ref'", "(yolo config diff claude)"); a word after the verb (`yolo pack lint
// <dir>`, `yolo broker restart <name>`), which a command takes as an argument; the word a
// placeholder stands in for; and short flags. A flag the command parses before picking its verb
// and then refuses for one verb (`yolo loopholes enable --format json`), or a removed flag a
// handler still refuses by name (prune's `--keep-images`), reads as parsed, and the check does not
// run a hint, so it cannot say the command does what the hint promises.
// TestTheHintCheckRefusesWhatIsNotACommand keeps the resolver, and the scan of each spelling,
// from passing everything.

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

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
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

// bareYolo is `yolo` as a command's first word without backticks: at the start of a line, or
// after a space or a tab, and followed by a space.
var bareYolo = regexp.MustCompile(`(?:^|[ \t])yolo `)

// nextStepLead is a lead-in this codebase writes right before a next step's command, or a shell
// operator chaining one, ending where the command begins: "then: yolo check", "Run yolo update",
// "Next: yolo pack lint", "→ yolo host apply", "nixos-rebuild switch && yolo check". A word must
// start where it does, so "rerun" is not "run".
var nextStepLead = regexp.MustCompile(`(?i)(?:(?:^|[^\pL\pN_])(?:then:|fix:|run:?|next:)|→|->|&&|\|\|)[ \t]+$`)

// alignedLead is a colon followed by a gap of two spaces, the column a list of steps aligns its
// commands in: "  2. Check the edit:  yolo check".
var alignedLead = regexp.MustCompile(`:[ \t]{2,}$`)

// bareHintEnd is where a command written without backticks ends before the end of its line: a
// gap of two spaces (before a "# comment", or a usage line's description), a quote, backtick or
// closing parenthesis, an opening parenthesis after a space, sentence punctuation followed by a
// space or the end, a dash, or a shell operator.
var bareHintEnd = regexp.MustCompile("  |[\"'`)\u201d\u2019]| \\(|[.,;:!?](?:\\s|$)| [\u2014\u2013] | (?:\\||&&|>) ")

// longFlag is a long flag spelled at the start of a string literal: "--assert", "--store=".
var longFlag = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]*`)

func TestEveryHintedYoloCommandExists(t *testing.T) {
	tree := loadCommandTree(t)
	hints := scanHints(t, tree.src.root)
	bare := 0
	for _, h := range hints {
		if h.bare {
			bare++
		}
	}
	if len(hints)-bare < 100 || bare < 50 {
		t.Fatalf("found %d backticked yolo commands and %d written without backticks under internal/ and cmd/; "+
			"the scan has lost its input", len(hints)-bare, bare)
	}
	for _, h := range hints {
		problem := tree.check(h.text)
		switch {
		case problem == "":
		case h.bare:
			t.Errorf("%s: yolo command written without backticks, %q, %s. If that is prose naming the "+
				"program, write `yolo` in backticks there; if the scan misread a command's form, fix "+
				"bareHints; never exempt the message", h.pos, h.text, problem)
		default:
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
		"yolo config ls --bogus",
		// A flag with no verb before it is read against what the command parses outside its
		// verbs' branches, not against every verb's flags at once.
		"yolo host --assert -- codex",
	} {
		if tree.check(bad) == "" {
			t.Errorf("`%s` resolved, but it is not a command", bad)
		}
	}
	// The same, written without backticks in each place the scan reads one: there a deleted verb
	// or flag must fail too, not read as prose and pass over.
	for _, text := range []string{
		"Fix the store, then: yolo builder",
		"fix: yolo host check-deps",
		"Run yolo builder to rebuild the image.",
		"run: yolo pack bogus <dir>",
		"Next: yolo host codex",
		"→ yolo prune --keep-everything",
		"  -> yolo host apply --bogus   (asks each question first)",
		"Restart the jail:\n  yolo builder  # from this workspace\n",
		"[dim]  yolo loopholes status --apply[/dim]",
		"The store is read-only.\n  yolo builder\n",
		"  2. Check the edit:  yolo builder",
		"  yolo config bogus <agent>/<surface>   discard its captured edits",
		"then: sudo nixos-rebuild switch && yolo builder",
	} {
		hs := hintsIn(text)
		if len(hs) != 1 || !hs[0].bare {
			t.Errorf("%q: read %+v, want one command written without backticks", text, hs)
			continue
		}
		if tree.check(hs[0].text) == "" {
			t.Errorf("%q: `%s` resolved, but it is not a command", text, hs[0].text)
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
		// config picks these verbs from a map of functions, so a verb's flags are read from the
		// function the map names.
		"yolo config ls --all",
		"yolo config promote <agent> --plan",
		"yolo loopholes --format json",
		"yolo openai-auth import --from <auth.json>",
		"yolo broker restart",
		"yolo capture codex",
		"yolo check --no-build --format json",
		"yolo stores --format json",
		"yolo internal capture-materialize",
		// Main strips the verbose flag before routing, so every command takes it.
		"yolo --verbose -- <cmd>",
		"yolo host apply --verbose",
	} {
		if problem := tree.check(good); problem != "" {
			t.Errorf("`%s` is a command, but the check says it %s", good, problem)
		}
	}
}

// TestTheHintScanReadsAHintBuiltFromPieces: a hint a message builds with `+` is read as the
// joined text, a piece that is not a literal standing for a placeholder, so a command word
// written in a literal piece is still checked. The scan read only one literal at a time, and a
// literal holding an opening backtick and no closing one held no hint, so a message that spliced
// a name into its command (`yolo loopholes disable " + name + "`) was never read at all.
func TestTheHintScanReadsAHintBuiltFromPieces(t *testing.T) {
	got := scanSource(t, "package x\n\nimport \"fmt\"\n\nvar name = \"x\"\n\n"+
		"var one = \"Run `yolo nosuchone` now.\"\n"+
		"var spliced = \"Run `yolo nosuchtwo \" + name + \"` now.\"\n"+
		"var split = \"Run `\" + \"yolo nosuchthree\" + \"` now.\"\n"+
		"var inner = \"a \" + fmt.Sprintf(\"Run `yolo nosuchfour %s`.\", name) + \" b\"\n"+
		"var ints = 1 + 2\n")
	want := []string{"yolo nosuchone", "yolo nosuchtwo <…>", "yolo nosuchthree", "yolo nosuchfour %s"}
	if !slices.Equal(got, want) {
		t.Errorf("scanned hints = %q, want %q", got, want)
	}
	tree := loadCommandTree(t)
	for _, h := range want {
		if tree.check(h) == "" {
			t.Errorf("`%s` resolved, but it is not a command", h)
		}
	}
}

// TestTheHintScanReadsACommandWrittenWithoutBackticks: a command a message spells without
// backticks is read where one stands (an indented line, after a next step's lead-in or a shell
// operator, in a column after a colon) up to where it ends, and a sentence whose subject is yolo
// is not read, even on an indented line of a paragraph with no punctuation to show it runs on, so
// a word after `yolo` that is not a command fails the scan rather than reading as prose. The scan
// read backticked commands only, so the `then: yolo check` many `yolo check` notes end with, and
// `yolo check-deps`'s closing re-check, were never checked.
func TestTheHintScanReadsACommandWrittenWithoutBackticks(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"then: yolo check", []string{"yolo check"}},
		{"Make it writable, then: yolo check --no-build\nIf that does not fix it, report it.",
			[]string{"yolo check --no-build"}},
		{"fix: yolo pack install   (retries the fetch)", []string{"yolo pack install"}},
		{"Run yolo update, then relaunch the jail.", []string{"yolo update"}},
		{"→ yolo host apply --assert (shows each question), then launch again.",
			[]string{"yolo host apply --assert"}},
		{"-> yolo loopholes status", []string{"yolo loopholes status"}},
		{"Pack scaffolded.\nNext: yolo pack lint ./pack\n", []string{"yolo pack lint ./pack"}},
		{"Restart the jail:\n  yolo stop      # from this workspace\n", []string{"yolo stop"}},
		{"[dim]  yolo check-deps  [dim]# check again[/dim][/dim]", []string{"yolo check-deps"}},
		{"  yolo host apply [flags]       render config surfaces into your real home",
			[]string{"yolo host apply [flags]"}},
		{"  2. Check the edit:  yolo check\n  3. Launch it:  yolo -- claude\n",
			[]string{"yolo check", "yolo -- claude"}},
		{"Examples:\n  yolo pack ls\n  yolo pack install\n",
			[]string{"yolo pack ls", "yolo pack install"}},
		{"never to a grant left behind.\n  yolo macos-fix-permissions <path>",
			[]string{"yolo macos-fix-permissions <path>"}},
		{"then: sudo nixos-rebuild switch && yolo check", []string{"yolo check"}},
		{"  rm <path> && yolo pack install", []string{"yolo pack install"}},
		// A sentence whose subject is yolo, a heading, an error's prefix, and a command in a
		// place nothing in the text marks as one.
		{"yolo could not create it", nil},
		{"yolo prune --apply", nil},
		{"yolo config render: unknown flag %q", nil},
		{"yolo pack — author and inspect agent config packs", nil},
		{"  yolo could not get this host's network stack to forward it", nil},
		// An indented line that carries on the paragraph above it at the same indent, with no
		// punctuation to show it runs on.
		{"  This is a known limitation of this host.\n" +
			"  yolo could not get the network stack to forward it\n", nil},
		{"  yolo will run: claude", nil},
		{"Image store: yolo cannot write it.", nil},
		{"which is empty: yolo was started with no PATH", nil},
		{"Usage: yolo audit [flags]", nil},
		{"the yolo jail keeper; yolo's own copy; YOLO Jail", nil},
		{"Run `yolo` as your normal user, or 'yolo config-ref' for the schema.", nil},
	} {
		var got []string
		for _, h := range hintsIn(c.text) {
			got = append(got, h.text)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: read %q, want %q", c.text, got, c.want)
		}
	}
	// And from source, as the scan reads it: a constant a note ends with, a literal joined
	// with a name, and a command on a line of its own.
	got := scanSource(t, "package x\n\nvar name = \"x\"\n\n"+
		"const recheck = \"then: yolo nosuchone\"\n"+
		"var note = \"Restart the jail:\\n  yolo nosuchtwo  # now\\n\" + name\n"+
		"var fix = \"fix: yolo nosuchthree \" + name + \" (retries it)\"\n")
	want := []string{"yolo nosuchone", "yolo nosuchtwo", "yolo nosuchthree <…>"}
	if !slices.Equal(got, want) {
		t.Errorf("scanned hints = %q, want %q", got, want)
	}
	tree := loadCommandTree(t)
	for _, h := range want {
		if tree.check(h) == "" {
			t.Errorf("`%s` resolved, but it is not a command", h)
		}
	}
}

// scanSource is what scanHints reads from one Go file holding src, in a module tree of its own.
func scanSource(t *testing.T, src string) []string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "x", "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range scanHints(t, root) {
		got = append(got, h.text)
	}
	return got
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

// hint is one `yolo …` command in a string literal, and where it is. bare is whether it is
// written without backticks.
type hint struct {
	pos, text string
	bare      bool
}

// hintsIn is every `yolo …` command text names, backticked or bare, with no position.
func hintsIn(text string) []hint {
	var out []hint
	for _, m := range hintPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, hint{text: m[1]})
	}
	for _, h := range bareHints(text) {
		out = append(out, hint{text: h, bare: true})
	}
	return out
}

// bareHints is every command text names without backticks, in the places the header comment
// says it is read.
func bareHints(text string) []string {
	var out []string
	prev, prevCommand := "", false
	for i, line := range strings.Split(richtext.Strip(text), "\n") {
		command := false
		for _, loc := range bareYolo.FindAllStringIndex(line, -1) {
			start := loc[1] - len("yolo ")
			lead := line[:start]
			anyEnd := nextStepLead.MatchString(lead)
			indented := lead != "" && strings.TrimLeft(lead, " \t") == ""
			if !anyEnd && !indented && !alignedLead.MatchString(lead) {
				continue // unindented, or after a word: nothing marks it as a command
			}
			cmd, after := line[loc[1]:], ""
			if m := bareHintEnd.FindStringIndex(cmd); m != nil {
				cmd, after = cmd[:m[0]], cmd[m[0]:]
			}
			if !anyEnd && after != "" && !strings.HasPrefix(after, "  ") {
				continue // the line runs on into a sentence whose subject is yolo
			}
			if indented && !anyEnd && after == "" && i > 0 && continuesParagraph(prev, line, prevCommand) {
				continue // the next line of a paragraph, whose sentence has yolo as its subject
			}
			command = command || indented
			out = append(out, strings.TrimSpace("yolo "+cmd))
		}
		prev, prevCommand = line, command
	}
	return out
}

// continuesParagraph reports whether line carries on the paragraph prev is in: prev is text at
// the same indent that neither introduces line (a colon) nor is itself a command line, as a block
// of examples is.
func continuesParagraph(prev, line string, prevCommand bool) bool {
	p := strings.TrimSpace(prev)
	if p == "" || strings.HasSuffix(p, ":") || prevCommand {
		return false
	}
	indent := func(s string) string { return s[:len(s)-len(strings.TrimLeft(s, " \t"))] }
	return indent(prev) == indent(line)
}

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
			add := func(at token.Pos, text string) {
				for _, h := range hintsIn(text) {
					h.pos = fmt.Sprintf("%s:%d", rel, fset.Position(at).Line)
					out = append(out, h)
				}
			}
			var visit func(ast.Node) bool
			visit = func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BinaryExpr:
					// A message built with `+` is read as one text, so a hint split across
					// literals, or with a name spliced into it, is read too. Each piece that is
					// not a literal reads as a placeholder, and is scanned on its own.
					text, rest, ok := joinedLiterals(n)
					if !ok {
						return true
					}
					add(n.Pos(), text)
					for _, r := range rest {
						ast.Inspect(r, visit)
					}
					return false
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						if v, err := strconv.Unquote(n.Value); err == nil {
							add(n.Pos(), v)
						}
					}
				}
				return true
			}
			ast.Inspect(f, visit)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// joinedLiterals is the text a chain of `+` builds: each string literal's value, and "<…>" (a
// placeholder) for each piece that is not one, which rest returns so the caller can scan it on
// its own. ok is false for a chain with no string literal in it, an arithmetic sum.
func joinedLiterals(e *ast.BinaryExpr) (text string, rest []ast.Expr, ok bool) {
	if e.Op != token.ADD {
		return "", nil, false
	}
	var b strings.Builder
	var walk func(ast.Expr)
	walk = func(x ast.Expr) {
		switch x := x.(type) {
		case *ast.ParenExpr:
			walk(x.X)
			return
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				walk(x.X)
				walk(x.Y)
				return
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if v, err := strconv.Unquote(x.Value); err == nil {
					b.WriteString(v)
					ok = true
					return
				}
			}
		}
		b.WriteString("<…>")
		rest = append(rest, x)
	}
	walk(e)
	return b.String(), rest, ok
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
	// Main consumes these before any command sees its argv (cli.go). isVerboseFlag is
	// applyVerboseFlag's test of one argument, which flagsFrom does not follow from it, since it
	// takes no argv.
	tree.global = map[string]bool{"--help": true}
	src.flagsFrom([]*srcFunc{cli.funcs["applyUserLayerFlag"], cli.funcs["applyVerboseFlag"],
		cli.funcs["isVerboseFlag"]}, tree.global, nil)
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
	add := func(v string, from *srcFunc, n ast.Node, bare bool) {
		if v == "" || v == "help" || strings.HasPrefix(v, "-") {
			return
		}
		if out[v] == nil {
			out[v] = &verbBranch{}
		}
		out[v].nodes = append(out[v].nodes, srcNode{in: from, n: n, bare: bare})
	}
	ast.Inspect(fn.decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SwitchStmt:
			if n.Tag == nil || types.ExprString(n.Tag) != d.on {
				return true
			}
			for _, s := range n.Body.List {
				cc := s.(*ast.CaseClause)
				var vs []string
				for _, e := range cc.List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						vs = append(vs, v)
					}
				}
				// `case "", "list":` is also what the command runs with no verb.
				bare := slices.Contains(vs, "")
				for _, v := range vs {
					add(v, fn, cc, bare)
				}
			}
		case *ast.IfStmt:
			if b, ok := n.Cond.(*ast.BinaryExpr); ok && b.Op == token.EQL && types.ExprString(b.X) == d.on {
				if lit, ok := b.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					add(v, fn, n.Body, false)
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
				add(v, fn, kv.Value, false)
			}
		}
	}
	return out
}

// verbBranch is the code one verb runs: its case clause, if body or map value.
type verbBranch struct{ nodes []srcNode }

// srcNode is one verb's branch in the function in. bare is whether the command also runs it with
// no verb (a `case "", "list":` clause).
type srcNode struct {
	in   *srcFunc
	n    ast.Node
	bare bool
}

// flags is every long flag k parses. For a command that takes verbs it is what the command
// parses outside every verb's branch, plus, with a verb, what that verb's branch parses; with no
// verb, a branch the command also runs with none counts as its own. A flag written with no verb
// used to be read against every verb's flags at once, so `yolo host --assert -- codex` passed.
func (c *commandTree) flags(k, verb string) map[string]bool {
	out := map[string]bool{}
	h := c.handlers[k]
	if h == nil {
		return out
	}
	verbs := c.verbs(k)
	if verbs == nil {
		c.src.flagsFrom([]*srcFunc{h}, out, nil)
		return out
	}
	skip := map[ast.Node]bool{}
	for _, b := range verbs {
		for _, n := range b.nodes {
			if verb != "" || !n.bare {
				skip[n.n] = true
			}
		}
	}
	c.src.flagsFrom([]*srcFunc{h}, out, skip)
	if verb == "" {
		return out
	}
	// The verb's branch, and what it calls, whatever their signatures: a verb's handler may take
	// no argv and parse nothing, which is then the truth about it. A branch that is a function
	// VALUE, as config's verb map holds, is that function.
	for _, n := range verbs[verb].nodes {
		next := c.src.scan(n.in, n.n, nil, out)
		if e, ok := n.n.(ast.Expr); ok {
			if f := c.src.funcValue(n.in, e); f != nil {
				next = append(next, f)
			}
		}
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
	return x.funcValue(from, call.Fun)
}

// funcValue is the package-level function of this module e names, from the file of from, or nil.
func (x *srcIndex) funcValue(from *srcFunc, e ast.Expr) *srcFunc {
	switch fn := e.(type) {
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
