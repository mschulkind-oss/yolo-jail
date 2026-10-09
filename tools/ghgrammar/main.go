// Command ghgrammar regenerates internal/ghbroker's gh flag grammar from the gh binary on
// PATH. It is run by hand when a gh release breaks the broker's grammar, never by a build:
//
//	go run ./tools/ghgrammar -out internal/ghbroker/grammar_gen.go
//
// The broker's classifier refuses any flag or command word it cannot name
// (docs/design/boundary-broker.md BB-P2, BB-D3), so it has to know every command path gh
// has and, for each flag, whether it takes a value. Both come from gh itself here, never
// from a hand-typed list:
//
//   - the command tree from `gh help reference`, and each leaf's usage line, flags,
//     inherited flags and aliases from its own `gh <path> --help` (the reference omits
//     inherited flags, so `-R` on `gh run view` would be missing);
//   - whether a flag takes a value is MEASURED, not read from the help text: the help
//     text's placeholder comes from backquoted words in the flag's description, so a
//     boolean such as `gh auth setup-git --force` prints one. Each flag is probed as
//     `gh <path> --<flag>` with no credential and an empty config: a value flag fails
//     parsing ("flag needs an argument", or the --json field list), and anything else got
//     past the parser.
//
// Nothing it runs reaches the network: every probe stops at argument parsing or at the
// "not logged in" check that precedes any request, in a config directory this program
// creates and removes.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type flagSpec struct {
	Short       string
	Long        string
	Placeholder string
	Desc        string
	Value       bool
	OptValue    bool
}

type command struct {
	Path    string
	Usage   string
	Flags   []flagSpec
	Aliases []string
}

var headingRE = regexp.MustCompile(`^#{2,4} gh (.*)$`)

// flagLineRE matches one pflag usage line: an optional `-x, ` short form, the long form,
// an optional placeholder, then two or more spaces and the description.
var flagLineRE = regexp.MustCompile(`^\s+(?:-([A-Za-z0-9]), )?--([A-Za-z0-9][A-Za-z0-9-]*)(?: (\S+))?(?:\s{2,}(.*))?$`)

func main() {
	out := flag.String("out", "", "file to write (default stdout)")
	ghBin := flag.String("gh", "gh", "the gh binary to read")
	flag.Parse()

	tmp, err := os.MkdirTemp("", "ghgrammar-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(tmp)
	env := probeEnv(tmp)

	version := strings.TrimSpace(firstLine(run(*ghBin, env, "--version")))
	ref := run(*ghBin, env, "help", "reference")
	paths := commandPaths(ref)
	var cmds []command
	for _, p := range paths {
		help := run(*ghBin, env, append(strings.Fields(p), "--help")...)
		c := parseHelp(p, help)
		for i := range c.Flags {
			f := &c.Flags[i]
			if strings.Contains(f.Placeholder, "[=") {
				f.OptValue = true
				continue
			}
			f.Value = probeTakesValue(*ghBin, env, p, f.Long)
		}
		cmds = append(cmds, c)
	}
	src := render(version, cmds)
	formatted, ferr := format.Source(src)
	if ferr != nil {
		fmt.Fprintln(os.Stderr, "gofmt:", ferr)
		formatted = src
	}
	if *out == "" {
		os.Stdout.Write(formatted)
		return
	}
	if err := os.WriteFile(*out, formatted, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ghgrammar:", err)
	os.Exit(1)
}

// probeEnv is an environment with no credential and a throwaway config, so a probe cannot
// authenticate and so cannot make a request.
func probeEnv(tmp string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv[:strings.IndexByte(kv, '=')]
		switch k {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
			"GH_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "GH_HOST", "GH_REPO":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GH_CONFIG_DIR="+filepath.Join(tmp, "config"),
		"XDG_CONFIG_HOME="+filepath.Join(tmp, "xdg-config"),
		"XDG_DATA_HOME="+filepath.Join(tmp, "xdg-data"),
		"GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "GH_PAGER=cat",
	)
}

func run(bin string, env []string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = os.TempDir()
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	_ = cmd.Run()
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// commandPaths lists every command path the reference has a heading for, in order.
func commandPaths(ref string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(ref, "\n") {
		m := headingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var words []string
		for _, w := range strings.Fields(m[1]) {
			if strings.ContainsAny(w[:1], "<[{-") {
				break
			}
			words = append(words, w)
		}
		p := strings.Join(words, " ")
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func parseHelp(path, help string) command {
	c := command{Path: path}
	section := ""
	sc := bufio.NewScanner(strings.NewReader(help))
	for sc.Scan() {
		line := sc.Text()
		if line != "" && !strings.HasPrefix(line, " ") {
			section = strings.TrimSpace(line)
			continue
		}
		switch section {
		case "USAGE":
			if t := strings.TrimSpace(line); t != "" && c.Usage == "" {
				c.Usage = t
			}
		case "FLAGS", "INHERITED FLAGS":
			m := flagLineRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			c.Flags = append(c.Flags, flagSpec{Short: m[1], Long: m[2], Placeholder: m[3], Desc: strings.TrimSpace(m[4])})
		case "ALIASES":
			for _, a := range strings.Split(line, ",") {
				if a = strings.TrimSpace(a); strings.HasPrefix(a, "gh ") {
					c.Aliases = append(c.Aliases, strings.TrimPrefix(a, "gh "))
				}
			}
		}
	}
	// `--help` is on every command and pflag lists it only sometimes.
	has := false
	for _, f := range c.Flags {
		if f.Long == "help" {
			has = true
		}
	}
	if !has {
		c.Flags = append(c.Flags, flagSpec{Long: "help", Desc: "Show help for command"})
	}
	sort.Slice(c.Flags, func(i, j int) bool { return c.Flags[i].Long < c.Flags[j].Long })
	return c
}

// probeTakesValue asks gh's own parser whether --long needs a value on path.
func probeTakesValue(bin string, env []string, path, long string) bool {
	if long == "help" {
		return false
	}
	out := run(bin, env, append(strings.Fields(path), "--"+long)...)
	return strings.Contains(out, "flag needs an argument") ||
		strings.Contains(out, "Specify one or more comma-separated fields")
}

func render(version string, cmds []command) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by tools/ghgrammar from %q; DO NOT EDIT.\n\n", version)
	b.WriteString("package ghbroker\n\n")
	b.WriteString("// grammarVersion is the `gh --version` first line this table was measured from.\n")
	fmt.Fprintf(&b, "const grammarVersion = %q\n\n", version)
	b.WriteString("// grammar is every gh command path with its usage line, its flags (local and\n")
	b.WriteString("// inherited) and whether each takes a value, measured from gh itself.\n")
	b.WriteString("var grammar = map[string]*ghCommand{\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "\t%q: {path: %q, usage: %q,", c.Path, c.Path, c.Usage)
		if len(c.Aliases) > 0 {
			fmt.Fprintf(&b, " aliases: %#v,", c.Aliases)
		}
		b.WriteString(" flags: []ghFlag{\n")
		for _, f := range c.Flags {
			fmt.Fprintf(&b, "\t\t{long: %q", f.Long)
			if f.Short != "" {
				fmt.Fprintf(&b, ", short: %q", f.Short)
			}
			if f.Value {
				b.WriteString(", value: true")
			}
			if f.OptValue {
				b.WriteString(", optValue: true")
			}
			if f.Placeholder != "" {
				fmt.Fprintf(&b, ", placeholder: %q", f.Placeholder)
			}
			fmt.Fprintf(&b, ", desc: %q},\n", f.Desc)
		}
		b.WriteString("\t}},\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}
