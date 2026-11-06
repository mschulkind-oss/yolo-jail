package run

import (
	"fmt"
	goruntime "runtime"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// privateClientPlatform is the one setup a selected private HTTP client cell is prepared for in
// this slice (docs/design/pack-brokers-http-plan.md, "Task 2 first slice"): a local Podman jail on
// a linux/amd64 host. Any other setup refuses the cell; nothing falls back to an ordinary launcher.
const privateClientPlatform = "linux/amd64"

// hostPlatform is the host's own GOOS/GOARCH, a variable so a test on another host can stand in
// the supported one.
var hostPlatform = goruntime.GOOS + "/" + goruntime.GOARCH

// selectedPrivateClientCells is every selected private client of an approved HTTP front of this
// launch's selected packs, resolved as the keeper's prepared generation resolves it.
func (o *Options) selectedPrivateClientCells(packs []*packload.Pack) ([]packload.PrivateProgramEnvPlan, error) {
	var cells []packload.PrivateProgramEnvPlan
	for _, module := range packLoopholeDecls(packs) {
		binding, ok := o.approvedHTTPBindings[module.Name]
		if !ok || binding.Pack != module.Pack {
			continue
		}
		plans, err := packload.ResolvePrivateProgramEnv(packs, module.Name, module.Pack, true)
		if err != nil {
			return nil, err
		}
		cells = append(cells, plans...)
	}
	return cells, nil
}

// privateClientCellUnsupported names why rt cannot run a private client cell, or "".
func privateClientCellUnsupported(rt string) string {
	switch {
	case rt != "podman": // parity: Refused — the private client cell is prepared only for a local Podman jail in this slice
		return "it is prepared only for a local Podman jail, and this launch's runtime is " + rt
	case containerJailPlatform() != privateClientPlatform:
		return "it is prepared only for a " + privateClientPlatform + " jail, and this jail is " + containerJailPlatform()
	case hostPlatform != privateClientPlatform:
		return "it is prepared only on a " + privateClientPlatform + " host, and this host is " + hostPlatform
	}
	return ""
}

// privateClientRefusal is a selected private client cell that cannot be bound to a checked
// delivery: the launch stops before any consumer runs, naming the cell, the cause and the repair.
type privateClientRefusal struct {
	cell   packload.PrivateProgramEnvPlan
	cause  string
	repair string
}

const privateClientBaselineRepair = "restore the supported baseline runtime (a local Podman linux/amd64 jail with no runtime customization) and an entirely locked registry dependency graph for this program, then rerun `yolo` to reprepare it"

func (r privateClientRefusal) Error() string {
	if r.cell.Bin == "" {
		return "a selected private HTTP client: " + r.cause
	}
	return fmt.Sprintf("private HTTP client %s (pack %s, front %s): %s", r.cell.Bin, r.cell.Pack, r.cell.Front, r.cause)
}

func (r privateClientRefusal) Render(color bool) string {
	var b strings.Builder
	b.WriteString(richtext.Render("[bold red]"+richtext.Escape("ERROR: "+r.Error())+"[/bold red]", color) + "\n")
	b.WriteString("  Nothing falls back to an ordinary launcher or an earlier install of it.\n")
	b.WriteString("\n" + richtext.Render("[bold]Repair:[/bold]", color) + "\n")
	b.WriteString("  " + r.repair + "\n")
	return b.String()
}

// checkPrivateClientDeliveries binds each cell to the producer's checked entrypoint, or refuses.
func checkPrivateClientDeliveries(cells []packload.PrivateProgramEnvPlan, verified map[string]callbrokerinput.PreparedHTTPClientEntrypoint) ([]callbrokerinput.PreparedHTTPClientEntrypoint, *privateClientRefusal) {
	var out []callbrokerinput.PreparedHTTPClientEntrypoint
	for _, cell := range cells {
		c, ok := verified[cell.Bin]
		if !ok {
			return nil, &privateClientRefusal{cell: cell, cause: "the selected capture route delivered no checked capture of it", repair: privateClientBaselineRepair}
		}
		plan := callbrokerinput.PreparedHTTPClientPlan{Bin: cell.Bin, Pack: cell.Pack, Front: cell.Front, RecipePack: cell.RecipePack, RecipeIdentity: cell.RecipeIdentity}
		if !c.SameSelection(plan) {
			return nil, &privateClientRefusal{cell: cell, cause: "its checked delivery names another selected recipe",
				repair: "stop the jail if it is running, then rerun `yolo` so the selected recipe is prepared again"}
		}
		if err := c.ValidateDelivery(); err != nil {
			return nil, &privateClientRefusal{cell: cell, cause: err.Error(), repair: privateClientBaselineRepair}
		}
		out = append(out, c)
	}
	return out, nil
}

// bindPrivateClientCommand rewrites a top-level command whose first word is a selected private
// client's bin to start that bin's bound bootstrap argv (entrypoint.PrivateClientArgv), keeping
// the rest of the command. private names the bins that must be bound; a command naming one with
// no binding is an error, never the ordinary launcher. Any other command is returned unchanged.
func bindPrivateClientCommand(targetCmd string, bindings []entrypoint.PrivateClientBinding, private []string) (string, error) {
	bins := slices.Clone(private)
	for _, b := range bindings {
		bins = append(bins, b.Bin)
	}
	for _, bin := range bins {
		word := shquote.Quote(bin)
		if targetCmd != word && !strings.HasPrefix(targetCmd, word+" ") {
			continue
		}
		for _, b := range bindings {
			if b.Bin == bin {
				return shquote.Join(entrypoint.PrivateClientArgv(b)) + strings.TrimPrefix(targetCmd, word), nil
			}
		}
		return "", fmt.Errorf("private HTTP client %s has no verified binding in this jail", bin)
	}
	return targetCmd, nil
}

// freshPrivateClientCommand is the fresh launch's first-session command once its keeper is
// ready: bound through the entrypoints record the keeper published, read and verified exactly as
// an attach reads it. A private client that cannot be bound runs a refusal in the jail instead,
// so the session still ends through the jail's own lifecycle.
func (o *Options) freshPrivateClientCommand(cname, rt string, staged stagedPacks, targetCmd string) string {
	if len(o.privateClientEntrypoints) == 0 {
		return targetCmd
	}
	private := make([]string, 0, len(o.privateClientEntrypoints))
	for _, c := range o.privateClientEntrypoints {
		private = append(private, c.Bin)
	}
	setupName := o.Setup.Name
	if setupName == "" {
		setupName = o.SetupName
	}
	bindings, err := o.validateAttachedPrivateHTTP(cname, rt, setupName, staged.root, staged.packs)
	bound := targetCmd
	if err == nil {
		bound, err = bindPrivateClientCommand(targetCmd, bindings, private)
	} else if unbound, bindErr := bindPrivateClientCommand(targetCmd, nil, private); bindErr == nil && unbound == targetCmd {
		// Not a private client's command: an ordinary invocation is unchanged.
		return targetCmd
	}
	if err != nil {
		return shquote.Join([]string{"printf", "%s\\n", "yolo: refusing to start: " + err.Error() + " — stop the jail and rerun yolo to reprepare it"}) + " >&2; exit 1"
	}
	return bound
}
