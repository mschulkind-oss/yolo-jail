package loopholes

// The `yolo loopholes {list,status,enable,disable}` command group. It inspects
// and toggles host-side loopholes. The discovery/doctor/set-enabled engines are
// alongside in this package; this is the thin command body behind injectable
// seams.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
	// Format is the output format: "" / outfmt.Text (the human report,
	// unchanged) or outfmt.JSON. The CLI front door resolves the flag family;
	// see jsonreport.go for the documents.
	//
	// The VALIDATION warnings loopholesWithConfig writes to Err are unaffected by
	// it, deliberately: a rejected config entry is a diagnostic about the config,
	// not part of the report, and it belongs on stderr in both forms.
	Format string
	// Color is whether Out receives ANSI color, decided by the caller through the one color
	// gate (tty.Color: requested, a terminal, and NO_COLOR unset; docs/reference/cli-color.md).
	// False is the plain report, the same text with the color left out. RealDeps leaves it
	// false, since this package does not probe the terminal; the CLI front door sets it.
	Color bool
	// LoadWorkspaceFile reads the per-workspace file for a workspace (config.ReadWorkspaceFile),
	// the scope these commands read LAST, as the launch merges it; nil reads none.
	LoadWorkspaceFile func(cwd string) *config.WorkspaceFile
	// LaunchConfig is the config the host launched this jail with (config.JailLaunchConfig), the
	// one place in a jail a brokered loophole's switch can be read from; nil reads none.
	LaunchConfig func(cwd string) (*jsonx.OrderedMap, bool)
	// HostDir is this jail's workspace as the host names it (YOLO_HOST_DIR), for the next step
	// an in-jail `enable` names; "" on the host or when unknown.
	HostDir string
	// WriteSwitch writes one switch into a workspace's per-workspace file and returns the file
	// (config.SetWorkspaceLoophole).
	WriteSwitch func(workspace, name string, enabled bool) (string, error)
	// RemoveSwitch takes one switch out of a workspace's per-workspace file, returning the file
	// and whether it held the switch (config.RemoveWorkspaceLoophole): `disable` of a loophole
	// no longer installed.
	RemoveSwitch func(workspace, name string) (string, bool, error)
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
		LoadWorkspaceFile: config.ReadWorkspaceFile,
		LaunchConfig:      config.JailLaunchConfig,
		HostDir:           os.Getenv("YOLO_HOST_DIR"),
		WriteSwitch:       config.SetWorkspaceLoophole,
		RemoveSwitch:      config.RemoveWorkspaceLoophole,
	}
}

// loopholesWithConfig discovers loopholes including host_services synthesized
// from the merged user+workspace config `loopholes:` block: user then
// workspace, later wins on key collision.
//
// Entries are VALIDATED before they are honored (docs/reference/loophole-system.md#trust-what-is-gated-and-what-is-not):
// these commands used to read the config with no schema pass at all, so a
// workspace entry `yolo check` rejects — e.g. a doctor_cmd with no command,
// host execution from two read-only-looking commands — was still listed, and
// Status executed it. An entry that fails validation is dropped with a printed
// reason and never reaches RunDoctorChecks. The scope rules follow the launch
// path's asymmetry: a workspace-scope violation refuses the entry on the host
// and is only a warning in-jail, where the entry stays honored.
// It returns a Set rather than a slice, so the doctor path downstream gets the ORIGIN
// GATE with the records (census site 5, docs/reference/loophole-system.md#selection-and-discovery): `status`
// executes each loophole's doctor_cmd, and this command has no pack resolution of its own
// — it reads what the process recorded through NewHostSet. On a `yolo loopholes` process
// nothing records anything, so a pack loophole is absent rather than executed, which is
// the fail-safe direction and is stated on packModules.
func loopholesWithConfig(deps Deps, includeDisabled bool) Set {
	set, _ := loopholesWithConfigAndBlock(deps, includeDisabled)
	return set
}

func loopholesWithConfigAndBlock(deps Deps, includeDisabled bool) (Set, *jsonx.OrderedMap) {
	// The same file-backed set config.ValidateConfig resolves names against.
	known, _ := NewResolver().Known()

	load := func(f func() *jsonx.OrderedMap) *jsonx.OrderedMap {
		if f == nil {
			return nil
		}
		return f()
	}
	loadWS := func(f func(string) *jsonx.OrderedMap) *jsonx.OrderedMap {
		if f == nil {
			return nil
		}
		return f(deps.Cwd)
	}
	trackedPath, _ := config.ResolveWorkspaceConfigPath(deps.Cwd, config.WorkspaceConfigName)
	scopes := []struct {
		cfg           *jsonx.OrderedMap
		fromWorkspace bool
		src           string
	}{
		{load(deps.LoadUserConfig), false, paths.UserConfigDisplayPath()},
		// deps.LoadWorkspaceConfig collapses yolo-jail.jsonc and
		// yolo-jail.local.jsonc, so the merged block cannot say which file an
		// entry came from and the refusal used to blame the tracked file for a
		// violation living in the local one. config.WorkspaceLoopholeOrigins
		// re-reads the two files (the same re-read the launch validator does) and
		// names the actual origin per entry; the collapsed map still supplies the
		// VALUES, so the injected-config seam these commands are tested through
		// keeps working — with no real files it simply finds no origins and falls
		// back to the tracked file, as the loader resolves it
		// (config.ResolveWorkspaceConfigPath: `yolo-jail.json` where that is the file).
		{loadWS(deps.LoadWorkspaceConfig), true, trackedPath},
	}
	wsOrigins := config.WorkspaceLoopholeOrigins(deps.Cwd)
	userInline := map[string]bool{}
	merged := jsonx.NewOrderedMap()
	mergedSettings := jsonx.NewOrderedMap()
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
			if entry, ok := val.(*jsonx.OrderedMap); ok {
				if supplied, hasSettings := getMap(entry, "settings"); hasSettings {
					if previous, exists := mergedSettings.Get(k); exists {
						if previousEntry, ok := previous.(*jsonx.OrderedMap); ok {
							if previousSettings, exists := getMap(previousEntry, "settings"); exists {
								supplied = config.MergeConfig(previousSettings, supplied)
							}
						}
					}
					settingsEntry := jsonx.NewOrderedMap()
					settingsEntry.Set("settings", supplied)
					mergedSettings.Set(k, settingsEntry)
				}
			}
			merged.Set(k, val)
		}
	}
	// THE PER-WORKSPACE FILE, LAST (config/workspacefile.go), as the launch merges it: its
	// switches win over both files above. Its reader refuses everything but a boolean
	// `enabled`, so its entries need none of the validation above. A name no loophole here
	// answers to is left out, so the listing gains no inline entry nobody wrote.
	if deps.LoadWorkspaceFile != nil {
		if wf := deps.LoadWorkspaceFile(deps.Cwd); wf != nil {
			for _, name := range wf.Loopholes.Keys() {
				_, isKnown := known[name]
				_, inMerged := merged.Get(name)
				if v, set := wf.LoopholeSwitch(name); set && (isKnown || inMerged) {
					setConfigEnabled(merged, name, v)
				}
			}
		}
	}
	// IN A JAIL, the per-workspace file lives on the host and never crosses, and it may switch
	// any loophole: a brokered one's switch is its alone, and any other's it overrides. The
	// config the host launched this jail with holds the values that decided, so each loophole it
	// switches lists as it says (config.JailLaunchConfig). Without it a broker the jail is using
	// would list as disabled, and a loophole the per-workspace file turned off as the inherited
	// user config has it, on.
	if deps.InJail && deps.LaunchConfig != nil {
		if lc, ok := deps.LaunchConfig(deps.Cwd); ok {
			block, _ := getMap(lc, "loopholes")
			if block != nil {
				for _, name := range block.Keys() {
					_, isKnown := known[name]
					_, inMerged := merged.Get(name)
					if !isKnown && !inMerged {
						continue
					}
					if v, set := ConfigEnabledOverride(block, name); set {
						setConfigEnabled(merged, name, v)
					}
				}
			}
		}
	}
	// NewHostSet, not a hand-built DiscoverOptions: it is the one constructor that
	// composes pack + config (the only two sources left), so this command cannot come to disagree
	// with the launch path about what this machine has. It always builds the
	// include-disabled superset; includeDisabled selects the VIEW below.
	set := NewHostSet(merged)
	// Under a TOML user config a pack's settings are its `[allow.<pack>]` table (packswitch.go);
	// a `loopholes.<name>.settings` key a legacy file wrote wins.
	if user := load(deps.LoadUserConfig); user != nil {
		if allowV, ok := user.Get("allow"); ok {
			only := jsonx.NewOrderedMap()
			only.Set("allow", allowV)
			if lowered := ConfigBlock(only); lowered != nil {
				for _, name := range lowered.Keys() {
					supplied := SettingsFromConfig(lowered, name)
					if supplied == nil {
						continue
					}
					if previous := SettingsFromConfig(mergedSettings, name); previous != nil {
						supplied = config.MergeConfig(supplied, previous)
					}
					entry := jsonx.NewOrderedMap()
					entry.Set("settings", supplied)
					mergedSettings.Set(name, entry)
				}
			}
		}
	}
	if includeDisabled {
		return set, mergedSettings
	}
	return SetOf(set.Enabled()).withGate(set), mergedSettings
}

// List runs `yolo loopholes list`.
func List(deps Deps) int {
	all := loopholesWithConfig(deps, true).All()
	// Before the empty-set branch, because the two forms answer that case
	// differently on purpose: the human form explains where a loophole could come
	// from, the document says "nothing is installed" and nothing else. See
	// jsonreport.go.
	if outfmt.IsJSON(deps.Format) {
		return listJSON(deps, all)
	}
	// COLOR IS ADDITIVE (docs/plans/cli-visual-polish.md), as in Status: a colored line is the
	// plain report's text wrapped in style tags, and what yolo did not write in it (a loophole's
	// name, tags, intercept hosts, description, an unmet requirement's reason, a path) is
	// Escaped, so a style tag inside it prints as text in both forms. A line that carries no
	// color is written as before, outside the renderer, and so needs no escaping.
	p := richtext.Printer{W: deps.Out, Color: deps.Color}
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		// TWO SOURCES, not three. There used to be a `bundled:` line naming
		// BundledLoopholesDir(), and dropping it is the point rather than a trim: the
		// empty-list message is the one surface that tells a user WHERE a loophole
		// could come from, and naming a channel that no longer exists would send them
		// to look in a directory yolo does not read (docs/reference/loophole-system.md
		// OQ-BP4). Every loophole yolo ships is a pack's now, so `packs:` is the
		// answer to "why is this list empty".
		p.Printf("  • [bold]pack:[/bold] a `loophole` contribution from a selected pack; "+
			"%s is selected implicitly when it exists", richtext.Escape(paths.LocalPackDir()))
		p.Printf("  • [bold]config:[/bold] loopholes: block in %s "+
			"(install-shaped keys are user-scope only; a workspace "+
			"yolo-jail.jsonc may set enabled/jail_env)", richtext.Escape(paths.UserConfigDisplayPath()))
		return 0
	}
	for _, lh := range all {
		// The SHORT label. A superseded loophole's full sentence names a pack, a
		// capability and a free-text reason, which would blow the %-36s column and
		// push every other line's name out of alignment — and the reason is the part
		// a reader most needs to be able to read, so it gets a continuation line of
		// its own below rather than a truncated column.
		//
		// The state and its reason are computed by listState (jsonreport.go), which
		// `--format json` reads too. One computation, two renderings: the states this
		// column shows and the states the document reports cannot drift apart.
		state, reason := listState(lh)
		label := listLabel(state, reason)
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
		// The pad is measured on the PLAIN label and written after its closing tag. A width
		// on the tagged string would count the tags, and one on the rendered string the
		// escapes, and either would short the column. Taking the pad from fmt's own %-36s
		// keeps the width rule the plain report always had: a longer label gets none.
		pad := fmt.Sprintf("%-36s", label)[len(label):]
		style := listStateStyle(state, reason)
		p.Printf("  [%s]%s[/%s]%s  [bold]%s[/bold]  [dim](%s)  %s[/dim]", style, richtext.Escape(label), style,
			pad, richtext.Escape(lh.Name), richtext.Escape(tags), richtext.Escape(extra))
		if lh.Description != "" {
			p.Printf("      [dim]%s[/dim]", richtext.Escape(lh.Description))
		}
		// ANYTHING THAT TURNS SOMETHING OFF MUST NAME WHO DID IT AND WHY
		// (docs/reference/pack-system.md §5). An unexplained disappearance is the
		// failure mode the whole mechanism exists to avoid, and `loopholes list` is the
		// command a user runs to find out what happened — so the pack, the capability and
		// the pack author's own `because` are printed here, one line per claim.
		for _, s := range lh.SupersededBy {
			fmt.Fprintf(deps.Out, "      %s\n", s.Line())
		}
		// THE SETTINGS DECLARATIONS ARE PRINTED HERE BECAUSE THERE IS NOWHERE ELSE
		// LEFT (docs/reference/pack-system.md). `yolo config-ref` is generated from
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
	// Before the in-jail short-circuit: the document reports that fact in a field
	// rather than as a sentence (see statusJSON).
	if outfmt.IsJSON(deps.Format) {
		return statusJSON(deps)
	}
	// COLOR IS ADDITIVE (docs/plans/cli-visual-polish.md): every line below is the plain
	// report's text wrapped in style tags, so with color off the tags vanish and the text is
	// unchanged. What yolo did not write (a loophole's name, a doctor's output, a supersession's
	// reason) is Escaped, so a style tag inside it prints as text in both forms.
	p := richtext.Printer{W: deps.Out, Color: deps.Color}
	if deps.InJail {
		p.Print("Inside jail — doctor checks are host-side.  From the host: [cyan]yolo loopholes status[/cyan]")
		return 0
	}
	set, currentConfig := loopholesWithConfigAndBlock(deps, true)
	all := set.All()
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		return 0
	}
	// THE SET's doctor runner, not the package-level one. `status` runs each loophole's
	// doctor_cmd — host code — and this command is one users treat as read-only preflight,
	// so a pack-shipped record runs only when the origin gate was evaluated AND passed
	// (docs/reference/loophole-system.md#selection-and-discovery). A withheld one is REPORTED, with the
	// reason, rather than skipped: a skip is indistinguishable from `no-check`, which
	// would read as "this loophole declares no self-check" — the wrong story entirely.
	for _, r := range runStatusDoctorChecks(set, all, currentConfig) {
		state := doctorState(set, r)
		// The state stays in its brackets, which are literal text to the renderer: no state
		// word is a style word, so `[ok]` prints as itself inside the tags around it.
		p.Printf("  [%s][%s][/%s] [bold]%s[/bold]  [dim]rc=%s[/dim]", doctorStateStyle[state], state,
			doctorStateStyle[state], richtext.Escape(r.Loophole.Name), rcStr(r.RC))
		// The who and the why, for the same reason `loopholes list` carries them: a
		// loophole a pack turned off must never be an unexplained absence, and `status` is
		// the other command a user reaches for when one is not working.
		for _, s := range r.Loophole.SupersededBy {
			p.Printf("      %s", richtext.Escape(s.Line()))
		}
		if r.Output != "" {
			for _, line := range strings.Split(r.Output, "\n") {
				p.Printf("      [dim]%s[/dim]", richtext.Escape(line))
			}
		}
	}
	return 0
}

// runStatusDoctorChecks validates current settings before asking each doctor to inspect them.
// The persistent settings file remains the last-launch input for a running daemon and is
// deliberately not rewritten by this read-only status command.
func runStatusDoctorChecks(set Set, all []*Loophole, settingsBlock *jsonx.OrderedMap) []DoctorResult {
	current := make(map[string][]byte)
	rejected := make(map[string]DoctorResult)
	for _, lp := range all {
		if lp == nil || !lp.Active() || !set.MayRunHostCode(lp) || len(lp.Settings) == 0 || len(lp.DoctorCmd) == 0 {
			continue
		}
		if len(lp.PlacementProblems("")) > 0 {
			continue
		}
		frozen, _, err := FrozenSettingsBytes(lp, SettingsFromConfig(settingsBlock, lp.Name))
		if err != nil {
			rc := 1
			rejected[lp.Name] = DoctorResult{Loophole: lp, RC: &rc, Output: "Current settings could not be resolved: " + err.Error() +
				". Correct `loopholes." + lp.Name + ".settings` in its declared scope and rerun `yolo loopholes status`."}
			continue
		}
		if lp.HostDaemon != nil && len(lp.HostDaemon.SettingsCheck) > 0 {
			checked := RunSettingsCheck(lp, frozen)
			if checked.Outcome != hostservice.CommandAccepted {
				rc := 1
				rejected[lp.Name] = DoctorResult{Loophole: lp, RC: &rc,
					Output: "Current settings were not accepted: " + checked.Reason + "\nRemedy: " + checked.Remedy +
						"\nThe self-check was not run; this finding is about current settings, not live daemon health. Correct the settings in their declared scope and rerun `yolo loopholes status`."}
				continue
			}
		}
		current[lp.Name] = frozen
	}

	var runnable []*Loophole
	for _, lp := range all {
		if _, skipped := rejected[lp.Name]; !skipped {
			runnable = append(runnable, lp)
		}
	}
	results := set.RunDoctorChecksWithSettings(runnable, doctorCheckTimeout, current, nil)
	byName := make(map[string]DoctorResult, len(results)+len(rejected))
	for _, result := range results {
		byName[result.Loophole.Name] = result
	}
	for name, result := range rejected {
		byName[name] = result
	}
	ordered := make([]DoctorResult, 0, len(all))
	for _, lp := range all {
		if result, ok := byName[lp.Name]; ok {
			ordered = append(ordered, result)
		}
	}
	return ordered
}

// doctorStateStyle is the color of each doctorState word, by the CLI's one convention
// (docs/plans/cli-visual-polish.md): green for a check that passed, red for one that failed,
// yellow where the loophole wants attention (an unmet requirement, a check the origin gate
// withheld), and dim where off is the expected answer (turned off, superseded by a selected
// pack's choice, or declaring no self-check). Every state doctorState returns has an entry.
var doctorStateStyle = map[string]string{
	"ok":         "green",
	"fail":       "red",
	"inactive":   "yellow",
	"unapproved": "yellow",
	"disabled":   "dim",
	"superseded": "dim",
	"no-check":   "dim",
}

// listStateStyle is the color of the state label `yolo loopholes list` prints for listState's
// pair, by the same convention as doctorStateStyle and with the color `status` gives the same
// fact: green for an active loophole, yellow for one a fact about this machine keeps inactive (an
// unmet requirement, a platform it does not run on, a binary not fetched yet), which `status`
// reports yellow as `[inactive]`, and dim for one turned off or superseded, where off is the
// expected answer — superseded being the user's own pack selection, which `status` reports dim
// as `[superseded]`.
func listStateStyle(state, reason string) string {
	switch {
	case state == "active":
		return "green"
	case state == "inactive" && reason != "superseded":
		return "yellow"
	default:
		return "dim"
	}
}

// SetEnabledOptions is what `yolo loopholes enable|disable` was asked beyond the name.
type SetEnabledOptions struct {
	// Workspace is the workspace the switch is for, already resolved by the caller: the
	// `--workspace` it was given, else the current workspace root.
	Workspace string
	// Global asks for the user-config block that switches the loophole for every workspace,
	// printed to paste: nothing is written.
	Global bool
}

// CmdSetEnabled runs `yolo loopholes enable|disable <name>`: it writes the switch into the
// workspace's PER-WORKSPACE FILE (config/workspacefile.go), host-side, and says which file and
// that it applies at the workspace's next fresh launch.
//
// IT NEVER EDITS THE USER CONFIG, by the maintainer's ruling (2026-10-01,
// docs/design/boundary-broker.md OQ-BB12): "I don't want to do anything that edits the user
// config directly. We can edit a file that lives next to it of a different name or whatever."
// config.jsonc is a hand-commented file, and a read-modify-write through the JSONC decoder drops
// every comment in it. So `--global`, the every-workspace switch, still prints the block to
// paste, writes nothing and exits 1, as the whole command did until this change.
//
// A BROKERED LOOPHOLE HAS NO SWITCH (BB-D104, BB-D109): it runs whenever its pack is selected,
// and what it may do is a grant in config. Both verbs refuse for one, naming that grant
// (setBrokerSwitch), and `disable` first takes out a switch a previous yolo wrote.
//
// HOST-SIDE ONLY. In a jail it refuses, naming the command to run on the host: the file lives
// in a folder no jail can read, and a jail's own folder would govern only launches made inside
// it, which is not what someone typing this in a jail means.
//
// A name `yolo loopholes list` does not show is refused, `--global` included, except that
// `disable` of one takes its switch out of the per-workspace file when the file still holds it:
// the trace of a deselected pack, which nothing else could clear.
//
// It never writes a manifest either (TestSetEnabledNeverWritesAManifest): after OQ-A9 a
// manifest carries the pack author's default, not the user's answer.
func CmdSetEnabled(deps Deps, name string, enabled bool, opts SetEnabledOptions) int {
	verb := verbFor(enabled)
	if deps.InJail {
		// A broker has no switch on either side (BB-D109): say so here too, rather than send
		// the user to a host command that refuses.
		for _, lp := range loopholesWithConfig(deps, true).All() {
			if lp.Name == name && lp.Brokered != nil {
				fmt.Fprintf(deps.Err, "yolo loopholes %s: %s has no switch: it runs whenever the pack that "+
					"ships it is selected, and grants nothing until the user's config does. %s\n", verb, name,
					config.BrokerGrantAdviceFor(*lp.Brokered))
				return 1
			}
		}
	}
	if deps.InJail && !opts.Global {
		fmt.Fprintf(deps.Err, "yolo loopholes %s: this is a jail, and a per-workspace switch is "+
			"made on the host, in a folder no jail can read.\n", verb)
		if deps.HostDir != "" && config.IsJailOwnWorkspace(opts.Workspace) {
			fmt.Fprintf(deps.Err, "On the host, run:\n  %s\nthen start a fresh jail there.\n",
				setEnabledCommand(enabled, name, deps.HostDir))
		} else {
			fmt.Fprintf(deps.Err, "On the host, run `yolo loopholes %s %s` in that project, then "+
				"start a fresh jail there.\n", verb, name)
		}
		return 1
	}
	// The names `yolo loopholes list` shows, so the refusal's next step lists exactly what
	// this command accepts. Checked before `--global` too, whose block for a name nothing
	// installs would be one the launch warns about or, once a pack ships it, refuses.
	var found *Loophole
	for _, lp := range loopholesWithConfig(deps, true).All() {
		if lp.Name == name {
			found = lp
		}
	}
	if found == nil {
		if !enabled && !opts.Global {
			// A switch for a loophole whose pack was deselected since: `yolo check` warns about
			// it, and this is the command that clears it.
			if rc, handled := clearUninstalledSwitch(deps, name, opts.Workspace); handled {
				return rc
			}
		}
		fmt.Fprintf(deps.Err, "yolo loopholes %s: no loophole named %q is installed on this "+
			"machine. `yolo loopholes list` shows the ones that are; a pack you select in %s "+
			"installs its own.\n", verb, name, paths.UserConfigDisplayPath())
		return 1
	}
	if found.Brokered != nil {
		return setBrokerSwitch(deps, found, enabled, opts)
	}
	if opts.Global {
		return setEnabledGlobally(deps, found, enabled)
	}
	ws := opts.Workspace
	if !workspaceUsable(deps, verb, name, enabled, ws) {
		return 1
	}
	write := deps.WriteSwitch
	if write == nil {
		write = config.SetWorkspaceLoophole
	}
	path, err := write(ws, name, enabled)
	if err != nil {
		fmt.Fprintf(deps.Err, "yolo loopholes %s: %v\n", verb, err)
		return 1
	}
	state := "on"
	if !enabled {
		state = "off"
	}
	fmt.Fprintf(deps.Out, "%s is %s for %s from its next fresh launch; the switch is in %s\n",
		name, state, ws, path)
	return 0
}

// workspaceUsable reports whether ws can carry a per-workspace switch, and refuses with the next
// step when it cannot: a folder no launch may use as a workspace, or no folder at all.
func workspaceUsable(deps Deps, verb, name string, enabled bool, ws string) bool {
	if breach := paths.WorkspaceScopeBreach(ws); breach != nil {
		fmt.Fprintf(deps.Err, "yolo loopholes %s: %s, and no launch may use it as a workspace. "+
			"cd into the project, or name it: %s\n", verb, breach.What(),
			setEnabledCommand(enabled, name, "<project>"))
		return false
	}
	if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
		fmt.Fprintf(deps.Err, "yolo loopholes %s: %s is not a folder, so it cannot be a workspace. "+
			"Name the project's folder with --workspace, or cd into it.\n", verb, ws)
		return false
	}
	return true
}

// clearUninstalledSwitch is `disable` of a name no loophole on this machine answers to: when the
// workspace's per-workspace file still holds a switch for it, it takes the switch out and says
// so. handled is false when there is no such switch, and the caller refuses the name as before.
func clearUninstalledSwitch(deps Deps, name, ws string) (rc int, handled bool) {
	if paths.WorkspaceScopeBreach(ws) != nil {
		return 0, false
	}
	if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
		return 0, false
	}
	remove := deps.RemoveSwitch
	if remove == nil {
		remove = config.RemoveWorkspaceLoophole
	}
	path, removed, err := remove(ws, name)
	if err != nil {
		fmt.Fprintf(deps.Err, "yolo loopholes disable: %v\n", err)
		return 1, true
	}
	if !removed {
		return 0, false
	}
	fmt.Fprintf(deps.Out, "%s is not installed on this machine, so its switch for %s is removed "+
		"from %s; it stays off there.\n", name, ws, path)
	return 0, true
}

// setBrokerSwitch is `enable` or `disable` of a brokered loophole, which has no switch any more
// (docs/design/boundary-broker.md BB-D104, BB-D109): it runs whenever its pack is selected, and
// what it may do is a grant in config, approved with `yolo config apply`. So both verbs refuse,
// naming that grant; `disable` first takes out a switch a previous yolo wrote into this
// workspace's per-workspace file, which is ignored now, and says so.
func setBrokerSwitch(deps Deps, lp *Loophole, enabled bool, opts SetEnabledOptions) int {
	verb := verbFor(enabled)
	advice := config.BrokerGrantAdviceFor(*lp.Brokered)
	if !enabled && !opts.Global && paths.WorkspaceScopeBreach(opts.Workspace) == nil {
		if fi, err := os.Stat(opts.Workspace); err == nil && fi.IsDir() {
			remove := deps.RemoveSwitch
			if remove == nil {
				remove = config.RemoveWorkspaceLoophole
			}
			path, removed, err := remove(opts.Workspace, lp.Name)
			if err != nil {
				fmt.Fprintf(deps.Err, "yolo loopholes disable: %v\n", err)
				return 1
			}
			if removed {
				fmt.Fprintf(deps.Out, "%s has no per-workspace switch any more, so its old one is removed from "+
					"%s. It runs whenever its pack is selected, and grants nothing until your config does. %s\n",
					lp.Name, path, advice)
				return 0
			}
		}
	}
	fmt.Fprintf(deps.Err, "yolo loopholes %s: %s has no switch: it runs whenever the pack that ships it is "+
		"selected, and grants nothing until your config does. %s To stop it running, take its pack out of "+
		"`packs` in %s.\n", verb, lp.Name, advice, paths.UserConfigDisplayPath())
	return 1
}

// setEnabledGlobally is `--global`: the every-workspace switch. In the readable config `packs` is
// the only switch (docs/design/config-user.md A.4): a loophole is on user-wide when the pack that
// ships it is selected, so the instruction names the pack verb, not a `loopholes.*` key (which
// `yolo config set` refuses). It writes nothing and exits 1, since nothing changed here
// (OQ-BB12). lp is the installed loophole, whose Pack says who ships it ("" when config does).
func setEnabledGlobally(deps Deps, lp *Loophole, enabled bool) int {
	verb, name := verbFor(enabled), lp.Name
	perWS := fmt.Sprintf("To switch it for one workspace only, run `yolo loopholes %s %s` in it, which writes "+
		"that workspace's own file beside the user config.\n", verb, name)
	if lp.Pack == "" {
		fmt.Fprintf(deps.Err, "yolo loopholes %s --global writes nothing: no pack ships %s (it is declared "+
			"in config), so there is no pack to %s. `yolo pack ls` shows the selected packs and "+
			"`yolo loopholes list` where each loophole comes from.\n%s", verb, name,
			map[bool]string{true: "select", false: "deselect"}[enabled], perWS)
		return 1
	}
	pack := shquote.JoinDisplay([]string{lp.Pack})
	if enabled {
		fmt.Fprintf(deps.Err, "yolo loopholes enable --global writes nothing: %s is on for every workspace "+
			"whenever the pack that ships it is selected, and `%s` is (to select it again after removing it: "+
			"`yolo pack add %s`, which edits %s). A workspace turns it off with `off = true` on its pack entry.\n%s",
			name, lp.Pack, pack, paths.UserConfigDisplayPath(), perWS)
		return 1
	}
	fmt.Fprintf(deps.Err, "yolo loopholes disable --global writes nothing: %s is on for every workspace "+
		"because the pack that ships it, `%s`, is selected. To turn it off everywhere, run `yolo pack remove %s` "+
		"(it edits %s).\n%s", name, lp.Pack, pack, paths.UserConfigDisplayPath(), perWS)
	return 1
}

// setEnabledCommand is the command that makes this switch for workspace, spelled to paste: the
// path is quoted for a shell, and a control character in it is written as an escape.
// placeholder is a workspace the reader fills in, which is left as written.
func setEnabledCommand(enabled bool, name, workspace string) string {
	ws := workspace
	if !strings.HasPrefix(ws, "<") {
		ws = shquote.QuoteDisplay(ws)
	}
	if enabled {
		return "yolo loopholes enable " + name + " --workspace " + ws
	}
	return "yolo loopholes disable " + name + " --workspace " + ws
}

// setConfigEnabled sets `enabled` on block's entry for name, keeping the entry's other keys, in a
// new map: the entry may be a loaded config's own.
func setConfigEnabled(block *jsonx.OrderedMap, name string, enabled bool) {
	entry := jsonx.NewOrderedMap()
	if old, ok := block.Get(name); ok {
		if m, isMap := old.(*jsonx.OrderedMap); isMap {
			for _, k := range m.Keys() {
				v, _ := m.Get(k)
				entry.Set(k, v)
			}
		}
	}
	entry.Set("enabled", enabled)
	block.Set(name, entry)
}

// getMap is m[key] as an object.
func getMap(m *jsonx.OrderedMap, key string) (*jsonx.OrderedMap, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.Get(key)
	if !ok {
		return nil, false
	}
	out, isMap := v.(*jsonx.OrderedMap)
	return out, isMap
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

// doctorState is the bracketed prefix `yolo loopholes status` reports for one
// doctor result, and the `state` its JSON form carries — one computation, two
// renderings, for the reason jsonreport.go's header gives.
//
// THE ORDER OF THE CASES IS THE BEHAVIOUR. `superseded` sits between `disabled`
// and `unapproved`, mirroring Active()/InactiveReason(): a superseded loophole is
// off for a reason the user's own pack selection chose, which outranks every
// machine fact below it, so reporting it as `inactive` would send the reader
// after an unmet requirement that is not why it is off. And `no-check` (the
// loophole declares no self-check) must stay distinct from `unapproved` (a check
// that was withheld by the origin gate) — collapsing them tells the reader the
// opposite of what happened.
//
// It takes the Set because `unapproved` is the SET's decision, not the
// loophole's: the origin gate lives on the resolved set.
func doctorState(set Set, r DoctorResult) string {
	switch {
	case !r.Loophole.Enabled:
		return "disabled"
	case r.Loophole.Superseded():
		return "superseded"
	case !set.MayRunHostCode(r.Loophole):
		return "unapproved"
	// `platforms` before the `requires` probe, as in Active(): RequirementsMet() folds in
	// neither, so without this a loophole this machine cannot run fell through to its
	// doctor_cmd's exit code and was graded `ok` or `fail` as if it were live here. A
	// downloaded binary not fetched yet is the same answer (binaries.go): its self-check was
	// not run, and `no-check` would say the loophole declares none.
	case !r.Loophole.SupportedHere(), !r.Loophole.BinariesFetched(), !r.Loophole.RequirementsMet():
		return "inactive"
	case r.RC != nil && *r.RC == 0:
		return "ok"
	case r.RC == nil:
		return "no-check"
	default:
		return "fail"
	}
}
