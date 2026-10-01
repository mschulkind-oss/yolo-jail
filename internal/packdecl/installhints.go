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
// space-separated tokens spelled from the allowlist above, each one a package NAME rather
// than something a manager reads as an instruction. So none starts with "-" (an option, and
// yolo writes the install command's flags itself) or with "." (a path: apt and dnf install a
// `./<file>` as given), and none ends with "-" (apt's suffix for REMOVE: `apt install fd-find
// ufw-` takes ufw away). After it, if present, is the step: free shell, since the whole remedy
// is printed for the user to read and run, but non-empty, on one line, spelled in printable
// ASCII so the terminal shows exactly what the hint holds (firstUnprintable), and the only
// one — a second `&&` is refused.
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
		switch {
		case strings.HasPrefix(tok, "-"):
			return fmt.Sprintf("has %q in its package part, which starts with \"-\" and so would "+
				"reach the package manager as an option — name the packages alone (yolo writes "+
				"the install command and its flags; a Homebrew cask is the \"brew-cask\" key), and "+
				"put extra steps after a single %q", tok, hintStepSeparator)
		case strings.HasPrefix(tok, "."):
			return fmt.Sprintf("has %q in its package part, which starts with \".\" and so is a "+
				"path, never a package name (apt and dnf install a \"./<file>\" as given, and \".\" "+
				"or \"..\" is a directory) — name the packages alone, and put extra steps after a "+
				"single %q", tok, hintStepSeparator)
		case strings.HasSuffix(tok, "-"):
			return fmt.Sprintf("has %q in its package part, which ends with \"-\" — apt reads that "+
				"as \"remove this package\", not install it — name the packages alone, and put "+
				"extra steps after a single %q", tok, hintStepSeparator)
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
			"printed whole, on one line, for you to read and run; write the step on one line",
			hintStepSeparator)
	case firstUnprintable(step) >= 0:
		return fmt.Sprintf("has %q in its step after %q — the step is spelled in printable ASCII, "+
			"since the remedy is printed for you to read and run, and a control character, an "+
			"invisible or direction-changing one, or a lookalike letter can make what a terminal "+
			"shows differ from what runs; write the step in printable ASCII",
			string(firstUnprintable(step)), hintStepSeparator)
	case strings.Contains(step, "&&"):
		return fmt.Sprintf("chains a second \"&&\" — a hint takes ONE step after its packages "+
			"(\"<package> [<package>…] && <command>\"); write that step as one command, after "+
			"the first %q", hintStepSeparator)
	}
	return ""
}

// firstUnprintable returns the first rune of s that is not printable ASCII (a space through
// "~"), or -1 when there is none. That is the step's alphabet because everything outside it
// can show a reader a different command from the one the hint holds: a backspace overwrites
// what came before it, an escape sequence can conceal the rest of the line while a copy taken
// from the terminal still carries it, a direction override reorders it, a zero-width character
// or a tab hides what sits between, and a lookalike letter names a different program.
func firstUnprintable(s string) rune {
	for _, r := range s {
		if r < ' ' || r > '~' {
			return r
		}
	}
	return -1
}
