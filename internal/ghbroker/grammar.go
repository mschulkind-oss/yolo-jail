package ghbroker

import (
	"strings"
	"unicode"
)

// grammar.go is the lookup half of the gh flag grammar grammar_gen.go carries.
//
// The table is MEASURED from gh by tools/ghgrammar, never typed: every command path gh
// has, its usage line, every flag it accepts (local and inherited), and whether each flag
// takes a value, which the generator asks gh's own parser rather than reading off the help
// text (a boolean whose description backquotes a word prints a placeholder, so the text
// cannot say). The classifier refuses anything this table does not name
// (docs/design/boundary-broker.md BB-P2, BB-D3), so a gh upgrade that adds a subcommand
// or a flag is refused until the table is regenerated and the policy reviewed.

// ghFlag is one flag of one command.
type ghFlag struct {
	// long is the flag's long name without the leading dashes; short its one-letter form,
	// or "".
	long, short string
	// value says the flag takes a value (`--limit 5`); optValue that it takes one only
	// glued on with `=` (pflag's NoOptDefVal, `gh browse --commit[=sha]`).
	value, optValue bool
	// placeholder and desc are gh's own help text, kept so a policy rule can tell two
	// flags of one spelling apart (the formatting `--template` from issue create's).
	placeholder, desc string
}

// ghCommand is one command path and its flags.
type ghCommand struct {
	path    string
	usage   string
	aliases []string
	flags   []ghFlag
}

// flagByLong and flagByShort look a flag up on one command.
func (c *ghCommand) flagByLong(name string) *ghFlag {
	for i := range c.flags {
		if c.flags[i].long == name {
			return &c.flags[i]
		}
	}
	return nil
}

func (c *ghCommand) flagByShort(name string) *ghFlag {
	for i := range c.flags {
		if c.flags[i].short == name {
			return &c.flags[i]
		}
	}
	return nil
}

// isGroup reports whether path names a command group (one with subcommands) rather than a
// command that runs.
func isGroup(path string) bool {
	prefix := path + " "
	for p := range grammar {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// aliasIndex maps an alias spelling, keyed by its CANONICAL parent path plus the alias
// word ("pr ls" → "list", " cs" → "codespace"), to the canonical word. Built once from the
// aliases gh lists per command, so `gh cs ls` and `gh pr ls` resolve to the command they
// are gh's own built-in names for. User aliases never resolve: the broker's gh reads no
// config.yml (docs/design/boundary-broker.md §4.1), and nothing here consults one.
var aliasIndex = buildAliasIndex()

func buildAliasIndex() map[string]string {
	idx := map[string]string{}
	// Group aliases first (single-word aliases of a top-level group), so the multi-word
	// aliases below can canonicalize their own first word.
	for path, c := range grammar {
		if strings.Contains(path, " ") {
			continue
		}
		for _, a := range c.aliases {
			if !strings.Contains(a, " ") {
				idx[" "+a] = path
			}
		}
	}
	for path, c := range grammar {
		want := strings.Fields(path)
		for _, a := range c.aliases {
			words := strings.Fields(a)
			if len(words) != len(want) {
				continue
			}
			// Canonicalize every word but the last through the group index.
			parent := ""
			ok := true
			for i, w := range words[:len(words)-1] {
				cw := w
				if canon, found := idx[parentKey(parent)+w]; found {
					cw = canon
				}
				if cw != want[i] {
					ok = false
					break
				}
				parent = join(parent, cw)
			}
			if ok {
				idx[parentKey(parent)+words[len(words)-1]] = want[len(want)-1]
			}
		}
	}
	return idx
}

func parentKey(parent string) string {
	if parent == "" {
		return " "
	}
	return parent + " "
}

func join(parent, word string) string {
	if parent == "" {
		return word
	}
	return parent + " " + word
}

// childPath resolves word under parent: the canonical child path when word is a built-in
// command name or a built-in alias of one, and "" otherwise. A word holding a space names no
// command, as gh reads it, though the table's space-joined keys would match it: `gh "pr view"`
// is gh's unknown command, never `gh pr view` (found by FuzzClassify).
func childPath(parent, word string) string {
	if word == "" || strings.ContainsFunc(word, unicode.IsSpace) {
		return ""
	}
	if p := join(parent, word); grammar[p] != nil {
		return p
	}
	if canon, ok := aliasIndex[parentKey(parent)+word]; ok {
		if p := join(parent, canon); grammar[p] != nil {
			return p
		}
	}
	return ""
}
