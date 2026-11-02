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
	// RemoveSwitch takes one switch out of a workspace's per-workspace file, returning the file
	// and whether it held the switch (config.RemoveWorkspaceLoophole): `disable` of a loophole
	// no longer installed.
	RemoveSwitch func(workspace, name string) (string, bool, error)
	// Standings is each grant-declaring loophole's standing per kind in a workspace
	// (ResolveStandings), by loophole name, for `list`, `status` and the retired verbs' refusal;
	// nil shows none.
	Standings func(workspace string, lps []*Loophole) (map[string][]KindStanding, error)
}

// RealDeps returns Deps backed by the real filesystem/config loaders.
func RealDeps() Deps {
	cwd, _ := os.Getwd()
	inJail := os.Getenv("YOLO_VERSION") != ""
	return Deps{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Cwd:    cwd,
		InJail: inJail,
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
		RemoveSwitch:      config.RemoveWorkspaceLoophole,
		Standings: func(workspace string, lps []*Loophole) (map[string][]KindStanding, error) {
			return ResolveStandings(workspace, inJail, lps)
		},
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
					if v, set := config.LoopholeEnabledOverride(block, name); set {
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
	standings := standingsFor(deps, all)
	// Before the empty-set branch, because the two forms answer that case
	// differently on purpose: the human form explains where a loophole could come
	// from, the document says "nothing is installed" and nothing else. See
	// jsonreport.go.
	if outfmt.IsJSON(deps.Format) {
		return listJSON(deps, all, standings)
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
		printStandings(p, standings[lh.Name])
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
	standings := standingsFor(deps, all)
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
		printStandings(p, standings[r.Loophole.Name])
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
	// Workspace is the workspace whose old switch `disable` removes, already resolved by the
	// caller: the `--workspace` it was given, else the working directory.
	Workspace string
	// Global was the every-workspace form. It is still parsed, so an old script gets the
	// refusal rather than a usage error, and it removes nothing.
	Global bool
}

// CmdSetEnabled runs `yolo loopholes enable|disable <name>`, which are RETIRED
// (docs/reference/loophole-grants.md §4.5, LG-D8): a loophole runs whenever its pack is
// selected, and a project gets access by a grant, not a switch. Both refuse for every loophole
// with §5's text, naming `yolo request` for this project and `yolo allow` for every project
// (retiredRefusal), and exit 1, as a retired spelling does in this CLI.
//
// `disable` FIRST removes a switch an older yolo wrote into the workspace's per-workspace file
// (config/workspacefile.go), host-side, and exits 0 when it removed one: until the migration
// retires that file, its `enabled: true` reads as a grant, so this is the command that takes it
// back. It does so for a name no installed loophole answers to too, the trace of a deselected
// pack. In a jail the file is on the host, so there is nothing here to remove.
//
// It never writes anything else: no switch, no user config, no manifest
// (TestSetEnabledNeverWritesAManifest).
func CmdSetEnabled(deps Deps, name string, enabled bool, opts SetEnabledOptions) int {
	verb := verbFor(enabled)
	if !enabled && !opts.Global && !deps.InJail {
		if rc, handled := removeOldSwitch(deps, name, opts.Workspace); handled {
			return rc
		}
	}
	var found *Loophole
	for _, lp := range loopholesWithConfig(deps, true).All() {
		if lp.Name == name {
			found = lp
		}
	}
	if found == nil {
		fmt.Fprintf(deps.Err, "yolo loopholes %s: no loophole named %q is installed on this "+
			"machine. `yolo loopholes list` shows the ones that are; a pack you select in %s "+
			"installs its own.\n", verb, name, paths.UserConfigDisplayPath())
		return 1
	}
	fmt.Fprint(deps.Err, retiredRefusal(deps, verb, found))
	return 1
}

// removeOldSwitch is `disable`'s first step: when ws's per-workspace file holds a switch for
// name, it takes the switch out, says so, and handled is true. handled is false when there is
// no such switch, or ws cannot be a workspace, and the caller refuses as for `enable`.
func removeOldSwitch(deps Deps, name, ws string) (rc int, handled bool) {
	if ws == "" || paths.WorkspaceScopeBreach(ws) != nil {
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
	fmt.Fprintf(deps.Out, "Removed the switch an older yolo wrote for %s from %s. Switches are retired: a loophole "+
		"runs whenever its pack is selected, and a project gets access by a grant (`yolo loopholes list` shows "+
		"what this project holds).\n", name, path)
	return 0, true
}

// retiredHeader is the first two lines of every retired-verb refusal (§5).
func retiredHeader(verb string) string {
	return "yolo loopholes " + verb + " is retired: a loophole runs whenever its pack is selected, and a project " +
		"gets access by a\n  grant, not a switch.\n"
}

// retiredRefusal is the refusal for lp, by what it declares: a grant-declaring loophole names
// the request for this project and the allow for every project, on the kind a project asks for
// first (exampleGrant); a brokered one names its read grant; one that declares no grant names
// its pack, which is all that decides whether it runs.
func retiredRefusal(deps Deps, verb string, lp *Loophole) string {
	var b strings.Builder
	b.WriteString(retiredHeader(verb))
	pack := shquote.JoinDisplay([]string{lp.Pack})
	switch {
	case len(lp.Grants) > 0 && lp.Pack != "":
		g := exampleGrant(lp.Grants)
		req := "yolo request " + pack + " " + g.Kind
		if g.Valued {
			req += " <" + valueNoun(g) + ">"
		}
		fmt.Fprintf(&b, "  For this project:    %s     (in the workspace), then `yolo config apply` here and y\n", req)
		if !requestableHere(deps, lp, g) {
			fmt.Fprintf(&b, "                       your config does not allow it on request yet, so first:  "+
				"yolo allow %s on_request %s\n", pack, g.Kind)
		}
		if g.Valued {
			fmt.Fprintf(&b, "  For every project:   yolo allow %s on_request %s     (each project then requests its own %s)\n",
				pack, g.Kind, valueNoun(g))
		} else {
			fmt.Fprintf(&b, "  For every project:   yolo allow %s %s\n", pack, g.Kind)
		}
		if path, toml := config.UserConfigFile(); !toml {
			fmt.Fprintf(&b, "  `yolo allow` writes a TOML user config: convert %s first with `yolo config migrate` on the host.\n", path)
		}
	case lp.Brokered != nil:
		fmt.Fprintf(&b, "  %s has no grant of its own to request. %s\n", lp.Name, config.BrokerGrantAdviceFor(*lp.Brokered))
	case lp.Pack != "" && config.IsSelectedByDefault(lp.Pack):
		fmt.Fprintf(&b, "  %s declares no grant, and its pack, %s, is selected by default: it runs in every launch "+
			"whose setup can run it, with no line in any config.\n", lp.Name, lp.Pack)
	case lp.Pack != "":
		fmt.Fprintf(&b, "  %s declares no grant, so its pack, %s, is all that decides it: `yolo pack add %s` runs it "+
			"everywhere.\n", lp.Name, lp.Pack, pack)
	default:
		fmt.Fprintf(&b, "  %s is declared in config (`loopholes.%s` in %s), not by a pack; it runs while that entry "+
			"is there.\n", lp.Name, lp.Name, paths.UserConfigDisplayPath())
	}
	if verb == "disable" && lp.Pack != "" {
		if config.IsSelectedByDefault(lp.Pack) {
			fmt.Fprintf(&b, "  To stop it running:  not possible yet. %s.\n", config.SelectedByDefaultNoOff(lp.Pack))
		} else {
			fmt.Fprintf(&b, "  To stop it running:  yolo pack remove %s\n", pack)
		}
	}
	return b.String()
}

// exampleGrant is the kind a refusal shows: the one requestable by default, else the one a
// legacy switch converted to, else the first declared.
func exampleGrant(grants []Grant) Grant {
	for _, g := range grants {
		if g.RequestableByDefault {
			return g
		}
	}
	for _, g := range grants {
		if g.LegacyEnable {
			return g
		}
	}
	return grants[0]
}

// valueNoun is a valued kind's noun for its placeholder, as `yolo request` spells it.
func valueNoun(g Grant) string {
	if g.ValueNoun != "" {
		return g.ValueNoun
	}
	return "values"
}

// requestableHere is whether a request for g would reach the gate: its standing is anything but
// not_allowed. With no standing to read, the manifest's own default answers.
func requestableHere(deps Deps, lp *Loophole, g Grant) bool {
	if deps.Standings != nil {
		if byName, err := deps.Standings(deps.Cwd, []*Loophole{lp}); err == nil {
			for _, k := range byName[lp.Name] {
				if k.Kind == g.Kind {
					return k.Standing != config.LoopholeGrantNotAllowed
				}
			}
		}
	}
	return g.RequestableByDefault
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
