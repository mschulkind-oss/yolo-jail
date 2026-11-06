package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// ProgramCaptureRequest is the fresh launch's selected ordinary installation vector.
// Env is used only for launcher/MCP selection; none of its server arguments or values
// is sent to a producer. The producer resolves execution authority from user selection.
type ProgramCaptureRequest struct {
	Packs                          []*packload.Pack
	Env                            *entrypoint.Env
	Runtime, Platform, Fingerprint string
	RepoRoot, Workspace            string
	Fresh                          bool
	Act                            *ActInterrupt
	Stderr                         io.Writer
	UnsupportedRuntime             string
	// ImageIdentity is this launch's image identity, asked only when a program is selected, so a
	// kept "the image already provides it" verdict can answer without a producer jail. nil, or
	// ok=false, means unknown: every selected program then asks its producer.
	ImageIdentity func() (string, bool)
	// PrivateClients is every selected private HTTP client cell of an approved front
	// (selectedPrivateClientCells). The producer checks each one's delivery and records the
	// checked entrypoint in PrivateClientEntrypoints, by bin; a cell it cannot check it refuses.
	PrivateClients           []packload.PrivateProgramEnvPlan
	PrivateClientEntrypoints map[string]callbrokerinput.PreparedHTTPClientEntrypoint
}

// NodeFloorUnavailable is an observed provider result, not an installation/network failure.
// It travels only on failed preparation; it is never an artifact admission requirement.
type NodeFloorUnavailable struct {
	Owner, Bin, Floor, Available string
}

func (e *NodeFloorUnavailable) Valid() bool {
	return e != nil && packdecl.ValidPackName(e.Owner) && packdecl.ValidBinName(e.Bin) && packdecl.ValidNodeFloor(e.Floor) && strings.Trim(strings.TrimPrefix(e.Floor, "v"), "0123456789.") == "" && entrypoint.ValidCaptureNodeAvailability(e.Available)
}

func (e *NodeFloorUnavailable) Error() string {
	if !e.Valid() {
		return "invalid structured Node-floor failure — retry selected preparation"
	}

	return fmt.Sprintf("REFUSING to start this jail: pack %s, program %s requires Node >=%s; the observed mise release list has no satisfying version. Available: %s — lower the pack's Node floor or retry after its provider offers a satisfying release", e.Owner, e.Bin, e.Floor, e.Available)
}

func ProgramRuntimeFingerprint(repoRoot string, cfg *jsonx.OrderedMap) (string, error) {
	var inputs [][]byte
	for _, name := range []string{"flake.nix", "flake.lock"} {
		b, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			return "", err
		}
		inputs = append(inputs, b)
	}
	packages, _ := cfg.Get("packages")
	if packages == nil {
		packages = []any{}
	}
	b, err := json.Marshal(packages)
	if err != nil {
		return "", err
	}
	inputs = append(inputs, b)
	raw, _ := json.Marshal(inputs)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (o *Options) prepareOrdinaryPrograms(cfg *jsonx.OrderedMap, rt, repoRoot string, packs []*packload.Pack, channel *packChannel) int {
	if o.captureJail() {
		return 0
	}
	// A SELECTED PRIVATE HTTP CLIENT is bound to a checked delivery or the launch stops here, before
	// any consumer runs; the ordinary program fallbacks below never apply to it.
	cells, err := o.selectedPrivateClientCells(packs)
	refuse := func(r privateClientRefusal) int {
		out := o.pr(o.Stderr)
		_, _ = io.WriteString(out.rt.W, r.Render(out.rt.Color))
		return 1
	}
	if err != nil {
		return refuse(privateClientRefusal{cause: "its selected recipe cannot be resolved: " + err.Error(),
			repair: "repair the selected pack's program and env declarations, then rerun `yolo`"})
	}
	if len(cells) > 0 {
		cause := privateClientCellUnsupported(rt)
		if cause == "" && o.PreparePrograms == nil {
			cause = "this launch has no selected producer to prepare it"
		}
		if cause == "" && o.roBindsUnsupported(rt) != "" {
			cause = "the capture store cannot be mounted read-only here: " + o.roBindsUnsupported(rt)
		}
		if cause != "" {
			return refuse(privateClientRefusal{cell: cells[0], cause: cause, repair: privateClientBaselineRepair})
		}
		if r := privateClientLaunchFlagsRefusal(cells, packs); r != nil {
			return refuse(*r)
		}
	}
	if o.PreparePrograms == nil || o.roBindsUnsupported(rt) != "" {
		return 0
	}
	fingerprint, err := ProgramRuntimeFingerprint(repoRoot, cfg)
	if err != nil {
		if len(cells) > 0 {
			return refuse(privateClientRefusal{cell: cells[0], cause: "its runtime could not be resolved: " + err.Error(), repair: privateClientBaselineRepair})
		}
		if o.Update {
			o.pr(o.Stderr).print("ordinary program runtime could not be resolved — repair the flake source, then retry --update: " + err.Error())
			return 1
		}
		return 0
	}
	_, servers := agentServerTables(cfg, false, packs)
	miseTools := cfgMap(cfgMap(cfg, "install"), "mise")
	if miseTools == nil {
		// Direct callers that have not crossed the format-aware reader retain the legacy shape.
		miseTools = cfgMap(cfg, "mise_tools")
	}
	e := entrypoint.NewEnv(map[string]string{"YOLO_MISE_TOOLS": jsonDumpsOrEmptyObj(miseTools), "YOLO_MCP_SERVERS": jsonDumpsOrEmptyObj(servers)})
	reason := OrdinaryRuntimeUnsupported(cfg, o.Getenv("YOLO_STORE_PACKAGES") == "1")
	if channel != nil {
		compositions := []packload.EnvComposition{channel.scope.SharedEnv()}
		for _, agent := range channel.scope.Agents() {
			compositions = append(compositions, channel.scope.EnvFor(agent))
		}
		for _, composition := range compositions {
			for _, env := range composition.Entries() {
				if !env.Unset && ordinaryRuntimeEnvKey(env.Key) {
					reason = "the composed " + env.Key + " changes the consumer runtime environment"
				}
			}
		}
	}
	if reason != "" && len(cells) > 0 {
		return refuse(privateClientRefusal{cell: cells[0], cause: "its delivery is unsupported here: " + reason, repair: privateClientBaselineRepair})
	}
	verified := map[string]callbrokerinput.PreparedHTTPClientEntrypoint{}
	deliveries, err := o.PreparePrograms(ProgramCaptureRequest{Packs: packs, Env: e, Runtime: rt, Platform: containerJailPlatform(), Fingerprint: fingerprint, RepoRoot: repoRoot, Workspace: o.Workspace, Fresh: o.Update, Act: o.actInterrupt(), Stderr: o.Stderr, UnsupportedRuntime: reason, ImageIdentity: o.launchImageIdentity(repoRoot),
		PrivateClients: cells, PrivateClientEntrypoints: verified})
	if o.actInterrupt().Interrupted() {
		err = context.Canceled
	}
	if err != nil {
		var unavailable *NodeFloorUnavailable
		if errors.As(err, &unavailable) && unavailable.Valid() {
			fmt.Fprintln(o.Stderr, unavailable)
			return provision.RefusedStatus
		}
		out := o.pr(o.Stderr)
		// PD-NF1's refusal shape: a preparation names its own block (a newer version that failed
		// over a prepared one, or a program with nothing prepared); anything else gets the same
		// shape naming the act.
		var refusal interface{ Render(color bool) string }
		if !errors.As(err, &refusal) {
			refusal = preparationRefusal{err}
		}
		_, _ = io.WriteString(out.rt.W, refusal.Render(out.rt.Color))
		return 1
	}
	bound, refusal := checkPrivateClientDeliveries(cells, verified)
	if refusal != nil {
		return refuse(*refusal)
	}
	o.programCaptures = deliveries
	o.privateClientEntrypoints = bound
	return 0
}

// preparationRefusal is a failed ordinary preparation no program's own block names: the ERROR line,
// the facts, a blank line and the repair.
type preparationRefusal struct{ err error }

func (r preparationRefusal) Render(color bool) string {
	var b strings.Builder
	b.WriteString(richtext.Render("[bold red]"+richtext.Escape("ERROR: preparing this launch's programs failed")+"[/bold red]", color) + "\n")
	for _, l := range strings.Split(strings.TrimRight(fmt.Sprint(r.err), "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\n" + richtext.Render("[bold]Repair:[/bold]", color) + "\n")
	b.WriteString("  fix what the error names, then retry the launch:\n")
	b.WriteString("    yolo\n")
	return b.String()
}

// launchImageIdentity is the ordinary preparation's lazy view of this launch's image identity. It
// shares the launch's one evaluation (identityMemo, imageprewarm.go): when the prewarm already
// started it, this waits for the same answer; when nothing has asked yet, it installs the memo, so
// the image step that follows reuses this answer instead of evaluating again.
func (o *Options) launchImageIdentity(repoRoot string) func() (string, bool) {
	if repoRoot == "" {
		return nil
	}
	return func() (string, bool) {
		if o.imageIdentity == nil {
			o.imageIdentity = &identityMemo{}
		}
		return o.imageIdentity.eval(repoRoot)
	}
}

// OrdinaryRuntimeUnsupported is the conservative reuse boundary, not a launch
// refusal. The base image marker does NOT identify package/loader variants; these
// installations stay on their authorized bounded legacy route instead of inventing
// equality or widening the credential-free seal.
func OrdinaryRuntimeUnsupported(cfg *jsonx.OrderedMap, storePackages bool) string {
	install := cfgMap(cfg, "install")
	if install == nil {
		install = jsonx.NewOrderedMap()
	}
	packages, ok := install.Get("nix")
	if ok && packages != nil {
		b, _ := jsonx.DumpsCompact(packages)
		if b != "[]" {
			return "install.nix: changes the runtime variant beyond the base image identity"
		}
	}
	if storePackages {
		return "store-delivered packages/loader libraries are not identified by the base image marker"
	}
	if tools := cfgMap(install, "mise"); tools != nil {
		if _, ok := tools.Get("node"); ok {
			return "the configured Node toolchain is not the sealed producer's observed toolchain"
		}
	}
	if config.HasEnvEntries(cfg) {
		if config.HasEnvFileEntry(cfg) {
			return "file-backed env_sources or env.files has no sealed runtime identity; retain its authorized installation route"
		}
		if env := config.ResolveEnvSources("", cfg, nil); env != nil {
			for _, key := range env.Keys() {
				if ordinaryRuntimeEnvKey(key) {
					return "configured " + key + " can change the installation/runtime loader environment"
				}
			}
		}
	}
	return ""
}

func ordinaryRuntimeEnvKey(key string) bool {
	key = strings.ToUpper(key)
	switch key {
	case "PATH", "NODE_OPTIONS", "NODE_PATH", "NODE_EXTRA_CA_CERTS", "NODE_AUTH_TOKEN", "NPM_TOKEN",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NIX_SSL_CERT_FILE", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
		return true
	}
	return strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") || strings.HasPrefix(key, "NPM_CONFIG_")
}
