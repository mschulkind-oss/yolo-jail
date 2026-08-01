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
		// Phase 5 owns the closure enumeration + refusal; recognized here so `--sealed`
		// is not an "unknown flag" error before then.
		pr.Printf("[yellow]apply --sealed: the sealed closure check is not built yet " +
			"(env-manager plan Phase 5). Use `yolo describe --json` to inspect the current " +
			"config in the meantime.[/yellow]")
		return 1
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

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

const applyUsage = `yolo apply — make this environment match its description, without running anything

  yolo apply                provision the environment at its configured confinement
  yolo apply --at <level>   … at a different notch (jail|guest|host) for this run
  yolo apply --host         shorthand for --at host: render your config into your real home
  yolo apply --sealed       refuse if any UNDECLARED input shaped the environment (Phase 5)
  yolo apply --dry-run      show what would change, write nothing

apply splits "make it so" from "run something in it": ` + "`yolo -- <cmd>`" + ` is
"apply, then exec." The host notch has no exec half — there apply IS the whole feature
(rendering your agent config into your real home). See ` + "`yolo describe`" + ` for what
the current description resolves to.`
