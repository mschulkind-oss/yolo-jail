package cli

// hostmodelmenu.go is what `yolo host --` hands a program from the program's own derives, for this
// launch alone, before it execs it: its LAUNCH SELECTION and its MODEL MENU.
//
// THE LAUNCH SELECTION (packdecl.LaunchSelection, a term coined there;
// docs/design/model-lists-and-pickers.md MM-D30, OQ-MM5 decided on its leaning B): a host `-p`
// moves a program whose provider and model live in its own config file (codex, opencode, pi,
// oh-omp) onto the -p's selection for this launch, without writing that file. The pack's own
// config-surface derive composes the selection over this launch's wire tables
// (entrypoint.HostLaunchSelection, MM-D24's one path), and when a `-p` was typed and what it
// composes differs from what the configured profile composes, the launch hands it to the program in
// the pack's words: argv right after argv[0] (codex's `-c`, pi's `--provider`), or a variable
// (opencode's OPENCODE_CONFIG_CONTENT). With no -p, and through every host wrapper, the program
// starts on its file, which `yolo host apply` writes for the configured profile (OQ-HC3).
//
// THE MODEL MENU (packdecl.ModelMenu, a term coined there; §14.7, MM-D24 to MM-D28): the step a
// jail's launcher runs before it execs a program whose pack declares one, run here against the
// program it is about to exec.
//
//   - THE LIST IS THE LAUNCH'S (MM-D24): the pack's own list derive, run in-process over this
//     launch's wire tables (entrypoint.HostModelList). No file is rendered.
//   - IT FOLLOWS THE PROVIDER THE PROGRAM RUNS ON (MM-D30, superseding MM-D25): the -p's, since the
//     launch selection moves the program onto it. Only where it did not, a selection that could not
//     be handed or a pack that declares no launch selection, does a -p over another provider get no
//     menu, since the list would name models the program is not running on.
//   - THE JAIL'S BUILD (MM-D26): modelmenu.Request, the one the jail's `yolo internal model-menu`
//     runs, with the list as an argument and the resolved target as the program.
//   - KEPT IN YOLO'S STATE WHILE A PROGRAM READS IT (MM-D27): modelmenu.Request.WriteIn, one menu
//     per cache key under paths.HostModelMenusDir, behind a shared lock the launch keeps for its
//     program's life.
//
// It knows no program: the declarations are the pack's, and the words it prints name the bin.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// hostMenuProgram is the selected pack that installs bin with a declared model menu, and that
// declaration: nil, nil when no selected pack declares one for bin. A program is sole-owned by its
// bin, so there is at most one.
func hostMenuProgram(packs []*packload.Pack, bin string) (*packload.Pack, *packdecl.ModelMenu) {
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.Bin == bin && in.ModelMenu != nil {
				return p, in.ModelMenu
			}
		}
	}
	return nil, nil
}

// hostModelMenuDir is the shared menu directory of one pack's program (MM-D27).
func hostModelMenuDir(pack, bin string) string {
	return filepath.Join(paths.HostModelMenusDir(), pack, bin)
}

// hostMenu is the menu one host launch hands its program: the held menu, and what its
// disclosure names, the pack that declared it and the provider its list is for.
type hostMenu struct {
	held           *modelmenu.Held
	pack, provider string
}

// Close releases the menu's lock, on the resident path and on every return before the exec.
// Safe on a nil menu.
func (m *hostMenu) Close() {
	if m != nil {
		_ = m.held.Close()
	}
}

// KeepAcrossExec hands the lock to the program about to be exec'd (modelmenu.Held.KeepAcrossExec).
// A failure is a line: the program still starts on its menu, which a later launch that writes
// another may then collect while it runs.
func (m *hostMenu) KeepAcrossExec(errw io.Writer) {
	if m == nil {
		return
	}
	if err := m.held.KeepAcrossExec(); err != nil {
		fmt.Fprintf(errw, "yolo host: could not hand the lock on %s to the program (%v); a later launch "+
			"may remove that menu while it runs.\n", m.held.Path, err)
	}
}

// rewrite is argv with the menu's flag right after argv[0], ahead of the pack's launch flags and
// the user's own argv, where the jail's launcher puts it (MM-D26), so a user's own later flag of
// the same name still wins; and the rewrite's disclosure, in the pack flags' words
// (LaunchInjection.DisclosureLines), since a launch has no quiet mode. argv unchanged and no lines
// for a nil menu.
func (m *hostMenu) rewrite(argv []string) ([]string, []string) {
	if m == nil || len(m.held.Flag) == 0 || len(argv) == 0 {
		return argv, nil
	}
	bin := filepath.Base(argv[0])
	out := append(append([]string{argv[0]}, m.held.Flag...), argv[1:]...)
	inj := &packload.LaunchInjection{Pack: m.pack, Flags: append([]string{}, m.held.Flag...),
		Before: append([]string{}, argv...), After: out,
		Why: fmt.Sprintf("%s's model menu: yolo's models for %s, from %s's own catalog", bin, m.provider, bin)}
	return out, inj.DisclosureLines()
}

// modelMenu is the step: the menu this launch hands its program, or nil when there is none to hand
// — no declaration, a jail, YOLO_NO_LAUNCH_FLAGS=1, a `-p` the program was not moved onto (sel), no
// list, or a build that failed. target is the resolved program, catalogEnv the environment its
// catalog run gets (the one the program itself will). Nothing here refuses the launch: every
// failure is a line on errw, and the program keeps its own menu.
func (c *hostComposition) modelMenu(target string, catalogEnv []string, sel *hostSelection, errw io.Writer) *hostMenu {
	p, spec := hostMenuProgram(c.packs, c.agent)
	if spec == nil {
		return nil
	}
	// IN A JAIL THE JAIL'S LAUNCHER ANSWERS, as it does for the binary (resolveHostLaunchTarget
	// runs the PATH copy there; MM-D28 (9)): that launcher builds the program's menu from the list
	// the jail's boot rendered for the jail's own selection, workspace scope and the jail
	// launch's `-p` included. A menu composed here, from the user scope's `profile`, would come
	// later in the argv and win (`-c` is last-wins), handing the program another selection's
	// models; and the store would be made in the jail's own home, which no host step is about.
	if config.InJail() {
		return nil
	}
	// The jail's escape, and the same one: the menu's words are a pack's flag (MM-D22).
	if os.Getenv(entrypoint.NoLaunchFlagsEnv) == "1" {
		return nil
	}
	if line, follows := c.menuFollowsTheLaunch(sel); !follows {
		if line != "" {
			fmt.Fprintf(errw, "yolo host: %s\n", line)
		}
		return nil
	}
	list, err := entrypoint.HostModelList(p, *spec, paths.Home(),
		&entrypoint.HostInputs{Vars: c.wireTables(), Packs: c.packs},
		func(line string) { fmt.Fprintf(errw, "yolo host: %s\n", line) })
	if err != nil {
		fmt.Fprintf(errw, "yolo host: could not compose %s's model list (%v), so %s shows its own model menu.\n",
			c.agent, err, c.agent)
		return nil
	}
	held := modelmenu.Request{Bin: c.agent, Spec: *spec, List: list, Program: []string{target},
		Env: catalogEnv, Prefix: "yolo host: "}.WriteIn(hostModelMenuDir(p.Name, c.agent), errw)
	if held == nil {
		return nil
	}
	return &hostMenu{held: held, pack: p.Name, provider: packload.ProviderFor(c.resolved, c.profile)}
}

// menuFollowsTheLaunch is MM-D30's menu rule: whether the program runs on this launch's selection,
// so a list composed for the launch names its models, and the line to print when it does not. It
// does with no -p, and with a -p its launch selection agrees with or moved it onto (sel). With a -p
// sel did not move it onto, it does not, and sel's own line has said why. A program whose pack
// declares no launch selection follows a -p only over the provider its configured profile selects:
// its file names that one, so a menu for another could list models it is not using, the fault
// MM-D22 exists to prevent. Providers, not profile names, because the list derive reads the
// provider.
func (c *hostComposition) menuFollowsTheLaunch(sel *hostSelection) (string, bool) {
	if c.typedProfile == "" {
		return "", true
	}
	if sel != nil && sel.declared {
		return "", sel.follows
	}
	launch := packload.ProviderFor(c.resolved, c.profile)
	configured := packload.ProviderFor(c.resolved, c.configuredPrimary())
	if launch == configured {
		return "", true
	}
	selects := func(p string) string {
		if p == "" {
			return "no provider"
		}
		return p
	}
	cfg := fmt.Sprintf("the profile your config names for %s (%s) selects %s", c.agent,
		c.configuredPrimary(), selects(configured))
	if c.configuredPrimary() == "" {
		cfg = fmt.Sprintf("your config names no profile for %s", c.agent)
	}
	return fmt.Sprintf("%s gets no model menu from yolo this launch: -p %s selects %s, while %s, and %s's "+
		"pack declares no launch_selection, so a -p cannot move %s off the provider its own config names "+
		"(docs/design/model-lists-and-pickers.md MM-D30); %s shows its own model menu. To change the "+
		"provider it starts on, set its profile in your config and run `yolo host apply`.",
		c.agent, shquote.Quote(c.typedProfile), selects(launch), cfg, c.agent, c.agent, c.agent), false
}

// configuredPrimary is the primary profile the user-scope `profile` key selects for this launch's
// agent, "" when it selects none.
func (c *hostComposition) configuredPrimary() string {
	if len(c.configuredSet) == 0 {
		return ""
	}
	return c.configuredSet[0]
}

// hostSelectionProgram is the selected pack that installs bin with a declared launch selection, and
// that declaration: nil, nil when no selected pack declares one for bin.
func hostSelectionProgram(packs []*packload.Pack, bin string) (*packload.Pack, *packdecl.LaunchSelection) {
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.Bin == bin && in.LaunchSelection != nil {
				return p, in.LaunchSelection
			}
		}
	}
	return nil, nil
}

// hostSelection is what one launch hands its program of its LAUNCH SELECTION
// (packdecl.LaunchSelection; docs/design/model-lists-and-pickers.md MM-D30): the argv words that go
// right after argv[0], the variables set in its environment, and the words the disclosure names
// them with. A nil *hostSelection hands nothing, which is every launch with no -p.
type hostSelection struct {
	// pack and bin are the declaring pack and the program.
	pack, bin string
	// declared is whether the program's pack declares a launch selection; follows whether the
	// program runs on this launch's selection: the -p's, handed or agreeing with its file.
	declared, follows bool
	// argv and vars are what is handed; keys name it for the disclosure.
	argv []string
	vars []entrypoint.Var
	keys []string
	// surface is the program's config surface the selection is composed for, and typed the -p as
	// typed, for `yolo host env`'s line (scriptVars).
	surface, typed string
}

// launchSelection is the step: what this launch hands its program of the selection the -p chose,
// composed over this launch's wire tables and compared with what the configured profile composes
// over the same tables with the configured selection (configuredWireTables). Nil, handing nothing,
// with no -p (the program's file is the configured selection, which a wrapper always runs on) and in
// a jail (the jail's own render wrote the -p's selection into its files already: MM-D28 (9)); one
// that is not declared, handing nothing, where the program's pack declares no launch selection.
// environ is the program's environment as composed so far, which a document variable is merged
// into. Nothing here refuses the launch: every reason it hands nothing is a line handed to say, and
// the program then starts on its own file.
func (c *hostComposition) launchSelection(environ []string, say func(string)) *hostSelection {
	if c.typedProfile == "" || config.InJail() {
		return nil
	}
	p, spec := hostSelectionProgram(c.packs, c.agent)
	if spec == nil {
		return &hostSelection{bin: c.agent}
	}
	sel := &hostSelection{pack: p.Name, bin: c.agent, declared: true}
	keepsFile := func(why string) *hostSelection {
		say(fmt.Sprintf("%s, so %s starts on the selection its own config holds (~/%s), not on -p %s's. %s",
			why, c.agent, spec.Surface, shquote.Quote(c.typedProfile), c.selectionRemedy()))
		return sel
	}
	launch, err := entrypoint.HostLaunchSelection(p, *spec, paths.Home(),
		&entrypoint.HostInputs{Vars: c.wireTables(), Packs: c.packs}, say)
	if err != nil {
		return keepsFile(fmt.Sprintf("could not compose %s's selection for this launch (%v)", c.agent, err))
	}
	configured, err := entrypoint.HostLaunchSelection(p, *spec, paths.Home(),
		&entrypoint.HostInputs{Vars: c.configuredWireTables(), Packs: c.packs}, nil)
	if err == nil && launch.Same(configured) {
		// The -p selects what the configured profile does: the file already holds it, and handing
		// it again would override a choice the user made in the program since.
		sel.follows = true
		return sel
	}
	if launch.Empty() {
		return keepsFile(fmt.Sprintf("-p %s gives %s no selection: %s's pack composes none for it, as for a "+
			"provider %s cannot reach", shquote.Quote(c.typedProfile), c.agent, c.agent, c.agent))
	}
	if os.Getenv(entrypoint.NoLaunchFlagsEnv) == "1" {
		return keepsFile(fmt.Sprintf("%s=1 is set, which skips what packs add to a launch, and moving %s onto "+
			"a -p is one of them", entrypoint.NoLaunchFlagsEnv, c.agent))
	}
	argv, err := launch.Argv()
	if err != nil {
		return keepsFile(fmt.Sprintf("%s's selection cannot be spelled as its pack's words (%v)", c.agent, err))
	}
	vars, err := launch.Vars(environLookup(environ))
	if err != nil {
		return keepsFile(fmt.Sprintf("%s's selection cannot be handed in a variable (%v)", c.agent, err))
	}
	sel.argv, sel.vars, sel.keys, sel.follows = argv, vars, launch.Keys(), true
	sel.surface, sel.typed = spec.Surface, c.typedProfile
	return sel
}

// scriptVars is what `yolo host env` exports of the selection, and the lines it says: the
// variables of a selection handed in variables alone (the env form), each disclosed; and for one
// that needs argv, which a script cannot carry, nothing, with the line naming the launch that
// does carry it, since the variables without the argv would hand the program half a selection.
func (s *hostSelection) scriptVars() ([]agentenv.Var, []string) {
	if s == nil || !s.follows || (len(s.argv) == 0 && len(s.vars) == 0) {
		return nil, nil
	}
	if len(s.argv) > 0 {
		return nil, []string{fmt.Sprintf("-p %s moves %s through its command line (%s), which a script "+
			"cannot carry, so a %s started from this shell runs on the selection its own config holds "+
			"(~/%s). To run %s on it for one launch: `yolo host -p %s -- %s`", shquote.Quote(s.typed), s.bin,
			shquote.Join(s.argv), s.bin, s.surface, s.bin, shquote.Quote(s.typed), shquote.Quote(s.bin))}
	}
	vars := make([]agentenv.Var, 0, len(s.vars))
	for _, v := range s.vars {
		vars = append(vars, agentenv.Var{Key: v.Name, Value: v.Value})
	}
	return vars, s.varLines()
}

// selectionRemedy is the next step a launch that could not move its program names: the launch's
// profile, or its whole active set, in the user's config, and `yolo host apply`, which writes it into
// the program's own file.
func (c *hostComposition) selectionRemedy() string {
	value := fmt.Sprintf("%q", c.profile)
	if len(c.set) > 1 {
		quoted := make([]string, 0, len(c.set))
		for _, p := range c.set {
			quoted = append(quoted, fmt.Sprintf("%q", p))
		}
		value = "[" + strings.Join(quoted, ", ") + "]"
	}
	return fmt.Sprintf("To start %s there by default, name it in your config (\"profile\": {%q: %s}) and "+
		"run `yolo host apply`.", c.agent, c.agent, value)
}

// environLookup is a lookup over an environ slice, the last assignment of a name winning.
func environLookup(environ []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := "", false
		for _, kv := range environ {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				value, found = v, true
			}
		}
		return value, found
	}
}

// why is the disclosure's account of what the launch hands and why.
func (s *hostSelection) why() string {
	return fmt.Sprintf("-p %s's selection for %s, for this launch only, its config files untouched: %s",
		shquote.Quote(s.typed), s.bin, strings.Join(s.keys, ", "))
}

// rewrite is argv with the selection's words right after argv[0], so a user's own later flag of the
// same name still wins (MM-D22's rule, MM-D30), and the rewrite's disclosure in the pack flags'
// words (LaunchInjection.DisclosureLines), since a launch has no quiet mode. argv unchanged and no
// lines when there are no words.
func (s *hostSelection) rewrite(argv []string) ([]string, []string) {
	if s == nil || len(s.argv) == 0 || len(argv) == 0 {
		return argv, nil
	}
	out := append(append([]string{argv[0]}, s.argv...), argv[1:]...)
	inj := &packload.LaunchInjection{Pack: s.pack, Flags: append([]string{}, s.argv...),
		Before: append([]string{}, argv...), After: out, Why: s.why()}
	return out, inj.DisclosureLines()
}

// varLines is the disclosure of the variables the selection sets: each by name and what it carries,
// never its value, which a row can make long and the host launch log keeps.
func (s *hostSelection) varLines() []string {
	if s == nil || len(s.vars) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("yolo SET variables for %s, from pack %s, for -p %s's selection, for this "+
		"launch only, its config files untouched:", s.bin, s.pack, shquote.Quote(s.typed))}
	for _, v := range s.vars {
		line := "  " + v.Name + ": " + v.Carries
		if v.Merged {
			line += " (merged over your own value: the selection's keys win, your other keys are kept)"
		}
		lines = append(lines, line)
	}
	return lines
}

// applyEnv is environ with the selection's variables set.
func (s *hostSelection) applyEnv(environ []string) []string {
	if s == nil || len(s.vars) == 0 {
		return environ
	}
	vars := make([]agentenv.Var, 0, len(s.vars))
	for _, v := range s.vars {
		vars = append(vars, agentenv.Var{Key: v.Name, Value: v.Value})
	}
	return agentenv.Apply(environ, vars)
}
