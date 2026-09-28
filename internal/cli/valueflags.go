package cli

// valueflags.go is the ONE reader for the launch front doors' value flags — `-p`/`--profile`,
// `--at`, `--network` and `--with-credentials` — at every notch (docs/plans/notch-convergence.md
// item 9, rows A1 and A2).
//
// # Why one reader
//
// Each parser used to spell these flags itself, and the copies disagreed about the same typo.
// `yolo --profile= -- claude` executed `--profile=` inside the jail (rc 127) while `yolo host
// --profile= -- claude` silently selected nothing; `yolo host -p=zai -- claude` was refused with
// the whole usage while the jail accepted it; `yolo -p -- claude` read the front door's injected
// `run` token as a profile name. The -p GRAMMAR was already one function (parseProfileValue,
// ES-D27); this is the same merge for the flag's SPELLING, so a value flag reads the same at
// `yolo [run]`, `yolo host --` and `yolo host env`.
//
// # The rule
//
// A value flag takes its value from the NEXT token (`-p zai`) or glued on with `=` (`-p=zai`,
// `--profile=zai`). The next token is the value whatever it looks like (`-p -h` is a profile
// named "-h", `--at -h` a notch named "-h"), with ONE exception: the `--` separator, which is never
// a value — it is where the wrapped program's argv begins, and reading it as a profile name is the
// defect row A2 recorded. A flag with no token after it, a flag followed by `--`, and a glued `=`
// with nothing after it are all MISSING values, and every notch refuses them the same way:
// "<flag> needs a value", exit 2.

import (
	"fmt"
	"strings"
)

// launchValueFlags is every value flag the launch front doors read, each with its spellings.
// It is the list valueTakingFlags (dispatch.go) and runHelpRequested's skip derive from, so a
// value flag added here is skipped by every subcommand scan at once.
//
// Each parser still NAMES the flags it takes at its readValueFlag call, as literals, because the
// host takes a subset and because the usage drift guards (TestRunUsageListsEveryRunFlag,
// TestUsageListsEveryParsedFlag) read a parser's own literals. What keeps those names and this
// list one set is TestEveryValueFlagAParserReadsIsSkipped, which reads every readValueFlag call
// in the package.
var launchValueFlags = [][]string{
	{"--profile", "-p"},
	{"--at"},
	{"--network"},
	{withCredentialsFlag},
}

// valueFlag is one value flag as read from argv.
type valueFlag struct {
	// name is the flag as typed, without any glued value: "-p" for both `-p zai` and `-p=zai`.
	name string
	// value is the flag's value; "" exactly when err is set.
	value string
	// last is the index of the last token the flag consumed: the flag's own index for a glued
	// or missing value, the next index for a separate one. A parser resumes at last+1.
	last int
	// err is "<flag> needs a value" for a missing or empty value, nil otherwise.
	err error
}

// readValueFlag reads args[i] as one of the value flag spellings in names, and reports false
// when args[i] is none of them. It is the one place a launch front door decides what a value
// flag's value is; see the file header for the rule.
func readValueFlag(args []string, i int, names ...string) (valueFlag, bool) {
	a := args[i]
	for _, name := range names {
		switch {
		case a == name:
			if i+1 >= len(args) || args[i+1] == "--" {
				return valueFlag{name: name, last: i, err: missingValue(name)}, true
			}
			return valueFlag{name: name, value: args[i+1], last: i + 1}, true
		case strings.HasPrefix(a, name+"="):
			v := a[len(name)+1:]
			if v == "" {
				return valueFlag{name: name, last: i, err: missingValue(name)}, true
			}
			return valueFlag{name: name, value: v, last: i}, true
		}
	}
	return valueFlag{}, false
}

// readAnyLaunchValueFlag is readValueFlag over every row of launchValueFlags.
func readAnyLaunchValueFlag(args []string, i int) (valueFlag, bool) {
	for _, names := range launchValueFlags {
		if f, ok := readValueFlag(args, i, names...); ok {
			return f, true
		}
	}
	return valueFlag{}, false
}

// missingValue is the one refusal for a value flag given no value, at every notch.
func missingValue(name string) error {
	return fmt.Errorf("%s needs a value", name)
}

// launchValueFlagNames is every spelling in launchValueFlags, flattened.
func launchValueFlagNames() []string {
	var out []string
	for _, names := range launchValueFlags {
		out = append(out, names...)
	}
	return out
}
