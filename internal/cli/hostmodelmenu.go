package cli

// hostmodelmenu.go is a program's MODEL MENU at `yolo host --` (packdecl.ModelMenu, a term coined
// there; docs/design/model-lists-and-pickers.md §14.7, MM-D24 to MM-D28): the step a jail's
// launcher runs before it execs a program whose pack declares one, run here by the host launch
// against the program it is about to exec.
//
// THE FOUR PIECES, each the design's:
//
//   - THE LIST IS THE LAUNCH'S (MM-D24): the pack's own list derive, run in-process over this
//     launch's wire tables (entrypoint.HostModelList). No file is rendered.
//   - ONLY WHERE THE LAUNCH AND THE CONFIGURED PROFILE AGREE (MM-D25): a host `-p` reaches the
//     program's environment, not the config file `yolo host apply` writes its provider into, so a
//     menu built for a `-p` over another provider could list models the program is not running
//     on. Which should win is OQ-MM5, open; what is built is the intersection of its options.
//   - THE JAIL'S BUILD (MM-D26): modelmenu.Request, the one the jail's `yolo internal model-menu`
//     runs, with the list as an argument and the resolved target as the program.
//   - KEPT IN YOLO'S STATE WHILE A PROGRAM READS IT (MM-D27): modelmenu.Request.WriteIn, one menu
//     per cache key under paths.HostModelMenusDir, behind a shared lock the launch keeps for its
//     program's life.
//
// It knows no program: the declaration is the pack's, and the words it prints name the bin.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
// — no declaration, YOLO_NO_LAUNCH_FLAGS=1, a `-p` over another provider than the configured one,
// no list, or a build that failed. target is the resolved program, catalogEnv the environment its
// catalog run gets (the one the program itself will). Nothing here refuses the launch: every
// failure is a line on errw, and the program keeps its own menu.
func (c *hostComposition) modelMenu(target string, catalogEnv []string, errw io.Writer) *hostMenu {
	p, spec := hostMenuProgram(c.packs, c.agent)
	if spec == nil {
		return nil
	}
	// The jail's escape, and the same one: the menu's words are a pack's flag (MM-D22).
	if os.Getenv(entrypoint.NoLaunchFlagsEnv) == "1" {
		return nil
	}
	if line := c.menuProviderDisagreement(); line != "" {
		fmt.Fprintf(errw, "yolo host: %s\n", line)
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

// menuProviderDisagreement is MM-D25's line, "" when the launch may build its program a menu: no
// `-p` was typed, so the launch's selection is the configured one, or the `-p` selects the
// provider the configured profile does. Providers, not profile names, because the list derive
// reads the provider. When they differ the line says so, and that the program keeps its own menu,
// whether or not the `-p`'s provider has a list: what it reports is that the `-p` does not choose
// the provider the program runs on here (MM-D28).
func (c *hostComposition) menuProviderDisagreement() string {
	if c.typedProfile == "" {
		return ""
	}
	launch := packload.ProviderFor(c.resolved, c.profile)
	configured := packload.ProviderFor(c.resolved, c.configuredProfile)
	if launch == configured {
		return ""
	}
	selects := func(p string) string {
		if p == "" {
			return "no provider"
		}
		return p
	}
	cfg := fmt.Sprintf("the profile your config names for %s (%s) selects %s", c.agent,
		c.configuredProfile, selects(configured))
	if c.configuredProfile == "" {
		cfg = fmt.Sprintf("your config names no profile for %s", c.agent)
	}
	return fmt.Sprintf("%s gets no model menu from yolo this launch: -p %s selects %s, while %s. At the "+
		"host a -p does not change the provider %s runs on, which its own config names and `yolo host "+
		"apply` writes for the configured profile, so a menu for %s could list models %s is not using "+
		"(docs/design/model-lists-and-pickers.md OQ-MM5); %s shows its own model menu.",
		c.agent, shquote.Quote(c.typedProfile), selects(launch), cfg, c.agent, selects(launch), c.agent, c.agent)
}
