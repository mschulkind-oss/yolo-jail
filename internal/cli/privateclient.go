package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// privateClientDeliveryRefusal is a selected private HTTP client whose checked delivery does not
// match its selection, or that the capture route cannot deliver: the launch stops before any
// consumer runs, naming the cell, what differs, and its own repair.
type privateClientDeliveryRefusal struct {
	Cell   packload.PrivateProgramEnvPlan
	Cause  string
	Repair string
}

const privateClientBaselineRepair = "restore the supported baseline runtime and an entirely locked registry dependency graph for this program, then rerun `yolo` to reprepare it"

func (e *privateClientDeliveryRefusal) Error() string {
	return fmt.Sprintf("private HTTP client %s (pack %s, front %s): %s", e.Cell.Bin, e.Cell.Pack, e.Cell.Front, e.Cause)
}

func (e *privateClientDeliveryRefusal) Render(color bool) string {
	var b strings.Builder
	b.WriteString(richtext.Render("[bold red]"+richtext.Escape("ERROR: "+e.Error())+"[/bold red]", color) + "\n")
	b.WriteString("  Nothing falls back to an ordinary launcher or an earlier install of it.\n")
	b.WriteString("\n" + richtext.Render("[bold]Repair:[/bold]", color) + "\n")
	b.WriteString("  " + e.Repair + "\n")
	return b.String()
}

func privateClientMismatch(cell packload.PrivateProgramEnvPlan, what, repair string) error {
	return &privateClientDeliveryRefusal{Cell: cell, Cause: "its checked delivery does not match: " + what, Repair: repair}
}

// auditedPrivateClient is one private HTTP client delivery whose exact source was audited: the
// selected npm capture input (pack, bin and exact recipe) and the one registry tarball and SHA-512
// integrity its resolution may name. Only a delivery listed in auditedPrivateClients takes the
// private route; any other is refused as unsupported (docs/design/pack-brokers-http-plan.md, "Task 2
// first slice": the slice authorizes exactly one cell).
type auditedPrivateClient struct {
	Owner, Bin, Package        string
	SourceURL, SourceIntegrity string
}

// auditedPrivateClients is data, not a branch: the checks below read it the same way for any entry.
// The one entry is the semver 7.7.2 tarball whose entire 52-file source was audited (local semver
// mathematics, static relative requires, console/argv only; no runtime, optional or bundled
// dependencies, install scripts, binding.gyp or native binaries).
var auditedPrivateClients = []auditedPrivateClient{{
	Owner: "audited-semver", Bin: "semver", Package: "semver@7.7.2",
	SourceURL:       "https://registry.npmjs.org/semver/-/semver-7.7.2.tgz",
	SourceIntegrity: "sha512-RF0Fw+rO5AMf9MAyaRXI4AV0Ulj5lMHqVxxdSgiVbixSCXoEmmX/jk0CuJw4+3SqroYO9VoUh+HcuJivvtJemA==",
}}

const auditedPrivateClientRepair = "select this HTTP front only with an audited private client delivery (pack audited-semver, recipe semver@7.7.2), or remove this client's `env` contribution naming the front, then rerun `yolo`"

// auditedPrivateClientFor is the audited delivery of the selected capture input in, if any.
func auditedPrivateClientFor(in npmCaptureInput) (auditedPrivateClient, bool) {
	for _, a := range auditedPrivateClients {
		if a.Owner == in.Owner && a.Bin == in.Bin && a.Package == in.Package {
			return a, true
		}
	}
	return auditedPrivateClient{}, false
}

// refuseUnauditedPrivateClient refuses, before anything is prepared, a private client whose
// selected recipe no audited delivery names.
func refuseUnauditedPrivateClient(cell packload.PrivateProgramEnvPlan, in npmCaptureInput) error {
	if _, ok := auditedPrivateClientFor(in); ok {
		return nil
	}
	return &privateClientDeliveryRefusal{Cell: cell, Repair: auditedPrivateClientRepair,
		Cause: "its delivery is unsupported: no audited private client delivery names " + in.Owner + "/" + in.Bin + " at " + in.Package}
}

// npmRegistryTarball is the public registry's tarball URL of name at version, the one audited
// source a private client's npm recipe may resolve to.
func npmRegistryTarball(name, version string) string {
	return "https://registry.npmjs.org/" + name + "/-/" + path.Base(name) + "-" + version + ".tgz"
}

// verifyPrivateClientDelivery checks one selected private client's ordinary delivery before any
// generation is minted: the selected recipe, platform and runtime identity, the exact audited
// release and source (URL and SHA-512 integrity), and the captured entrypoint and runtime files
// in the admitted, read-only capture entry. It returns the checked entrypoint, unbound to a
// generation (the keeper binds it).
func verifyPrivateClientDelivery(cell packload.PrivateProgramEnvPlan, in npmCaptureInput, record npmCaptureRecord,
	delivery entrypoint.ProgramCaptureDelivery, platform string, packs []*packload.Pack) (callbrokerinput.PreparedHTTPClientEntrypoint, error) {
	var zero callbrokerinput.PreparedHTTPClientEntrypoint
	recipeRepair := "stop the jail if it is running, then rerun `yolo` so the selected recipe is prepared again"
	if cell.Bin != in.Bin || cell.RecipePack != in.Owner {
		return zero, privateClientMismatch(cell, "the prepared program is "+in.Owner+"/"+in.Bin, recipeRepair)
	}
	identity, found := "", 0
	for _, p := range packs {
		if p == nil || p.Decl == nil || p.Name != in.Owner {
			continue
		}
		for _, inst := range p.Decl.InstallContributions() {
			if inst.Bin != in.Bin {
				continue
			}
			found++
			if candidate, ok := npmCaptureInputFor(p.Name, inst); ok && candidate == in {
				identity, _ = packload.ProgramRecipeIdentity(inst)
			}
		}
	}
	if found != 1 || identity == "" || identity != cell.RecipeIdentity {
		return zero, privateClientMismatch(cell, "the prepared recipe is not the selected one", recipeRepair)
	}
	if delivery.Declared != in.Package || delivery.Platform != platform || record.Resolution == nil {
		return zero, privateClientMismatch(cell, "the delivery names another recipe or platform", recipeRepair)
	}
	inputs, err := record.Resolution.inputs()
	if err != nil {
		return zero, privateClientMismatch(cell, err.Error(), privateClientBaselineRepair)
	}
	compat, err := inputs.Identity()
	if err != nil || compat != delivery.Compatibility || compat != record.Identity || inputs.Runtime != delivery.Runtime || inputs.Platform != platform {
		return zero, privateClientMismatch(cell, "the delivery's runtime identity is not the prepared one", privateClientBaselineRepair)
	}
	name, selector, err := npmCaptureSpec(in.Package)
	if err != nil || selector != record.Resolution.Release {
		return zero, privateClientMismatch(cell, "its recipe pins no exact audited release (it declares "+in.Package+", which resolved "+record.Resolution.Release+")",
			"pin the pack's program to the audited exact release, then rerun `yolo` to reprepare it")
	}
	audited, ok := auditedPrivateClientFor(in)
	if !ok {
		return zero, refuseUnauditedPrivateClient(cell, in)
	}
	url, integrity, ok := strings.Cut(record.Resolution.Source, "|")
	if !ok || url != npmRegistryTarball(name, selector) || url != audited.SourceURL {
		return zero, privateClientMismatch(cell, "its source is not the registry's audited tarball of "+in.Package, privateClientBaselineRepair)
	}
	if integrity != audited.SourceIntegrity {
		return zero, privateClientMismatch(cell, "its source integrity is not the audited SHA-512 of "+in.Package, privateClientBaselineRepair)
	}
	entry := callbrokerinput.PreparedHTTPClientEntrypoint{Bin: cell.Bin, Pack: cell.Pack, Front: cell.Front, RecipePack: cell.RecipePack,
		RecipeIdentity: cell.RecipeIdentity, Declared: delivery.Declared, Platform: delivery.Platform, Runtime: delivery.Runtime,
		Compatibility: delivery.Compatibility, Release: record.Resolution.Release, SourceURL: url, SourceIntegrity: integrity,
		Bootstrap: entrypoint.PrivateClientBootstrap}
	entry.Binding.Bin = cell.Bin
	if err := entry.ValidateDelivery(); err != nil {
		return zero, privateClientMismatch(cell, err.Error(), privateClientBaselineRepair)
	}

	// The admitted capture: the same entry and manifest the ordinary materialize would serve.
	store := &capture.Store{Dir: paths.CapturesDir()}
	admitted, _, err := resolveProgramCaptureFor(store, in.Bin, platform, compat)
	if err != nil {
		return zero, &privateClientDeliveryRefusal{Cell: cell, Cause: "no admitted capture holds its delivery: " + err.Error(), Repair: privateClientBaselineRepair}
	}
	manifest, err := capture.ReadManifest(admitted.Root)
	if err == nil {
		err = validateProgramManifest(manifest, platform, npmCaptureArtifacts(in, compat))
	}
	if err != nil {
		return zero, privateClientMismatch(cell, "its capture manifest: "+err.Error(), privateClientBaselineRepair)
	}
	tree := capture.TreeDir(admitted.Root)
	runtimeRoot := ".local/lib/yolo-npm/" + compat
	installed := runtimeRoot + "/node_modules/" + name
	pkg, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(installed), "package.json"))
	var manifestVersion struct{ Version string }
	if err != nil || json.Unmarshal(pkg, &manifestVersion) != nil || manifestVersion.Version != selector {
		return zero, privateClientMismatch(cell, "the captured package is not release "+selector, privateClientBaselineRepair)
	}
	entry.Binding = entrypoint.PrivateClientBinding{Bin: cell.Bin, Generation: strings.Repeat("0", 32), CaptureKey: admitted.Key,
		RuntimeRoot: runtimeRoot, Entrypoint: path.Join(installed, record.Resolution.BinPath)}
	if entry.Binding.EntrypointSHA256, err = entrypoint.PrivateClientFileDigest(filepath.Join(tree, filepath.FromSlash(entry.Binding.Entrypoint))); err != nil {
		return zero, privateClientMismatch(cell, "its captured entrypoint "+record.Resolution.BinPath+" is not a regular file", privateClientBaselineRepair)
	}
	if entry.Binding.RuntimeFiles, err = entrypoint.PrivateClientRuntimeDigest(tree, runtimeRoot); err != nil {
		return zero, privateClientMismatch(cell, "its captured runtime files: "+err.Error(), privateClientBaselineRepair)
	}
	// The same check the bootstrap repeats in the jail, over the same admitted entry.
	if _, err := entrypoint.VerifyPrivateClient(store.Dir, entry.Binding); err != nil {
		return zero, privateClientMismatch(cell, err.Error(), privateClientBaselineRepair)
	}
	entry.Binding.Generation = ""
	return entry, nil
}

// privateClientExec and privateClientStore are the bootstrap's exec and in-jail store, variables
// so a test can observe the exec without replacing its process.
var (
	privateClientExec  = syscall.Exec
	privateClientStore = entrypoint.PrivateClientStore
)

// runPrivateClientExec is the shipped bootstrap of a bound private client (`yolo internal
// private-client-exec`): it accepts exactly the bound argv, requires the jail's own binding of the
// bin to be the same one, re-verifies the entrypoint and runtime files in the read-only capture
// store, and execs Node on the verified entrypoint. It installs, refreshes and mints nothing.
func runPrivateClientExec(args []string, getenv func(string) string) int {
	refuse := func(msg string) int {
		fmt.Fprintln(os.Stderr, "yolo: private client: "+msg+" — stop the jail and rerun `yolo` to reprepare it")
		return 1
	}
	b, rest, err := entrypoint.ParsePrivateClientArgs(args)
	if err != nil {
		return refuse(err.Error())
	}
	bound, err := entrypoint.ParsePrivateClients(getenv(entrypoint.PrivateHTTPClientsEnv))
	if err != nil {
		return refuse(err.Error())
	}
	match := false
	for _, candidate := range bound {
		if candidate.Bin == b.Bin {
			match = candidate == b
		}
	}
	if !match {
		return refuse(b.Bin + " is not bound to this generation and entrypoint in this jail")
	}
	entry, err := entrypoint.VerifyPrivateClient(privateClientStore, b)
	if err != nil {
		return refuse(err.Error())
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return refuse("no Node runtime is on PATH")
	}
	if err := privateClientExec(node, append([]string{"node", entry}, rest...), os.Environ()); err != nil {
		return refuse(errors.Join(errors.New("exec failed"), err).Error())
	}
	return 0
}
