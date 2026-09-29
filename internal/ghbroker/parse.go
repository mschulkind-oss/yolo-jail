package ghbroker

import (
	"fmt"
	"strings"
)

// parse.go parses a jail's gh argv against the measured grammar, the way gh itself does
// (docs/design/boundary-broker.md §5.1 rule 1): flags anywhere, including before the
// subcommand; long and short forms; `=` and separate values; bundled short flags. What it
// cannot parse it refuses, and it never guesses: a flag the command's grammar does not
// name is an error, not a positional and not a pass-through (BB-P2).

// flagUse is one flag as the argv used it.
type flagUse struct {
	flag *ghFlag
	// value is the flag's value; hasValue says one was given (always, for a value flag;
	// only with `=`, for a boolean or an optional-value flag).
	value    string
	hasValue bool
}

// parsed is an argv resolved against one command.
type parsed struct {
	cmd         *ghCommand
	flags       []flagUse
	positionals []string
}

// refusal is a parse or policy verdict that the command never runs, with the reason the
// jail is told.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refusef(format string, args ...any) *refusal {
	return &refusal{msg: fmt.Sprintf(format, args...)}
}

// isFlagToken reports whether a token is spelled as a flag. A lone "-" is a positional
// (gh's "read standard input" spelling), and "--" ends the flags.
func isFlagToken(tok string) bool {
	return len(tok) > 1 && tok[0] == '-' && tok != "--"
}

// findPath walks argv for the command path: the leading non-flag words that each name a
// built-in command under the one before. Flags before or between the words are skipped;
// `-R`/`--repo` without `=` also skips its value, because that is the one flag an agent
// puts before the subcommand (`gh -R o/r pr view 1`, MEASURED to parse in gh). Every
// other flag's arity is checked against the found command in parseAgainst, which refuses
// a flag that would have swallowed a path word.
func findPath(argv []string) (path string, pathIdx map[int]bool) {
	pathIdx = map[int]bool{}
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" {
			break
		}
		if isFlagToken(tok) {
			if tok == "-R" || tok == "--repo" {
				i++
			}
			continue
		}
		next := childPath(path, tok)
		if next == "" {
			break
		}
		path = next
		pathIdx[i] = true
	}
	return path, pathIdx
}

// parseArgv resolves argv (everything after `gh`) to a command and its flag uses.
func parseArgv(argv []string) (*parsed, error) {
	path, pathIdx := findPath(argv)
	if path == "" {
		first := ""
		for _, tok := range argv {
			if !isFlagToken(tok) {
				first = tok
				break
			}
		}
		if first == "" {
			return nil, refusef("no gh command was given")
		}
		return nil, refusef("%q is not a gh command this broker knows (it runs gh's built-in "+
			"commands only; aliases and extensions never resolve)", first)
	}
	if isGroup(path) {
		return nil, refusef("`gh %s` names a group of commands, not one command; name the "+
			"subcommand", path)
	}
	p, err := parseAgainst(grammar[path], argv, pathIdx)
	if err != nil {
		// The command is still named, so a caller can say the more useful thing when the
		// command itself is refused whatever its flags (`gh -R x co 1`).
		return &parsed{cmd: grammar[path]}, err
	}
	return p, nil
}

func parseAgainst(cmd *ghCommand, argv []string, pathIdx map[int]bool) (*parsed, error) {
	p := &parsed{cmd: cmd}
	// takeValue returns the token after i as a flag's value, refusing when there is none or
	// when it is one of the path words findPath already claimed.
	takeValue := func(i int, spelled string) (string, error) {
		if i+1 >= len(argv) {
			return "", refusef("flag %s needs a value", spelled)
		}
		if pathIdx[i+1] {
			return "", refusef("flag %s before the subcommand would take %q as its value; put "+
				"flags after the command", spelled, argv[i+1])
		}
		return argv[i+1], nil
	}
	for i := 0; i < len(argv); i++ {
		if pathIdx[i] {
			continue
		}
		tok := argv[i]
		switch {
		case tok == "--":
			for _, rest := range argv[i+1:] {
				if strings.HasPrefix(rest, "-") && rest != "-" {
					return nil, refusef("positional argument %q begins with '-', which the "+
						"broker does not pass through", rest)
				}
				p.positionals = append(p.positionals, rest)
			}
			return p, nil
		case !isFlagToken(tok):
			p.positionals = append(p.positionals, tok)
		case strings.HasPrefix(tok, "--"):
			name, val, hasEq := strings.Cut(tok[2:], "=")
			f := cmd.flagByLong(name)
			if f == nil {
				return nil, unknownFlag(cmd, "--"+name)
			}
			u := flagUse{flag: f}
			switch {
			case f.value && hasEq:
				u.value, u.hasValue = val, true
			case f.value:
				v, err := takeValue(i, "--"+name)
				if err != nil {
					return nil, err
				}
				u.value, u.hasValue = v, true
				i++
			case hasEq:
				u.value, u.hasValue = val, true
			}
			p.flags = append(p.flags, u)
		default:
			// A bundle of short flags: `-cw`, `-L5`, `-R o/r`, `-R=o/r`.
			shorts := tok[1:]
			for j := 0; j < len(shorts); j++ {
				c := string(shorts[j])
				f := cmd.flagByShort(c)
				if f == nil {
					return nil, unknownFlag(cmd, "-"+c)
				}
				u := flagUse{flag: f}
				if f.value {
					rest := strings.TrimPrefix(shorts[j+1:], "=")
					if rest != "" {
						u.value, u.hasValue = rest, true
					} else {
						v, err := takeValue(i, "-"+c)
						if err != nil {
							return nil, err
						}
						u.value, u.hasValue = v, true
						i++
					}
					p.flags = append(p.flags, u)
					break
				}
				if j+1 < len(shorts) && shorts[j+1] == '=' {
					u.value, u.hasValue = shorts[j+2:], true
					p.flags = append(p.flags, u)
					break
				}
				p.flags = append(p.flags, u)
			}
		}
	}
	return p, nil
}

func unknownFlag(cmd *ghCommand, spelled string) error {
	return refusef("flag %s is not one `gh %s` has in the gh version this broker was built "+
		"against, and a flag the broker cannot name is refused", spelled, cmd.path)
}

// has reports whether the argv used the flag with this long name.
func (p *parsed) has(long string) bool {
	for _, u := range p.flags {
		if u.flag.long == long {
			return true
		}
	}
	return false
}

// values returns every value the argv gave the flag with this long name, in order.
func (p *parsed) values(long string) []string {
	var out []string
	for _, u := range p.flags {
		if u.flag.long == long && u.hasValue {
			out = append(out, u.value)
		}
	}
	return out
}

// canonical rebuilds the argv from the parse: the canonical command path, then every flag
// in the order given, in its long form with any value glued on with `=`, then the
// positionals. It is what the broker RUNS, which is the point: the jail's spelling never
// reaches gh, so a flag the parser read one way cannot be read another way by gh
// (docs/design/boundary-broker.md §4.1).
func (p *parsed) canonical() []string {
	out := strings.Fields(p.cmd.path)
	for _, u := range p.flags {
		if u.hasValue {
			out = append(out, "--"+u.flag.long+"="+u.value)
		} else {
			out = append(out, "--"+u.flag.long)
		}
	}
	return append(out, p.positionals...)
}
