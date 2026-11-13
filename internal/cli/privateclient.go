package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
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
	// The interpreter closure: the bootstrap runs only the Node bytes the capture was prepared
	// with (pack-brokers-http-plan.md, Task 2).
	entry.Binding.NodeSHA256 = "sha256:" + record.Resolution.Runtime.NodeDigest
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
		RuntimeRoot: runtimeRoot, Entrypoint: path.Join(installed, record.Resolution.BinPath), NodeSHA256: entry.Binding.NodeSHA256}
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
// so a test can observe the exec without replacing its process. privateClientMayBePublished and
// privateClientFetch are its publication prefilter and host exchange, variables so a test can name
// a published cell and stand in for a live host.
var (
	privateClientExec           = syscall.Exec
	privateClientStore          = entrypoint.PrivateClientStore
	privateClientMayBePublished = callbrokerinput.HTTPClientBinMayBePublished
	privateClientFetch          = callbroker.FetchHTTPClientPublication
)

// publishPrivateClient asks the host, over this jail's own endpoint for the bound generation's
// front, for the client's publication (pack-brokers-http-plan.md, Task 4), and returns environ with
// the client's three ordinary variables set to the local token, the front's HTTPS URL and a trust
// file holding only the front's pinned certificate. Any value those variables already had is
// dropped first, so the jail's environment cannot point the client elsewhere. A host refusal
// stops the client: a published cell never runs with a partial or stale publication.
func publishPrivateClient(environ []string, b entrypoint.PrivateClientBinding, getenv func(string) string) ([]string, error) {
	loophole, seen := "", 0
	for _, item := range strings.Split(getenv(paths.HTTPFrontGenerationsEnv), ",") {
		name, generation, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && generation == b.Generation {
			loophole, seen = name, seen+1
		}
	}
	if seen != 1 || loophole == "" || loophole == "." || loophole == ".." || strings.ContainsAny(loophole, "/\x00") {
		return nil, errors.New("this jail's " + paths.HTTPFrontGenerationsEnv + " names no single front for its generation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pub, err := privateClientFetch(ctx, paths.JailHostServicesDir+"/"+loophole+paths.ServiceEndpointExt, b.Generation, b.Bin)
	if err != nil {
		return nil, err
	}
	der, err := base64.StdEncoding.Strict().DecodeString(pub.CertificateBase64)
	if err != nil {
		return nil, errors.New("the host's publication carries no usable certificate")
	}
	trust, err := privateClientTrustPath(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(environ)+3)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name != pub.TokenVariable && name != pub.URLVariable && name != pub.TrustVariable {
			out = append(out, kv)
		}
	}
	return append(out, pub.TokenVariable+"="+pub.Token, pub.URLVariable+"="+pub.URL, pub.TrustVariable+"="+trust), nil
}

// runPrivateClientExec is the shipped bootstrap of a bound private client (`yolo internal
// private-client-exec`): it accepts exactly the bound argv, requires the jail's own binding of the
// bin to be the same one, re-verifies the entrypoint and runtime files in the read-only capture
// store, and execs Node on the verified entrypoint. It installs, refreshes and mints nothing.
//
// The interpreter closure is sealed (pack-brokers-http-plan.md, Task 2): PATH only locates a
// candidate Node, which runs only when its bytes are the bound NodeSHA256 and then through the
// verified descriptor, never by path; and the environment drops every variable that can change
// what Node loads (entrypoint.PrivateClientEnviron), NODE_OPTIONS and NODE_PATH among them.
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
	candidate, err := exec.LookPath("node")
	if err != nil {
		return refuse("no Node runtime is on PATH")
	}
	node, err := entrypoint.OpenPrivateClientNode(candidate, b.NodeSHA256)
	if err != nil {
		return refuse(err.Error())
	}
	defer node.Close()
	environ := entrypoint.PrivateClientEnviron(os.Environ())
	if privateClientMayBePublished(b.Bin) {
		if environ, err = publishPrivateClient(environ, b, getenv); err != nil {
			return refuse(b.Bin + "'s front could not be published to it: " + err.Error())
		}
	}
	// The descriptor is close-on-exec: the kernel opens the executable through it before closing
	// it, and Node then reads its own path from /proc/self/exe, the real file.
	self := "/proc/self/fd/" + strconv.Itoa(int(node.Fd()))
	if err := privateClientExec(self, append([]string{"node", entry}, rest...), environ); err != nil {
		return refuse(errors.Join(errors.New("exec failed"), err).Error())
	}
	return 0
}
