package packdecl

import (
	"fmt"
	"sort"
	"strings"
)

// hintStepSeparator is the one spelling that chains a step onto an install hint's packages:
// `<package> [<package>…] && <command>` (Contribution.InstallHints states the convention, the
// maintainer's ruling of 2026-10-01).
const hintStepSeparator = " && "

// hintPackageChars is the punctuation a hint's PACKAGE PART may hold besides ASCII letters,
// digits and the spaces between packages. It is an ALLOWLIST, sized to the spellings real
// managers use rather than to the characters a shell treats specially: `owner/tap/name` and
// `python@3.12` (Homebrew), `pkg:amd64` and `pkg=1.2` (apt), `python312Packages.pip` (nix),
// `gcc-c++` (dnf). Everything else — `;`, `|`, `&`, a backtick, `$`, parentheses, `<` `>`,
// quotes, globs, `~`, `#`, any other whitespace — is refused, so the package slot can carry
// no shell.
const hintPackageChars = "._+-@/:="

func isHintPackageChar(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') ||
		strings.ContainsRune(hintPackageChars, r)
}

// installHintsProblems validates every value of one contribution's install_hints, in sorted
// key order so the problem list is deterministic. Called for `program` and `requires`, the two
// kinds that read the field (DepRequirements).
func installHintsProblems(label string, c Contribution) []string {
	keys := make([]string, 0, len(c.InstallHints))
	for k := range c.InstallHints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var problems []string
	for _, mgr := range keys {
		if why := installHintProblem(c.InstallHints[mgr]); why != "" {
			problems = append(problems, fmt.Sprintf("%s: install_hints %q for bin %q %s",
				label, mgr, c.Bin, why))
		}
	}
	return problems
}

// installHintProblem returns why one hint value breaks the convention, or "". The phrase
// follows `install_hints "<key>" for bin "<bin>"` and ends with the next step.
//
// The value splits at its FIRST ` && `. Before it is the package part: one or more
// space-separated tokens spelled from the allowlist above, none starting with "-" (the
// package manager would read that as an option, and yolo writes the install command's flags
// itself). After it, if present, is the step: free shell, since the whole remedy is shown
// before anything runs, but non-empty, on one line, and the only one — a second `&&` is
// refused.
func installHintProblem(value string) string {
	pkgs, step, chained := strings.Cut(value, hintStepSeparator)
	// A trailing `&&` with nothing after it never matches the separator, which needs the
	// space on both sides. Read it as the empty step it is, so the message names the step
	// rather than the "&" the package-part check would find.
	if !chained {
		if trimmed := strings.TrimRight(value, " "); strings.HasSuffix(trimmed, " &&") {
			pkgs, chained = strings.TrimSuffix(trimmed, " &&"), true
		}
	}
	for _, r := range pkgs {
		if r != ' ' && !isHintPackageChar(r) {
			return fmt.Sprintf("has %q in its package part %q — the package part is plain "+
				"package names (letters, digits and %s, separated by spaces), so it can carry "+
				"no shell; put extra steps after a single %q: \"<package> && <command>\"",
				string(r), pkgs, strings.Join(strings.Split(hintPackageChars, ""), " "),
				hintStepSeparator)
		}
	}
	tokens := strings.Fields(pkgs)
	if len(tokens) == 0 {
		if chained {
			return fmt.Sprintf("names no package before %q — write \"<package> && <command>\"",
				hintStepSeparator)
		}
		return "names no package — give the package that provides it on this manager, or drop the key"
	}
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			return fmt.Sprintf("has %q in its package part, which starts with \"-\" and so would "+
				"reach the package manager as an option — name the packages alone (yolo writes "+
				"the install command and its flags; a Homebrew cask is the \"brew-cask\" key), and "+
				"put extra steps after a single %q", tok, hintStepSeparator)
		}
	}
	if !chained {
		return ""
	}
	switch {
	case strings.TrimSpace(step) == "":
		return fmt.Sprintf("ends at %q with no step after it — write \"<package> && <command>\", "+
			"or drop the %q", hintStepSeparator, hintStepSeparator)
	case strings.ContainsAny(step, "\r\n"):
		return fmt.Sprintf("has a step after %q that spans more than one line — the remedy is "+
			"printed whole, on one line, before it runs; write the step on one line",
			hintStepSeparator)
	case strings.Contains(step, "&&"):
		return fmt.Sprintf("chains a second \"&&\" — a hint takes ONE step after its packages "+
			"(\"<package> [<package>…] && <command>\"); write that step as one command, after "+
			"the first %q", hintStepSeparator)
	}
	return ""
}
