// Package patchrecovery is the one vocabulary of a PATCHED BUILD'S RECOVERY HATCHES
// (docs/reference/patched-forks.md, "Recovery hatches"): the environment variables a user puts in
// front of one command to get past a patched fork or patched extension that will not build, the
// grammar of their values, and the one shape every refusal lists them in.
//
// A hatch is for broken EXTERNAL state, never for a yolo bug: an upstream that moved incompatibly
// or broke its own build. Every hatch is opt-in, read by the act it is typed in front of, and
// never persisted or forwarded into a jail.
//
// THE VALUE GRAMMAR (Parse), shared by every hatch here: "1" covers every patched build the act
// selects; otherwise a comma-separated list of names, each matching a patched build by its key
// (`pi-fork/pi`), its pack (`pi-fork`) or its bin or extension name (`pi`). An empty value is unset.
//
// THE OUTPUT SHAPE (Section), ruled 2026-10-16: a blank line, a heading, then one copyable command
// per line with a few words of label. No justification prose.
package patchrecovery

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The hatches. AllowPatchFailures and AllowMissingPrograms predate this package and keep their
// spellings in paths; the rest are spelled here.
const (
	// AllowPatchFailuresEnv runs the intact admitted build of the series as it stands past a
	// recorded patch failure, for one run.
	AllowPatchFailuresEnv = paths.AllowPatchFailuresEnv
	// UseCachedGoodEnv runs the record's older good build (one whose series or recipe is no longer
	// the manifest's) for one fresh jail launch.
	UseCachedGoodEnv = "YOLO_USE_CACHED_GOOD"
	// AllowOlderUpstreamEnv is the WALK-BACK: when the newest upstream version does not take the
	// series or does not build, try older release tags, newest first, down to the series' base.
	AllowOlderUpstreamEnv = "YOLO_ALLOW_OLDER_UPSTREAM"
	// SkipPatchesEnv builds and runs the upstream itself, with no patch series applied.
	SkipPatchesEnv = "YOLO_SKIP_PATCHES"
	// SkipExtensionsEnv starts the owning agent without the named built extension: no build, no
	// mount, and its entry left out of the agent's extension list.
	SkipExtensionsEnv = "YOLO_SKIP_EXTENSIONS"
	// AllowMissingProgramsEnv starts the jail without a program it cannot deliver. Not scoped: any
	// non-empty value.
	AllowMissingProgramsEnv = paths.AllowMissingProgramsEnv
	// UseInstalledEnv runs the INSTALLED version of an unpatched program yolo manages (a launcher-
	// or floor-installed program, a yolo-installed MCP server, a pre-launch refresh's content) when
	// a newer version was found and installing it failed (docs/design/program-delivery.md PD-NF1,
	// PD-NF2). Scoped by Parse's grammar, a name matching the program's pack, bin or package.
	UseInstalledEnv = "YOLO_USE_INSTALLED"
)

// Scoped lists the patched-build hatches that take Parse's grammar, in the order a refusal lists
// them. UseInstalledEnv takes the grammar too, but it is never a patched build's hatch.
var Scoped = []string{AllowPatchFailuresEnv, UseCachedGoodEnv, AllowOlderUpstreamEnv, SkipPatchesEnv, SkipExtensionsEnv}

// WithoutScoped drops every Scoped hatch from environ ("NAME=value" items) and nothing else.
// The hatches are the OUTER launch's decisions about which builds to deliver; a build jail, which is
// a nested launch of its own, must never act on them (it has no host capture store, so
// YOLO_USE_CACHED_GOOD there refuses the jail itself).
func WithoutScoped(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, item := range environ {
		name, _, _ := strings.Cut(item, "=")
		if !slices.Contains(Scoped, name) {
			out = append(out, item)
		}
	}
	return out
}

// GetenvWithoutScoped wraps getenv (nil: this process's) so a Scoped hatch reads as unset.
func GetenvWithoutScoped(getenv func(string) string) func(string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	return func(name string) string {
		if slices.Contains(Scoped, name) {
			return ""
		}
		return getenv(name)
	}
}

// Scope is one hatch's parsed value.
type Scope struct {
	// Env is the variable it was read from, "" for a literal Parse.
	Env string
	all bool
	// names are the listed names, in the order written, deduplicated.
	names []string
}

// Parse reads value under the grammar above. It never fails: an empty name between commas is
// dropped, and a name that matches nothing is reported by Unmatched, not here.
func Parse(value string) Scope {
	value = strings.TrimSpace(value)
	if value == "1" {
		return Scope{all: true}
	}
	var s Scope
	for _, n := range strings.Split(value, ",") {
		n = strings.TrimSpace(n)
		if n != "" && !slices.Contains(s.names, n) {
			s.names = append(s.names, n)
		}
	}
	return s
}

// Read is Parse of env's value under getenv, nil getenv reading this process's environment.
func Read(getenv func(string) string, env string) Scope {
	if getenv == nil {
		getenv = os.Getenv
	}
	s := Parse(getenv(env))
	s.Env = env
	return s
}

// Set reports whether the hatch is set at all.
func (s Scope) Set() bool { return s.all || len(s.names) > 0 }

// All reports whether the value is "1".
func (s Scope) All() bool { return s.all }

// Names are the listed names, nil for "1" or unset.
func (s Scope) Names() []string { return append([]string(nil), s.names...) }

// Subject is what a scope name is matched against: one patched build.
type Subject struct {
	// Key is `<pack>/<bin>`, Pack the fork or extension pack, Name the bin or extension name.
	Key, Pack, Name string
}

// Covers reports whether s covers sub: "1", or a name equal to its key, pack or name.
func (s Scope) Covers(sub Subject) bool {
	if s.all {
		return true
	}
	for _, n := range s.names {
		if n == sub.Key || n == sub.Pack || n == sub.Name {
			return true
		}
	}
	return false
}

// Unmatched are the names of s that cover none of subs, in the order written: a typo, or a pack
// this workspace does not select. "1" has none.
func (s Scope) Unmatched(subs []Subject) []string {
	var out []string
	for _, n := range s.names {
		if !slices.ContainsFunc(subs, func(sub Subject) bool { return Scope{names: []string{n}}.Covers(sub) }) {
			out = append(out, n)
		}
	}
	return out
}

// UnmatchedLine is the one warning line for names of env that cover nothing this act selects, ""
// when there are none: the act goes on, since a hatch left in a shell profile must not stop a
// workspace that does not select the pack it names.
func UnmatchedLine(env string, unmatched []string, subs []Subject) string {
	if len(unmatched) == 0 {
		return ""
	}
	keys := make([]string, 0, len(subs))
	for _, sub := range subs {
		keys = append(keys, sub.Key)
	}
	slices.Sort(keys)
	known := "none here"
	if len(keys) > 0 {
		known = strings.Join(keys, ", ")
	}
	return fmt.Sprintf("Warning: %s names %s, which matches no patched build here (patched builds: %s)",
		env, strings.Join(unmatched, ", "), known)
}

// Hatch is one line of a refusal's list: Env=Value in front of Command, then Label.
type Hatch struct {
	Env, Value, Command string
	// Label is a few words: what the line gets ("run v1.1.0 + 2 patches"). No justification.
	Label string
}

// Text is the copyable command: `YOLO_X=value command`. Scoped hatches merge the requested name
// with that same variable's existing selection, so a one-off recovery does not discard other picks.
func (h Hatch) Text() string {
	cmd := h.Command
	if cmd == "" {
		cmd = "yolo"
	}
	value := h.Value
	if scopedHatch(h.Env) {
		if current, ok := os.LookupEnv(h.Env); ok {
			value = mergeScope(current, value)
		}
		value = shquote.Quote(value)
	}
	return h.Env + "=" + value + " " + cmd
}

func scopedHatch(env string) bool {
	return env == UseInstalledEnv || slices.Contains(Scoped, env)
}

func mergeScope(current, requested string) string {
	prior := Parse(current)
	if prior.All() {
		return "1"
	}
	addition := Parse(requested)
	if addition.All() {
		return "1"
	}
	names := prior.Names()
	for _, name := range addition.Names() {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, ",")
}

// StartAnyway is the heading the hatches stand under.
const StartAnyway = "Start anyway:"

// Lines is the hatch list as lines, without the heading: each command padded to one column, its
// label after it, the command in bold cyan and the label dim when color is on.
func Lines(hatches []Hatch, color bool) []string {
	width := 0
	for _, h := range hatches {
		width = max(width, len(h.Text()))
	}
	out := make([]string, 0, len(hatches))
	for _, h := range hatches {
		text := h.Text()
		pad := strings.Repeat(" ", width-len(text)+4)
		line := "  [bold cyan]" + richtext.Escape(text) + "[/bold cyan]"
		if h.Label != "" {
			line += pad + "[dim]" + richtext.Escape(h.Label) + "[/dim]"
		}
		out = append(out, richtext.Render(line, color))
	}
	return out
}

// Section is the hatch list as a block: a blank line, heading, then Lines; "" for no hatches.
func Section(heading string, hatches []Hatch, color bool) string {
	if len(hatches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + richtext.Render("[bold]"+richtext.Escape(heading)+"[/bold]", color) + "\n")
	for _, l := range Lines(hatches, color) {
		b.WriteString(l + "\n")
	}
	return b.String()
}
