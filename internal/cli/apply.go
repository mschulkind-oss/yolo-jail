package cli

// apply.go is `yolo apply` — "make this environment match its description," split from
// "run something in it" (env-manager plan Phase 3, design §3.1). Today `yolo` means
// launch, and provisioning is a side effect of launching; `apply` names the make-it-so
// half so there is an answer to "set up my environment but don't run anything," and an
// answer at all at the host notch, where there is nothing to enter.
//
// Scope of THIS phase: the verb, its flags, and the notch routing. The heavy lifting
// differs by notch:
//   - jail: provision (build image, stage packs, render config) then exit. That is the
//     existing run pipeline minus the exec, and wiring a no-exec mode through it is
//     deferred (noted below) rather than stubbed — so at jail `apply` currently reports
//     what a launch WOULD provision and directs to `yolo` / `yolo apply --at host`.
//   - host: render the applicable config into the real home. That is Phase 4
//     (`apply --host`), gated on the host-render work; here it is recognized and routed
//     with an honest "not yet" rather than silently doing nothing.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func runApply(args []string) int {
	return applyMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

func applyMain(args []string, out, errw io.Writer, color bool) int {
	var at string
	var dryRun, sealed bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, applyUsage+"\n")
			return 0
		case a == "--at":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo apply: --at needs a value (jail|guest|host)\n")
				return 2
			}
			i++
			at = args[i]
		case hasPrefix(a, "--at="):
			at = a[len("--at="):]
		case a == "--host":
			at = "host" // shorthand for --at host
		case a == "--dry-run":
			dryRun = true
		case a == "--sealed":
			sealed = true
		default:
			fmt.Fprintf(errw, "yolo apply: unexpected argument %q\n\n%s\n", a, applyUsage)
			return 2
		}
	}

	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "yolo apply: %v\n", err)
		return 1
	}
	// --at overrides the configured notch for this invocation (the §4.1 escape valve);
	// otherwise use the configured confinement.
	notch := config.ResolveConfinement(cfg)
	if at != "" {
		n := config.Confinement(at)
		ok := false
		for _, k := range config.KnownConfinements {
			if n == k {
				ok = true
			}
		}
		if !ok {
			fmt.Fprintf(errw, "yolo apply: --at %q is not a confinement level (jail|guest|host)\n", at)
			return 2
		}
		notch = n
	}

	pr := richtext.Printer{W: out, Color: color}
	if sealed {
		return applySealed(out, errw, color)
	}

	switch notch {
	case config.ConfinementHost:
		// Phase 4 (`apply --host`) renders the applicable config into the real home.
		pr.Printf("[yellow]apply at the host notch is not built yet (env-manager plan " +
			"Phase 4). It will render your pack config into your real home (pure rmw, only " +
			"the keys yolo manages). For now, `yolo describe` shows what it would apply.[/yellow]")
		return 1
	case config.ConfinementGuest:
		pr.Printf("[yellow]apply at the guest notch is not built yet (env-manager plan " +
			"Phase 7 — the LSM-confined backend).[/yellow]")
		return 1
	default: // jail
		_ = dryRun
		pr.Printf("[bold]apply[/bold] at confinement [cyan]jail[/cyan].")
		pr.Printf("[dim]At the jail notch, provisioning happens as part of launch. Run " +
			"`yolo -- <cmd>` to provision and enter, or `yolo -- true` to provision and exit. " +
			"A dedicated provision-without-launch path is a follow-up (env-manager plan Phase 3 " +
			"leaves the no-exec jail provision to a later increment).[/dim]")
		pr.Printf("")
		return describeMain(nil, out, errw, color)
	}
}

// applySealed enumerates the input closure (env-manager design §3.3) and refuses if any
// UNDECLARED input shaped the environment. Sealing does not mean "no host reads" — a
// named-but-impure input (the user config, a pack's reads-host) is declared, nix's
// fixed-output derivation. It means no input that NOTHING names. The two undeclared
// inputs today are:
//   - yolo-jail.local.jsonc: auto-merged, gitignored, needs no include entry.
//   - an outstanding capture overlay: in-jail edits that outrank every declared layer,
//     yet nothing declares them (they are a staging area to promote, §3.3).
func applySealed(out, errw io.Writer, color bool) int {
	pr := richtext.Printer{W: out, Color: color}
	ws := workspaceRoot()

	var refusals []string
	// (1) yolo-jail.local.jsonc present anywhere in the workspace root.
	localPath := filepath.Join(ws, config.WorkspaceLocalConfigName)
	if _, err := os.Stat(localPath); err == nil {
		refusals = append(refusals,
			config.WorkspaceLocalConfigName+" is present and merges into the config, but "+
				"nothing declares it (it is gitignored, machine-local). Fold its keys into "+
				"yolo-jail.jsonc or remove it to seal.")
	}
	// (2) any capture surface carrying outstanding overlay keys.
	for _, s := range surfaceManifest().Surfaces() {
		if n := overlayKeyCount(s.Agent, s.Name); n > 0 {
			refusals = append(refusals, fmt.Sprintf(
				"%s/%s has %d captured in-jail edit(s) outranking the definition — "+
					"promote them into a pack or `yolo config reset %s --surface %s` to discard.",
				s.Agent, s.Name, n, s.Agent, s.Name))
		}
	}

	if len(refusals) > 0 {
		pr.Printf("[bold red]apply --sealed: refused — %d undeclared input(s):[/bold red]", len(refusals))
		for _, r := range refusals {
			pr.Printf("  [red]✗[/red] %s", r)
		}
		return 1
	}
	pr.Printf("[green]sealed[/green] — the environment is assembled only from declared inputs.")
	pr.Printf("[dim]Its `describe --hash` is now a reproducibility pin, not just a cache key.[/dim]")
	return 0
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

const applyUsage = `yolo apply — make this environment match its description, without running anything

  yolo apply                provision the environment at its configured confinement
  yolo apply --at <level>   … at a different notch (jail|guest|host) for this run
  yolo apply --host         shorthand for --at host: render your config into your real home
  yolo apply --sealed       refuse if any UNDECLARED input shaped the environment
                            (yolo-jail.local.jsonc, an outstanding capture overlay)
  yolo apply --dry-run      show what would change, write nothing

apply splits "make it so" from "run something in it": ` + "`yolo -- <cmd>`" + ` is
"apply, then exec." The host notch has no exec half — there apply IS the whole feature
(rendering your agent config into your real home). See ` + "`yolo describe`" + ` for what
the current description resolves to.`
