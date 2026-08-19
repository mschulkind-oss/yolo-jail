package loopholes

// The `yolo loopholes {list,status,enable,disable}` command group. It inspects
// and toggles host-side loopholes. The discovery/doctor/set-enabled engines are
// alongside in this package; this is the thin command body behind injectable
// seams.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Deps are the injectable seams: Out/Err writers, the workspace cwd, and the
// in-jail flag (YOLO_VERSION set). LoadUserConfig / LoadWorkspaceConfig return
// the merged config maps (nil on any error).
type Deps struct {
	Out, Err            io.Writer
	Cwd                 string
	InJail              bool
	LoadUserConfig      func() *jsonx.OrderedMap
	LoadWorkspaceConfig func(cwd string) *jsonx.OrderedMap
}

// RealDeps returns Deps backed by the real filesystem/config loaders.
func RealDeps() Deps {
	cwd, _ := os.Getwd()
	return Deps{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Cwd:    cwd,
		InJail: os.Getenv("YOLO_VERSION") != "",
		LoadUserConfig: func() *jsonx.OrderedMap {
			// UserScopeConfig, not LoadJSONCFile: it carries the includes AND any
			// --user-layer, which is what lets an in-jail agent install a loophole and
			// see it in `yolo loopholes list` in the same invocation (OQ-LP9 R5).
			m, err := config.UserScopeConfig(false, nil)
			if err != nil {
				return nil
			}
			return m
		},
		LoadWorkspaceConfig: func(cwd string) *jsonx.OrderedMap {
			m, err := config.LoadWorkspaceConfig(cwd, false, nil)
			if err != nil {
				return nil
			}
			return m
		},
	}
}

// loopholesWithConfig discovers loopholes including host_services synthesized
// from the merged user+workspace config `loopholes:` block: user then
// workspace, later wins on key collision.
//
// Entries are VALIDATED before they are honored (loophole-packaging.md item
// 1b): these commands used to read the config with no schema pass at all, so a
// workspace entry `yolo check` rejects — e.g. a doctor_cmd with no command,
// host execution from two read-only-looking commands — was still listed, and
// Status executed it. An entry that fails validation is dropped with a printed
// reason and never reaches RunDoctorChecks. The scope rules follow the launch
// path's asymmetry: a workspace-scope violation refuses the entry on the host
// and is only a warning in-jail, where the entry stays honored.
// It returns a Set rather than a slice, so the doctor path downstream gets the ORIGIN
// GATE with the records (census site 5, docs/design/loophole-packaging.md §5.1): `status`
// executes each loophole's doctor_cmd, and this command has no pack resolution of its own
// — it reads what the process recorded through NewHostSet. On a `yolo loopholes` process
// nothing records anything, so a pack loophole is absent rather than executed, which is
// the fail-safe direction and is stated on packModules.
func loopholesWithConfig(deps Deps, includeDisabled bool) Set {
	// The same file-backed set config.ValidateConfig resolves names against.
	known, _ := NewResolver().Known()

	scopes := []struct {
		cfg           *jsonx.OrderedMap
		fromWorkspace bool
		src           string
	}{
		{deps.LoadUserConfig(), false, paths.UserConfigPath()},
		// deps.LoadWorkspaceConfig collapses yolo-jail.jsonc and
		// yolo-jail.local.jsonc, so the merged block cannot say which file an
		// entry came from and the refusal used to blame the tracked file for a
		// violation living in the local one. config.WorkspaceLoopholeOrigins
		// re-reads the two files (the same re-read the launch validator does) and
		// names the actual origin per entry; the collapsed map still supplies the
		// VALUES, so the injected-config seam these commands are tested through
		// keeps working — with no real files it simply finds no origins and falls
		// back to the tracked name.
		{deps.LoadWorkspaceConfig(deps.Cwd), true, filepath.Join(deps.Cwd, config.WorkspaceConfigName)},
	}
	wsOrigins := config.WorkspaceLoopholeOrigins(deps.Cwd)
	userInline := map[string]bool{}
	merged := jsonx.NewOrderedMap()
	for _, sc := range scopes {
		if sc.cfg == nil {
			continue
		}
		v, ok := sc.cfg.Get("loopholes")
		if !ok {
			continue
		}
		lh, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		for _, k := range lh.Keys() {
			val, _ := lh.Get(k)
			var info *config.LoopholeInfo
			if ki, isKnown := known[k]; isKnown {
				kiCopy := ki
				info = &kiCopy
			}
			src := sc.src
			if sc.fromWorkspace {
				if files := wsOrigins[k]; len(files) > 0 {
					// "A or B" when both files contributed: both are
					// agent-editable, and which key came from which is the
					// per-file question only the launch validator answers.
					src = strings.Join(files, " or ")
				}
			}
			problems := config.LoopholeEntryErrors(k, val, info, userInline[k],
				sc.fromWorkspace, deps.InJail, src, deps.Cwd)
			if len(problems) > 0 {
				fmt.Fprintf(deps.Err, "Ignoring loopholes.%s (from %s):\n", k, src)
				for _, p := range problems {
					fmt.Fprintf(deps.Err, "  • %s\n", p)
				}
				continue
			}
			if !sc.fromWorkspace {
				if m, isMap := val.(*jsonx.OrderedMap); isMap {
					if _, hasCmd := m.Get("command"); hasCmd {
						userInline[k] = true
					}
				}
			}
			merged.Set(k, val)
		}
	}
	// NewHostSet, not a hand-built DiscoverOptions: it is the one constructor that
	// composes bundled + pack + user + config, so this command cannot come to disagree
	// with the launch path about what this machine has. It always builds the
	// include-disabled superset; includeDisabled selects the VIEW below.
	set := NewHostSet(merged)
	if includeDisabled {
		return set
	}
	return SetOf(set.Enabled()).withGate(set)
}

// List runs `yolo loopholes list`.
func List(deps Deps) int {
	all := loopholesWithConfig(deps, true).All()
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		// TWO SOURCES, not three. There used to be a `bundled:` line naming
		// BundledLoopholesDir(), and dropping it is the point rather than a trim: the
		// empty-list message is the one surface that tells a user WHERE a loophole
		// could come from, and naming a channel that no longer exists would send them
		// to look in a directory yolo does not read (docs/design/broker-as-a-pack.md
		// OQ-BP4). Every loophole yolo ships is a pack's now, so `packs:` is the
		// answer to "why is this list empty".
		fmt.Fprintf(deps.Out, "  • pack: a `loophole` contribution from a selected pack; "+
			"%s is selected implicitly when it exists\n", paths.LocalPackDir())
		fmt.Fprintf(deps.Out, "  • config: loopholes: block in %s "+
			"(install-shaped keys are user-scope only; a workspace "+
			"yolo-jail.jsonc may set enabled/jail_env)\n", paths.UserConfigPath())
		return 0
	}
	for _, lh := range all {
		var label string
		switch {
		case !lh.Enabled:
			label = "disabled"
		case lh.Superseded():
			// The SHORT label, with the who and the why on their own continuation lines
			// below. The full sentence names a pack, a capability and a free-text reason,
			// which would blow the %-36s column and push every other line's name out of
			// alignment — and the reason is the part a reader most needs to be able to
			// read, so it gets a line of its own rather than a truncated column.
			label = "inactive (superseded)"
		default:
			if reason, ok := lh.InactiveReason(); ok {
				label = "inactive (" + reason + ")"
			} else {
				label = "active"
			}
		}
		// Interception is a property of the intercept list, not of the transport
		// string — see RuntimeArgsFor. The `transport=` fallback still prints for
		// every non-intercepting loophole, which is what makes the active transport
		// visible without asking (loophole-transport.md OQ-T2).
		var extra string
		if len(lh.Intercepts) > 0 {
			hosts := make([]string, len(lh.Intercepts))
			for i, ic := range lh.Intercepts {
				hosts[i] = ic.Host
			}
			extra = "intercepts=[" + strings.Join(hosts, ", ") + "]"
		} else {
			extra = "transport=" + lh.Transport
		}
		tags := lh.Source + "/" + lh.Transport + "/" + lh.Lifecycle
		fmt.Fprintf(deps.Out, "  %-36s  %s  (%s)  %s\n", label, lh.Name, tags, extra)
		if lh.Description != "" {
			fmt.Fprintf(deps.Out, "      %s\n", lh.Description)
		}
		// ANYTHING THAT TURNS SOMETHING OFF MUST NAME WHO DID IT AND WHY
		// (docs/design/pack-capabilities.md §5). An unexplained disappearance is the
		// failure mode the whole mechanism exists to avoid, and `loopholes list` is the
		// command a user runs to find out what happened — so the pack, the capability and
		// the pack author's own `because` are printed here, one line per claim.
		for _, s := range lh.SupersededBy {
			fmt.Fprintf(deps.Out, "      %s\n", s.Line())
		}
		// THE SETTINGS DECLARATIONS ARE PRINTED HERE BECAUSE THERE IS NOWHERE ELSE
		// LEFT (docs/design/pack-config-keys.md). `yolo config-ref` is generated from
		// core's own schema, and the entire point of this mechanism is that these keys
		// are NOT in core's schema — a pack declares them. So a user who cannot see
		// them here can only discover a key by guessing it wrong and reading the
		// validation error, which is a poor substitute for a list.
		//
		// One line per key, carrying the three facts a config author needs and no
		// others: the type (what to write), the scope (WHICH FILE may write it, which
		// is the half that is refused rather than ignored), and the default (what
		// happens if you write nothing). The description trails, because it is the
		// only free-text field and the only one that can be long.
		for _, st := range lh.Settings {
			fmt.Fprintf(deps.Out, "      settings.%s: %s, %s-scope, default %s%s\n",
				st.Key, st.Type, st.Scope, settingValueRepr(st.Default),
				descriptionSuffix(st.Description))
		}
	}
	// A claim that matched no served capability is NOT reprinted here: Discover already
	// warned it to stderr while applying the claims, which covers this command and every
	// other discovery surface at once. Reprinting would put the same fact on stderr twice
	// for one `loopholes list`, the same objection gateAdmitsCrossing makes about its own
	// silent branch. Set.SupersessionProblems() is the value-shaped seam for a surface
	// that wants to render it differently.
	return 0
}

// Status runs `yolo loopholes status` (each loophole's doctor_cmd), including
// the in-jail short-circuit.
func Status(deps Deps) int {
	if deps.InJail {
		fmt.Fprintln(deps.Out, "Inside jail — doctor checks are host-side.  From the host: yolo loopholes status")
		return 0
	}
	set := loopholesWithConfig(deps, true)
	all := set.All()
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		return 0
	}
	// THE SET's doctor runner, not the package-level one. `status` runs each loophole's
	// doctor_cmd — host code — and this command is one users treat as read-only preflight,
	// so a pack-shipped record runs only when the origin gate was evaluated AND passed
	// (docs/design/loophole-packaging.md §5.1). A withheld one is REPORTED, with the
	// reason, rather than skipped: a skip is indistinguishable from `no-check`, which
	// would read as "this loophole declares no self-check" — the wrong story entirely.
	for _, r := range set.RunDoctorChecks(all, 10*time.Second) {
		var prefix string
		switch {
		case !r.Loophole.Enabled:
			prefix = "disabled"
		// Between `disabled` and `unapproved`, mirroring Active()/InactiveReason(): a
		// superseded loophole is off for a reason the user's own pack selection chose,
		// which outranks every machine fact below it. Reporting it as `inactive` would
		// send the reader after an unmet requirement that is not why it is off.
		case r.Loophole.Superseded():
			prefix = "superseded"
		case !set.MayRunHostCode(r.Loophole):
			prefix = "unapproved"
		case !r.Loophole.RequirementsMet():
			prefix = "inactive"
		case r.RC != nil && *r.RC == 0:
			prefix = "ok"
		case r.RC == nil:
			prefix = "no-check"
		default:
			prefix = "fail"
		}
		fmt.Fprintf(deps.Out, "  [%s] %s  rc=%s\n", prefix, r.Loophole.Name, rcStr(r.RC))
		// The who and the why, for the same reason `loopholes list` carries them: a
		// loophole a pack turned off must never be an unexplained absence, and `status` is
		// the other command a user reaches for when one is not working.
		for _, s := range r.Loophole.SupersededBy {
			fmt.Fprintf(deps.Out, "      %s\n", s.Line())
		}
		if r.Output != "" {
			for _, line := range strings.Split(r.Output, "\n") {
				fmt.Fprintf(deps.Out, "      %s\n", line)
			}
		}
	}
	return 0
}

// CmdSetEnabled runs `yolo loopholes enable|disable <name>`. It TOGGLES NOTHING
// today: it prints the config key to write, names the file to write it in, and
// exits 1.
//
// THAT IS A DELIBERATE INTERIM STATE, not a half-finished edit, and here is the
// whole of why. The command only ever had one mechanism: rewrite `enabled` in a
// manifest under the hand-placed user loopholes directory, refusing every other
// source. That directory is retired (retired.go, OQ-LP10), so the mechanism has
// nothing left to write to — and OQ-LP10's second payoff is exactly this: with the
// special case gone, enable/disable state belongs in CONFIG, for every source
// (docs/design/loophole-packaging.md §5.2, which already calls that the better end
// state).
//
// Writing it is a separate change because it is a separate DECISION, not more typing.
// `loopholes.<name>.enabled` lives in ~/.config/yolo-jail/config.jsonc, a hand-written
// commented file that nothing in yolo writes today; a read-modify-write through
// json5 → jsonx.DumpsIndent drops every comment in it (the degradation SetEnabled used
// to accept for a yolo-generated manifest, which is a very different file). And the
// obvious dodge — a conventionally-named auto-merged state file beside it — is
// WITHDRAWN WITH CAUSE in this codebase already (internal/config/userlayer.go's header:
// it activates because a file exists, invisibly at the call site). So the honest
// interim is a command that tells you precisely what to write, rather than one that
// silently does nothing or quietly reformats your config.
//
// The instruction points at the USER config (loophole-packaging.md §5.2): it used to
// direct people at the workspace yolo-jail.jsonc — the weaker, agent-editable scope,
// and the one whose install-shaped keys are now refused. `enabled` is honored from
// either scope, but the command should never steer a human toward the file this
// design distrusts.
func CmdSetEnabled(deps Deps, name string, enabled bool) int {
	fmt.Fprintf(deps.Err,
		"yolo loopholes %s cannot write this yet — set it in config instead.\n"+
			"In %s add:\n"+
			"  \"loopholes\": { %q: { \"enabled\": %t } }\n"+
			"That key works for every source (bundled, pack-shipped, config-inline); "+
			"it is also honored from a workspace yolo-jail.jsonc, which is the "+
			"agent-editable scope and therefore the weaker place to put it.\n",
		verbFor(enabled), paths.UserConfigPath(), name, enabled)
	return 1
}

// verbFor names the subcommand the user actually typed, so the refusal echoes their
// own word back rather than a generic one.
func verbFor(enabled bool) string {
	if enabled {
		return "enable"
	}
	return "disable"
}

// rcStr renders an *int rc as the int, or "None" when nil.
func rcStr(rc *int) string {
	if rc == nil {
		return "None"
	}
	return fmt.Sprintf("%d", *rc)
}

// settingValueRepr renders a declared default for `loopholes list`, in the JSON
// spelling a user would type into their config — `[]` and not `[]string{}`, `""` and
// not the empty output an unquoted string gives. The listing is read by someone about
// to write the value, so it has to be in the language they will write it in.
func settingValueRepr(v any) string {
	out, err := jsonx.DumpsCompact(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return out
}

// descriptionSuffix appends a setting's description, or nothing. Separated so the
// format string above stays one line and the empty case cannot leave a dangling
// separator.
func descriptionSuffix(description string) string {
	if description == "" {
		return ""
	}
	return " — " + description
}
