package cli

// usageexamples_test.go is the last clause of the standard's item 2
// (docs/design/self-documenting-cli.md): "Help lists synopsis, flags, positional
// args, effects, and ≥1 example". The first four were DONE, with
// TestUsageListsEveryParsedFlag deriving flag coverage from the handlers' own
// source. Examples were the remaining half, and only `run` and `pack` had one.
//
// This test derives them the same way — mechanically, from the registry — so a
// twenty-fifth command cannot arrive with help that only points onward.

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestEveryCommandShowsACopyableExample requires each registered command's help
// to carry at least one literal invocation that runs as written AND reaches that
// command.
//
// "Reaches that command" is decided by the PRODUCTION ROUTER, not by a string
// match on the command's name: each candidate line goes through RewriteArgv and
// Subcommand, the same two functions Main uses. That is what makes the test
// about the example rather than about its spelling. Three payoffs:
//
//   - `run`'s canonical form is `yolo -- <cmd>`, which never contains the word
//     "run". RewriteArgv inserts it, exactly as it would for a real invocation,
//     so the natural example passes and a contrived `yolo run …` is not forced.
//   - An example that would route somewhere ELSE fails. `yolo --network host --
//     bash` in host's help would resolve to `run`, and this catches that.
//   - An alias needs no special case. `doctor` shares checkUsage, so checkUsage
//     has to show `yolo doctor` too — which is the honest requirement, since
//     that is the name the reader arrived under.
func TestEveryCommandShowsACopyableExample(t *testing.T) {
	for _, sub := range slices.Sorted(maps.Keys(registry)) {
		t.Run(sub, func(t *testing.T) {
			spec, ok := subcommandUsage[sub]
			if !ok {
				t.Fatalf("%q has no usage text at all", sub)
			}
			block, found := exampleBlock(spec.text)
			if !found {
				t.Fatalf("`yolo %s --help` has no `Examples:` section. Every command "+
					"must show at least one invocation a reader can copy and run; "+
					"pointing onward to a sibling command or to `yolo config-ref` is "+
					"not an example.", sub)
			}
			var reached []string
			for _, line := range block {
				if routesTo(line) == sub {
					reached = append(reached, line)
				}
			}
			if len(reached) == 0 {
				t.Errorf("`yolo %s --help` has an Examples section, but none of its "+
					"lines is an invocation this CLI would route to %q:\n  %s",
					sub, sub, strings.Join(block, "\n  "))
			}
		})
	}
}

// exampleBlock returns the lines of a usage text's `Examples:` (or `Example:`)
// section — the indented run that follows the header, ending at the first blank
// line or unindented line.
func exampleBlock(text string) (lines []string, found bool) {
	in := false
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "Examples:" || trimmed == "Example:" {
			in = true
			found = true
			continue
		}
		if !in {
			continue
		}
		if trimmed == "" || !strings.HasPrefix(raw, " ") {
			break
		}
		lines = append(lines, trimmed)
	}
	return lines, found
}

// routesTo returns the registry key this CLI would dispatch the example line to,
// or "" when the line is not a `yolo` invocation at all (a shell wrapper like
// `eval "$(yolo host env)"` is legitimate in an Examples block, it just is not
// the one that satisfies the bar).
//
// The trailing `# …` comment is stripped, because a comment explaining what the
// line does is part of what makes an example readable, and shells drop it too.
func routesTo(line string) string {
	if i := strings.Index(line, " #"); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "yolo" {
		return ""
	}
	// The real router: `--`→run rewrite first, then leading-positional
	// resolution. Anything Main would refuse resolves to "" here too.
	return Subcommand(RewriteArgv(fields[1:]))
}

// TestRoutesToUsesTheRealRouter guards the test above from the failure mode it
// is most exposed to: a helper that reports the answer it was asked for.
//
// If routesTo degenerated into "does the line contain the command name", every
// case in TestEveryCommandShowsACopyableExample would keep passing while the
// property it checks — that the example reaches THIS command — stopped being
// checked at all. These are the discriminating cases.
func TestRoutesToUsesTheRealRouter(t *testing.T) {
	cases := map[string]string{
		"yolo ps --format json":            "ps",
		"yolo -- claude":                   "run", // the rewrite, not the word
		"yolo --new -- bash":               "run",
		"yolo host -- claude":              "host",
		"yolo host apply --assert":         "host",
		"yolo loopholes list":              "loopholes",
		"yolo doctor":                      "doctor",
		"yolo check --no-build":            "check",
		"yolo prune --apply  # do it":      "prune", // the comment is stripped
		`eval "$(yolo host env)"`:          "",      // a shell wrapper, not an invocation
		"yolo --network host -- bash":      "run",   // NOT host: --network's value is skipped
		"yolo definitely-not-a-subcommand": "",
	}
	for line, want := range cases {
		if got := routesTo(line); got != want {
			t.Errorf("routesTo(%q) = %q, want %q", line, got, want)
		}
	}
}
