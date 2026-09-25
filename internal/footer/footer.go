// Package footer renders the YOLO SEGMENT of an agent's footer: the text yolo adds to the
// line an agent draws under its input box, stating what the session is billed through and
// where the agent runs. It is `yolo internal footer`, the one renderer
// docs/design/agent-footer.md §2 rules in, and every agent pack's adapter calls it rather
// than carrying a script of its own.
//
// Terms, as that design coins them: the FOOTER is the agent's line; the YOLO SEGMENT is the
// text yolo adds to it; the BILLING ROUTE is what the session is billed through, in plain
// words (the agent's own login, or a provider); the NOTCH is one setting of the confinement
// dial — `jail` or `host` here, since `guest` is unbuilt and nothing may print it yet.
//
// # Core keeps no agent list
//
// Everything agent-specific arrives as an argument from the pack's own command string: the
// agent's name, its provider switches and how it tests them, the plain words for its login
// and for each provider its profiles select, which routes are bridged, and the template.
// Core knows no agent, no provider display name (no provider declaration carries one,
// §1.1) and no agent's stdin schema — a template names paths into whatever JSON the agent
// pipes in.
//
// # Arguments
//
// EVERY flag takes exactly one value, as `--name value` or `--name=value`, and an unknown
// flag is skipped together with its value. That is what keeps a command frozen into an
// edited-in-place file working across releases (§3): an older command meeting a newer
// renderer, or a newer one an older renderer, loses a flag rather than the footer.
//
//	--agent NAME         the key into YOLO_USE_PROFILES
//	--login WORDS        the billing route when no profile and no switch applies
//	--switch VAR=ID      an agent's own provider selector; repeatable, first that is on wins
//	--truthy V1,V2,...   the values a switch counts as on (trimmed, case-insensitive);
//	                     absent, any non-empty value is on
//	--words ID=WORDS     the plain words for a provider id; repeatable
//	--bridged NAME       a profile name or provider id this agent reaches through the wire
//	                     bridge; repeatable
//	--template TEXT      the line; `{yolo.billing}`, `{yolo.notch}`, `{stdin.a.b}`
//
// # What it never does
//
// It never exits non-zero, never writes to stderr, and never prints a credential value: a
// switch's value is compared and never printed, and no template field names an env var. In
// a jail it reads no file and makes no network call — only its own env and, when the
// template asks for a `{stdin.…}` field, the agent's JSON on stdin.
//
// # At the host
//
// No host launch exports the profile tables, so at the host notch "env first" would find
// nothing and every footer would name the agent's login. OQ-FT6 rules the one exception to
// reading no file: when this process is not in a jail and its env carries no
// YOLO_USE_PROFILES, the tables come from the caller's HostTables, which composes them from
// the user config the way `yolo host env` resolves a profile. This package does not compose
// them itself because that composition lives in internal/cli, which imports this one.
package footer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// DefaultTemplate is the line a command that passes no --template prints: the segment
// alone, in the shape every example in the design takes.
const DefaultTemplate = "yolo: {yolo.billing} · {yolo.notch}"

// The two notches this renderer can name. `guest` is absent on purpose: the notch is
// unbuilt, and until its launcher sets a marker a guest session is indistinguishable from
// the host (§1.2), which is the under-claim the design chose.
const (
	NotchJail = "jail"
	NotchHost = "host"
)

// stdinLimit and stdinTimeout bound the one read this command makes that another process
// controls. An agent that pipes its status JSON writes it and closes the pipe at once; one
// that never closes it (or a stdin inherited from a terminal session) must cost the footer a
// field, never the line.
const (
	stdinLimit   = 1 << 20
	stdinTimeout = 300 * time.Millisecond
)

// Switch is one agent provider selector: an env var the agent itself reads to pick a
// provider, and the provider id it picks.
type Switch struct {
	Var      string
	Provider string
}

// Options is a parsed command line.
type Options struct {
	Agent    string
	Login    string
	Switches []Switch
	// Truthy is the lowercased set of values a switch counts as on. nil means a switch is on
	// whenever it is set non-empty.
	Truthy   []string
	Words    map[string]string
	Bridged  map[string]bool
	Template string
}

// ParseArgs reads the flags above. It never fails: a malformed or unknown flag is skipped.
func ParseArgs(args []string) Options {
	o := Options{Words: map[string]string{}, Bridged: map[string]bool{}, Template: DefaultTemplate}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue // a positional; this command takes none
		}
		name, value, inline := strings.Cut(a[2:], "=")
		if !inline {
			if i+1 >= len(args) {
				break
			}
			i++
			value = args[i]
		}
		switch name {
		case "agent":
			o.Agent = value
		case "login":
			o.Login = value
		case "switch":
			v, id, ok := strings.Cut(value, "=")
			if ok && v != "" && id != "" {
				o.Switches = append(o.Switches, Switch{Var: v, Provider: id})
			}
		case "truthy":
			o.Truthy = []string{}
			for _, t := range strings.Split(value, ",") {
				if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
					o.Truthy = append(o.Truthy, t)
				}
			}
		case "words":
			id, words, ok := strings.Cut(value, "=")
			if ok && id != "" {
				o.Words[id] = words
			}
		case "bridged":
			if value != "" {
				o.Bridged[value] = true
			}
		case "template":
			o.Template = value
		}
	}
	return o
}

// Notch is where the agent runs, asked of config.InJail — the one answer to "am I in a
// jail?" — and never of a copy of its test (the copies already disagree: §1.2). An absent
// or empty marker reads as host, deliberately: a missing marker can only under-claim
// confinement.
func Notch() string {
	if config.InJail() {
		return NotchJail
	}
	return NotchHost
}

// Billing is the billing route in the pack's plain words, by the rule of §1.1, in order:
//
//  1. A profile is selected for the agent: that profile's provider, resolved the way the
//     launch resolved it (packload.ProviderFor over YOLO_PROFILES). A bridged route shows
//     the profile and "(bridge)", never an upstream; a provider the launch does not know
//     shows the profile name alone; a profile named unlike its provider follows the words
//     in parentheses. If a switch names a different provider, "≠ <it> (env)" follows.
//  2. No profile, but a switch is on: that provider, marked "(env)" — set outside yolo's
//     profiles.
//  3. Neither: the pack's words for the agent's own login, never guessed.
//
// getenv is the agent's environment. Absent or malformed tables read as empty.
func Billing(o Options, getenv func(string) string) string {
	tables := &entrypoint.Env{Vars: map[string]string{
		useProfilesVar: getenv(useProfilesVar),
		profilesVar:    getenv(profilesVar),
		providersVar:   getenv(providersVar),
	}}
	sw, switched := o.firstSwitchOn(getenv)

	profile := ""
	if o.Agent != "" {
		profile = packload.ProfileTable(tables.LoadUseProfiles())[o.Agent]
	}
	if profile == "" {
		if switched {
			return o.words(sw.Provider) + " (env)"
		}
		return o.Login
	}

	provider := packload.ProviderFor(tables.LoadProfiles(), profile)
	known := false
	if provider != "" {
		_, known = tables.LoadProviders().Get(provider)
	}
	var route string
	switch {
	case o.Bridged[profile] || (provider != "" && o.Bridged[provider]):
		route = profile + " (bridge)"
	case !known:
		route = profile
	case profile == provider:
		route = o.words(provider)
	default:
		route = o.words(provider) + " (profile " + profile + ")"
	}
	if switched && sw.Provider != provider {
		route += " ≠ " + o.words(sw.Provider) + " (env)"
	}
	return route
}

// firstSwitchOn returns the first switch, in the pack's order, that the agent would count
// as on. The value is compared and nothing else: it never reaches the output.
func (o Options) firstSwitchOn(getenv func(string) string) (Switch, bool) {
	for _, s := range o.Switches {
		v := getenv(s.Var)
		if v == "" {
			continue
		}
		if o.Truthy == nil {
			return s, true
		}
		norm := strings.ToLower(strings.TrimSpace(v))
		for _, t := range o.Truthy {
			if norm == t {
				return s, true
			}
		}
	}
	return Switch{}, false
}

// words is a provider's plain words, or its id when the pack gave none — a provider added
// after a command was frozen into a file shows its id rather than nothing (§3).
func (o Options) words(id string) string {
	if w := o.Words[id]; w != "" {
		return w
	}
	return id
}

// Render expands the template into the one line the agent shows.
//
// A placeholder is `{name}`: `yolo.billing` and `yolo.notch` are this package's facts, and
// `stdin.<path>` is a dotted path into the agent's JSON (an all-digit segment indexes an
// array). A missing field, an unknown name, an object or an array renders empty. An
// unclosed `{` is dropped, so no literal `{…}` reaches the screen. Every substituted value
// loses its control characters, and the whole line its line breaks, so neither the agent's
// data nor a template can split the footer or smuggle a terminal escape through a value.
//
// stdin is called at most once, and only if the template names a stdin field.
func Render(o Options, getenv func(string) string, stdin func() any) string {
	var doc any
	read := false
	field := func(name string) string {
		switch {
		case name == "yolo.billing":
			return Billing(o, getenv)
		case name == "yolo.notch":
			return Notch()
		case name == "stdin" || strings.HasPrefix(name, "stdin."):
			if !read {
				read = true
				if stdin != nil {
					doc = stdin()
				}
			}
			return lookup(doc, strings.TrimPrefix(strings.TrimPrefix(name, "stdin"), "."))
		}
		return ""
	}

	var b strings.Builder
	t := o.Template
	for len(t) > 0 {
		open := strings.IndexByte(t, '{')
		if open < 0 {
			b.WriteString(t)
			break
		}
		b.WriteString(t[:open])
		rest := t[open+1:]
		end := strings.IndexAny(rest, "{}")
		if end < 0 || rest[end] == '{' {
			// Unclosed, or another `{` first: this brace opens nothing.
			t = rest
			continue
		}
		b.WriteString(clean(field(strings.TrimSpace(rest[:end]))))
		t = rest[end+1:]
	}
	line := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(b.String())
	return strings.TrimSpace(line)
}

// lookup walks a dotted path into decoded JSON and renders a scalar leaf.
func lookup(doc any, path string) string {
	cur := doc
	if path != "" {
		for _, seg := range strings.Split(path, ".") {
			switch v := cur.(type) {
			case map[string]any:
				next, ok := v[seg]
				if !ok {
					return ""
				}
				cur = next
			case []any:
				n, err := strconv.Atoi(seg)
				if err != nil || n < 0 || n >= len(v) {
					return ""
				}
				cur = v[n]
			default:
				return ""
			}
		}
	}
	switch v := cur.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// clean makes a substituted value safe to put on one terminal line: invalid UTF-8 is
// dropped and every control character (C0, DEL, C1) becomes a space.
func clean(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// ReadJSON is the stdin reader Main hands Render: the agent's JSON, decoded with numbers
// kept as written, or nil for a terminal, an empty or malformed stream, one over the size
// bound, or one that has not closed within the time bound.
func ReadJSON(r io.Reader) any {
	if r == nil {
		return nil
	}
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return nil
		}
	}
	got := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(r, stdinLimit+1))
		got <- data
	}()
	var data []byte
	select {
	case data = <-got:
	case <-time.After(stdinTimeout):
		return nil
	}
	if len(data) > stdinLimit {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if dec.Decode(&doc) != nil {
		return nil
	}
	return doc
}

// The three wire tables the billing route reads, by their env names.
const (
	useProfilesVar = "YOLO_USE_PROFILES"
	profilesVar    = "YOLO_PROFILES"
	providersVar   = "YOLO_PROVIDERS"
)

// Tables are the three wire tables as the JSON text a launch puts in the env: the
// selection (YOLO_USE_PROFILES), the resolved profiles (YOLO_PROFILES) and the composed
// providers (YOLO_PROVIDERS). An empty field reads as an absent table.
type Tables struct {
	UseProfiles, Profiles, Providers string
}

// HostTables composes the tables at the host notch (OQ-FT6). It must not write to stderr,
// since everything this command prints lands in the agent's footer or nowhere.
type HostTables func() Tables

// WithHostTables is the agent's env with the three tables answered by host when this
// process is at the host notch and its own env carries no selection: OQ-FT6's "env first,
// else the profile selection from the user config file". In a jail, or when the env names a
// selection, or with a nil host, it is getenv itself, so the jail path still reads no file.
//
// host is asked at most once, and only when something reads a table (the billing route is
// the one reader, so a template without `{yolo.billing}` never asks). A panic in it reads as
// empty tables: the footer then names the login, which under-claims rather than failing.
func WithHostTables(getenv func(string) string, host HostTables) func(string) string {
	if host == nil || config.InJail() || getenv(useProfilesVar) != "" {
		return getenv
	}
	var tables *Tables
	load := func() Tables {
		if tables == nil {
			t := Tables{}
			func() {
				defer func() { _ = recover() }()
				t = host()
			}()
			tables = &t
		}
		return *tables
	}
	return func(name string) string {
		switch name {
		case useProfilesVar:
			return load().UseProfiles
		case profilesVar:
			return load().Profiles
		case providersVar:
			return load().Providers
		}
		return getenv(name)
	}
}

// Main is `yolo internal footer`. It prints the rendered line (nothing when it is empty)
// and returns 0 whatever happened, a panic included: a footer that errors costs the agent
// its status row, which is worse than a footer that says nothing.
//
// host supplies the tables at the host notch (WithHostTables); nil reads the env alone.
func Main(args []string, stdin io.Reader, stdout io.Writer, host HostTables) (code int) {
	defer func() {
		_ = recover()
		code = 0
	}()
	o := ParseArgs(args)
	line := Render(o, WithHostTables(os.Getenv, host), func() any { return ReadJSON(stdin) })
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	return 0
}
