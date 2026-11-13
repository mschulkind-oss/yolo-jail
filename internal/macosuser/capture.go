package macosuser

// capture.go is the macos-user half of INSTALL CAPTURE (docs/design/program-delivery.md §6.3,
// docs/reference/install-capture.md slice 6): run a vendor installer once, against a throwaway home,
// under the narrowed Seatbelt profile in seatbeltcapture.go, and leave an entry-shaped
// proto-entry the host act admits into the machine store.
//
// # The shape, and the two things that make it different from a launch
//
//	prepare    a throwaway staging tree on NEUTRAL GROUND, shared-group ACLs
//	stage      the yolo binary + this launch's pack trees into the host-owned staging root
//	profile    SeatbeltCaptureProfile over the staging tree — the shared home is DENIED
//	bootstrap  `darwin-bootstrap` into the STAGING HOME, so the generated launcher exists there
//	drive      `capture-run` under sandbox-exec, HOME=<staging>, running that launcher
//
// **The staging tree is neutral ground, not <CapturesDir>/staging.** On the container backends
// the capture workspace IS the store's staging dir, because the jail reaches it through a bind
// and `rename(2)` compares the mount. Neither half holds here. `paths.CapturesDir()` is under the
// INVOKING user's home, and this backend exists to keep the sandbox uid out of that home — the
// same reason StagePackCommands copies packs to /var instead of pointing the sandbox at
// ~/.local/share/yolo-jail. Granting `_yolojail` a writable subtree inside the admin's home would
// also make the machine-wide CAS reachable from a program yolo is about to run for the first
// time, which is the one directory a captured installer must never be able to write. So the
// capture writes to a shared-group tree under CaptureRootDefault and the HOST moves the finished
// proto-entry into the store afterwards — see internal/cli/capturemacos.go, which owns that move
// and refuses rather than copying if the two are not on one mount.
//
// **The bootstrap runs against the staging home.** The capture must run THE GENERATED LAUNCHER
// (install-capture.md slice 3(d)) — a second implementation of download-then-run would capture
// bytes a launch would never have produced — and the launcher only exists in a home the
// bootstrap has generated into. So `buildBootstrapEnv` takes the home as a parameter and this
// path passes the staging home where a launch passes SandboxHome().
//
// # THE RUN-AS-YOU MODE (macos_user_mode "self", slice 9 of docs/design/macos-user-model-plan.md)
//
// The same five steps, run as the invoking user: every crossing is /usr/bin/env alone (RunAs), the
// staging tree is yours, 0700, with no group or access list (SelfCaptureStagingCommands), the env
// and CA files get no grant, the bootstrap is confined under the capture's profile, and that profile
// is the deny-by-default one with the machine root and the shared store — your agents' logins —
// denied (SeatbeltSelfCaptureProfile). The gates ask no account and no run-as rule. A fork's build
// takes the same mode, under the sealed variant. NOT MEASURED on a Mac: macOS CI loads the account
// capture profile only (integration/macosusercaptureseatbelt_test.go).
//
// # MEASURED vs. DESIGNED-AGAINST-READ-CODE
//
// MEASURED, by the tests beside this file, on any OS: every artifact below is a pure function
// and its bytes are pinned — the command lists, the profile, the two argvs, and the invariants
// that connect them. CapturePlanInvariants is the gate that fails if the profile is swapped for
// the session one or the bootstrap is pointed at the shared home.
//
// MEASURED ON HARDWARE, 2026-09-11: the whole recording half, in one pass, on an Apple Silicon Mac
// (macOS 26.5, yolo 0.8.0+1336.gecb17e8c). `yolo capture claude` ran every stage listed above —
// staging tree, bootstrap into the STAGING home, the generated launcher driving the real vendor
// installer, then `capture-run: 10 paths (3 renamed, 0 copied)` and the host act moving the
// proto-entry into the store as entry ceb51e9936131b0a. So this file's Seatbelt profile HAS been
// loaded by a kernel, and it is capture's own profile rather than the session one — which is the
// thing CapturePlanInvariants exists to keep separate. Until that run, this comment said "NOT
// MEASURED, anywhere: that any of it works on a Mac. No Seatbelt profile has been loaded by a
// kernel" — true when written, then stale in two stages (the session profile was measured
// 2026-09-10 by docs/plans/runbooks/macos-user-manual-checks.md item 2).
//
// STILL NOT MEASURED: the MATERIALIZE half. Its rewrite (install-capture.md hand-off H2,
// internal/capture/rewrite.go) is built and measured on Linux against temp dirs standing in for
// this backend's two homes. Since hand-off H4 a launch here reaches it: the host CLI picks each
// selected installer program's entry, the launch stages a sealed, host-owned copy of it under the staging
// root (StagedCapturesRoot, StageCaptureCommands), and the bootstrap bakes that store into the
// generated launcher, whose `_try_materialize` hands it to `capture-materialize`; and a launch
// auto-captures what the store lacks (internal/cli/run/autocapture.go). No Mac has run that
// path. Podman-in-podman cannot exercise this backend at all, so no nested jail will ever cover
// any of it. The hardware checklist is in install-capture.md's slice 6 section.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const (
	// captureHomeLeaf and captureOutLeaf split one staging tree into the home the installer
	// runs against and the entry-shaped directory the delta is moved into.
	//
	// SIBLINGS, not nested. The out dir must be on the same MOUNT as the surfaces for the
	// delta move to be a rename (capture.Result.Copied is what a caller pays otherwise), and
	// it must not be INSIDE a capture surface or the capture would capture itself — the
	// driver refuses that outright. Two children of one directory satisfies both without
	// depending on where either one happens to sit.
	captureHomeLeaf = "home"
	captureOutLeaf  = "out"
	// captureTmpLeaf is a run-as-you capture's TMPDIR (CaptureStagingTmp), a sibling of the two
	// above, so nothing in it is under a capture surface of the staging home.
	captureTmpLeaf = "tmp"
	// accountCaptureTempRoot is the /var/folders tree the account capture's profile grants
	// (SeatbeltCaptureProfile), named in its temp-validation context (MMP-D36).
	accountCaptureTempRoot = "/private/var/folders"

	// captureScanFlag asks the driver for the FULL absolute-reference scan. It is not
	// optional on this backend: the staging path is not the final home path, so a capture
	// whose references were never enumerated is admitted `relocatable:false` and refuses to
	// materialize into /Users/_yolojail — which is the only home it will ever be asked for.
	captureScanFlag = "--scan-content-refs"
)

// CaptureRootDefault is the neutral root every install capture stages under:
// /Users/Shared/yolo-captures.
//
// NEUTRAL GROUND, for the same reason a workspace must be (HomeContaining, and the plan
// invariant that rejects a home-dir workspace): a directory inside a user's home shared with the
// sandbox uid is the thing this backend refuses to have. Under /Users/Shared rather than beside
// the store so both the invoking user and `_yolojail` can reach it through the shared-group ACL
// that already exists for workspaces.
//
// A SIBLING of SharedRootDefault, not a child. A capture staging tree is not a workspace, and
// putting it under the workspace root would make it look like one to everything that enumerates
// them (`yolo yolo-account share`, a human running `ls`).
func CaptureRootDefault() string { return "/Users/Shared/yolo-captures" }

// CaptureStagingRoot is the per-program staging tree: <captureRoot>/<bin>. An empty captureRoot
// means CaptureRootDefault().
//
// Keyed by BIN, matching the store's staging id and the per-program capture lock: two captures
// of different programs are independent, and one shared directory would serialise them for no
// reason. The `yolo capture` flock is what stops two captures of the SAME program colliding here.
func CaptureStagingRoot(captureRoot, bin string) string {
	if captureRoot == "" {
		captureRoot = CaptureRootDefault()
	}
	return filepath.Join(captureRoot, bin)
}

// CaptureStagingHome is the throwaway HOME an install capture runs against.
func CaptureStagingHome(stagingRoot string) string {
	return filepath.Join(stagingRoot, captureHomeLeaf)
}

// CaptureStagingTmp is a run-as-you capture's or build's own TMPDIR (MMP-D21): inside the staging
// tree, which its profile makes writable, because the deny-by-default profile closes the shared
// /tmp (OQ-MM8) that `go build`, `mktemp -d` and installers fall back to when TMPDIR is unset. It
// is made with the tree and removed with it.
func CaptureStagingTmp(stagingRoot string) string {
	return filepath.Join(stagingRoot, captureTmpLeaf)
}

// CaptureStagingOut is the ENTRY-SHAPED directory the driver fills: <out>/tree plus the manifest
// beside it, which is exactly what capture.Store.AdmitEntry consumes.
func CaptureStagingOut(stagingRoot string) string {
	return filepath.Join(stagingRoot, captureOutLeaf)
}

// CaptureOptions are the inputs the front door resolves for one install capture.
type CaptureOptions struct {
	// Bin is the program whose pack-declared installer is being captured. It is a path
	// segment (packdecl.ValidBinName has already vetted it at the `yolo capture` front door).
	Bin string
	// Config is the loaded jail config — the same one a launch uses, because the bootstrap
	// this runs is the same bootstrap.
	Config *jsonx.OrderedMap
	// SelfExe is the running yolo binary (os.Executable()), staged for the sandbox to
	// self-exec as both the bootstrap and the capture driver.
	SelfExe string
	// HostPackRoot is the host-side staged pack tree. Without it the bootstrap renders no
	// pack surfaces, so no launcher for Bin exists and the capture has nothing to run.
	HostPackRoot string
	// SandboxEnv is the composed launch env (git identity, TERM, the profile/provider
	// channel) — layered into both the bootstrap env and the driver env exactly as a launch
	// layers it, so the capture jail is the jail a launch produces.
	SandboxEnv *jsonx.OrderedMap
	// HostUser is the invoking (admin) user, who owns the staging tree so the host half can
	// move the finished proto-entry out of it. "" leaves the chown off the command list.
	HostUser string
	// CaptureRoot overrides CaptureRootDefault(); "" uses it. A test seam and an escape
	// hatch, not a config key.
	CaptureRoot string
	// Darwin is the already-materialized native `packages:` result, or nil.
	//
	// `yolo capture` passes nil today and that is a stated gap, not an oversight: a capture
	// would otherwise pay a full native nix build to run one shell script from a CDN. The
	// cost is that an installer needing a `packages:`-declared tool fails inside the capture
	// instead of finding it. The seam is here so wiring it later is a caller change.
	Darwin *Darwin
	// BlockedTools are the selected packs' blocked-tool declarations, threaded into the
	// staging home's YOLO_BLOCK_CONFIG exactly as a launch threads them.
	//
	// IT HAS TO BE PASSED, not defaulted. Core blocks nothing on its own any more — the
	// `grep -r`/`find` rules are a pack contribution — so a capture that left this nil
	// would bootstrap a staging home with NO blockers while the launch it claims to
	// reproduce has them. The capture would then record an installer run under a shim set
	// no real launch ever has, which is the one difference between the two homes this file
	// exists to prevent (see the bootstrap paragraph in the file comment).
	BlockedTools []packload.BlockedTool
	// CATrust is the TLS trust the capture runs its installer under (cabundle.go): the same
	// composition a launch makes, so an installer behind a corporate CA reaches its CDN as the
	// launch's own tools do. RunCaptureAct composes it when the capture has a tool profile
	// (Darwin), which `yolo capture` does not pass today; the zero value composes none.
	CATrust CATrust
	// Mode is who the capture runs as (mode.go), read by cli from the user-scope
	// `macos_user_mode` as a launch reads it; the zero value is the second account. HostHome is
	// the invoking user's resolved home, which places the machine root and the shared store the
	// run-as-you capture keeps the installer out of (LaunchHomes); DarwinUserTempDir is their
	// DARWIN_USER_TEMP_DIR, which its profile grants under the shared-/tmp ruling. The account
	// mode reads neither. (slice 9 of docs/design/macos-user-model-plan.md)
	Mode              Mode
	HostHome          string
	DarwinUserTempDir string
	// SharedTmp is the user-scope `macos_user_shared_tmp` hatch (OQ-MM8, MMP-D18), read by cli as a
	// launch reads it: the run-as-you capture profile then grants the shared /tmp. The account mode
	// ignores it.
	SharedTmp bool
}

// captureRunAs is a capture's crossing (mode.go, MMP-D4) and the homes its profile is built from.
// The account mode's is RunAs{}, today's byte for byte. The run-as-you mode's crosses through
// /usr/bin/env alone, as the invoking user, with HOME the staging home and the machine root, the
// shared store and the real home LaunchHomes places for that user — never a workspace's, since a
// capture has none.
func captureRunAs(opts CaptureOptions, stagingHome string) RunAs {
	if opts.Mode != ModeSelf {
		return RunAs{}
	}
	// An unnamed home stays unnamed (resolvePathAbs would make "" the cwd), and
	// capturePlanProblems refuses the plan for it.
	hostHome := opts.HostHome
	if hostHome != "" {
		hostHome = resolvePathAbs(hostHome)
	}
	homes := LaunchHomes(ModeSelf, stagingHome, hostHome)
	if hostHome == "" {
		homes.Machine, homes.Shared = "", ""
	}
	return RunAs{Mode: ModeSelf, Homes: homes, User: opts.HostUser}
}

// CapturePlan is the fully-resolved, ordered artifacts + commands for one install capture — the
// analogue of RunPlan, and a real gate rather than a pretty-printer (CapturePlanInvariants).
type CapturePlan struct {
	// RunAs is the capture's crossing into the sandbox (mode.go, MMP-D4): the second account, or
	// in the run-as-you mode you, with the homes its profile keeps the installer out of
	// (captureRunAs).
	RunAs RunAs
	Bin   string
	Cname string
	// StagingRoot is the throwaway tree; StagingHome and OutDir are its two children.
	StagingRoot string
	StagingHome string
	OutDir      string
	ProfilePath string
	Seatbelt    string
	StagedDir   string
	StagedYolo  string
	// PackRoot is the sealed staged pack tree the bootstrap renders from, or "".
	PackRoot string
	// PrepareCommands provision the staging tree; StageCommands stage the binary and the pack
	// trees; CleanupCommands remove what the capture leaves behind. All three run as the host
	// user, never under sudo (docs/design/macos-user-privilege-lifecycle-plan.md, task 6).
	PrepareCommands [][]string
	StageCommands   [][]string
	CleanupCommands [][]string
	// ScratchGuestCommands are PL-7's fallback for the staging tree: when the host cannot remove
	// it, the yolo account, which owns what the installer wrote, makes it writable and removes
	// it through the one run-as rule, and the host then removes the rest (removeCaptureScratch).
	ScratchGuestCommands [][]string
	// BootstrapArgv generates the STAGING HOME's shims, launchers and pack surfaces.
	BootstrapArgv []string
	// DriverArgv runs `yolo internal capture-run` under sandbox-exec as the yolo account.
	DriverArgv []string
	// EnvFile is this capture's session env file — the same mechanism a launch uses
	// (envfile.go), for the same reason: the capture's composed env is the launch's
	// profile/provider channel, and that channel carries hydrated provider CREDENTIALS
	// (internal/cli/run/profilechannel.go's shape vars). They rode the driver argv in
	// cleartext until 2026-09-13. EnvFileContent is what to write into it; the two are ""
	// together, and the three command lists keep the same before/after ordering RunPlan's do.
	EnvFile              string
	EnvFileContent       string
	EnvFileCommands      [][]string
	EnvFileGrantCommands [][]string
	// CATrust is the TLS trust the capture composed (CaptureOptions.CATrust). CABundleFile and
	// CAExtrasFile are its CA files beside its env file, with what to write into each
	// (cabundle.go); each "" when no variable names it. CleanupCommands remove both. CAFollows is
	// the bundle variable the capture's env set, which the other four then name too, or "".
	CATrust          CATrust
	CABundleFile     string
	CABundleContent  string
	CAExtrasFile     string
	CAExtrasContent  string
	CAFollows        string
	DarwinPathPrefix []string
	// OffendingHome is the user home containing StagingRoot, when there is one — the same
	// neutral-ground check a launch makes about its workspace, applied to the staging tree.
	OffendingHome    string
	OffendingHomeSet bool
}

// BuildCapturePlan assembles the whole capture plan (pure — no shelling out, no filesystem).
func BuildCapturePlan(opts CaptureOptions) CapturePlan {
	return buildCapturePlanAt(opts, CaptureStagingRoot(opts.CaptureRoot, opts.Bin))
}

// buildCapturePlanAt is BuildCapturePlan over a staging tree its caller names: an installer's,
// keyed by its bin, or a fork's build's, keyed by the build (ForkBuildStagingRoot).
func buildCapturePlanAt(opts CaptureOptions, stagingRoot string) CapturePlan {
	darwinPrefix := []string{}
	if opts.Darwin != nil {
		darwinPrefix = append(darwinPrefix, opts.Darwin.PathPrefix...)
	}
	stagingHome := CaptureStagingHome(stagingRoot)
	// THE CROSSING (captureRunAs): the second account, or, in the run-as-you mode, you.
	runAs := captureRunAs(opts, stagingHome)
	self := runAs.Mode == ModeSelf
	outDir := CaptureStagingOut(stagingRoot)
	cname := cnameFor(stagingRoot, "")
	profilePath := SessionProfilePath(cname, "")

	// Git identity, read out of the launch env by prefix — the same way BuildRunPlan reads
	// it. A capture makes no commits, but the bootstrap's configureGit runs either way and a
	// staging home missing it would be one more difference between the capture jail and the
	// jail whose bytes it claims to record.
	gitIdentity := jsonx.NewOrderedMap()
	if opts.SandboxEnv != nil {
		for _, k := range opts.SandboxEnv.Keys() {
			if strings.HasPrefix(k, "YOLO_GIT") {
				v, _ := opts.SandboxEnv.Get(k)
				gitIdentity.Set(k, v)
			}
		}
	}

	packRoot := ""
	if opts.HostPackRoot != "" {
		packRoot = StagedPackTreeRoot(cname, opts.HostPackRoot, "")
	}
	captureEnv := opts.SandboxEnv
	if captureEnv == nil {
		captureEnv = jsonx.NewOrderedMap()
	}
	// A capture owns these roots; never carry composed launch metadata into its program
	// launchers. The same resolved value is passed to the bootstrap and written into the
	// driver's session environment so the generated launcher evaluates this capture's policy.
	captureEnv = withoutEnvVar(captureEnv, paths.MacosUserTmpdirContextEnv)
	if opts.Mode == ModeAccount || opts.Mode == ModeSelf {
		validationContext := SandboxTmpdirValidationContext{
			Mode: opts.Mode, Workspace: stagingRoot, Home: stagingHome,
		}
		if self {
			validationContext.DarwinUserTempDir = opts.DarwinUserTempDir
			validationContext.SharedTmp = opts.SharedTmp
		} else {
			// The account capture's (allow default) profile grants writes to /tmp and all of
			// /var/folders (SeatbeltCaptureProfile), so its context names exactly those: the
			// launcher-time check refuses only what the profile would (MMP-D36).
			validationContext.DarwinUserTempDir = accountCaptureTempRoot
			validationContext.SharedTmp = true
		}
		if contextJSON, err := MarshalSandboxTmpdirValidationContext(validationContext); err == nil {
			captureEnv = withEnvVar(captureEnv, paths.MacosUserTmpdirContextEnv, contextJSON)
		}
	}
	// THE RUN-AS-YOU CAPTURE'S OWN TMPDIR (MMP-D21), over any composed one: its profile closes the
	// shared /tmp, and a TMPDIR from the user's env would point outside the staging tree, where
	// the installer cannot write either. It rides the env file to the driver and the bootstrap's
	// own env (below), so the installer, the build line and the bootstrap all use it.
	captureTmp := ""
	if self {
		captureTmp = CaptureStagingTmp(stagingRoot)
		captureEnv = withEnvVar(captureEnv, "TMPDIR", captureTmp)
	}
	// THE STAGING HOME, NOT SandboxHome(). This is the one argument that makes the whole
	// slice work: the bootstrap generates ~/.yolo/bin/launch/<bin> into the home the capture
	// will run in, so the installer the driver runs is the launcher a launch would have run.
	// Pointed at the shared home instead, the capture would provision the machine's real
	// agent home and then capture nothing.
	//
	// NO HOME OVERLAY (the "" argument). A launch stages the composed CONTENT tree — skills
	// and briefings — and the bootstrap copies it over the home. A capture wants none of it:
	// the overlay carries prose for an agent to read, this home exists to run one installer
	// and be deleted, and naming a tree StageCommands below does not stage would point the
	// bootstrap's copy at a directory that is not there. Absence is the honest input, the
	// same way "" means no packs above.
	//
	// AND NO CONTEXT TREE (the "" and the zero HostContext), on the same reasoning one step
	// further: a capture runs a vendor INSTALLER, not an agent, so it has no use for the
	// human's ~/.claude/settings.json and no business carrying it into a home whose whole
	// contract is that everything written under it is the installer's output. The zero value
	// makes the host-layer report `unsupported`, which is the true statement about a capture
	// — it delivered no host layers — and keeps the bytes out of the delta walk.
	//
	// AND NO CAPTURE STORE (the third ""), which is the recursion guard rather than an omission:
	// the installer this runs IS the generated launcher, which materializes first when a store is
	// baked into it, so a capture of a program the store already holds would file the store's own
	// bytes as a fresh install and never pick up a newer vendor release (install-capture.md slice
	// 4(f); internal/cli's runCaptureJail suppresses the container's store mount for the same
	// reason).
	//
	// AND NONE OF THE CONFIG'S OWN mise_tools (docs/design/forked-programs-as-packs.md FP-D19): a
	// capture jail is handed only yolo's defaults, as the container arm's is, so the staging home's
	// mise config names no tool of the user's for the installer to set off.
	captureMiseTools, _ := config.JailMiseTools(opts.Config, true)
	bootstrapEnv := buildBootstrapEnv(stagingRoot, opts.Config, gitIdentity, captureEnv,
		packRoot, "", "", "", HostContext{}, "", stagingHome, darwinPrefix, opts.BlockedTools, captureMiseTools)
	if captureTmp != "" {
		bootstrapEnv.Set("TMPDIR", captureTmp)
	}
	// THE RUN-AS-YOU CAPTURE'S REAL HOME, told to the bootstrap as a launch tells it (BuildRunPlan's
	// self branch, MMP-D7): the bootstrap's identity check and scope rule read it
	// (paths.GuestRealHome), and without it they take the account mode's branch and stat
	// /Users/_yolojail, which the capture profile denies, so every run-as-you capture and fork
	// build was refused before its build line (MMP-D22). It is the ONLY one of that branch's homes a
	// capture passes: the machine root and the shared store are what its profile denies
	// (captureStoreDeny), the mise store is the staging home's (buildBootstrapEnv), and a capture
	// names no sidecar, so the bootstrap lays no workspace-home layout that would read them.
	if self && runAs.Homes.Real != "" {
		bootstrapEnv.Set(paths.DarwinRealHomeEnv, runAs.Homes.Real)
	}
	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(stagingRoot)

	// The session env file, keyed on this capture's own cname — never a launch's, which is
	// what keeps a capture from reading (or sweeping) the environment of a session running
	// beside it. Its REMOVAL rides CaptureCleanupCommands rather than a list of its own,
	// because a capture already sweeps everything it created in one deferred call.
	//
	// THE TLS VARIABLES, as a launch sets them (cabundle.go): defaults under the composed env, and
	// the capture's own CA files beside its env file, keyed on the same cname.
	sandboxEnv, caBundleFile, caExtrasFile, caFollows := applyCATrust(captureEnv, opts.CATrust, cname)
	if packRoot != "" {
		sandboxEnv = withEnvVar(sandboxEnv, "YOLO_PACK_ROOT", packRoot)
	}
	caBundleContent, caExtrasContent := caTrustContents(opts.CATrust, caBundleFile, caExtrasFile)
	envFile := ""
	envFileContent := SandboxEnvFileContent(sandboxEnv)
	if envFileContent != "" {
		envFile = SandboxEnvFile(cname, "")
		// Named to the bootstrap by path, as a launch names it (BuildRunPlanWithDaemons): the
		// capture's bootstrap is the launch's bootstrap, and its generator Env should see the
		// environment the driver will run in.
		bootstrapEnv.Set(SandboxEnvFileEnv, envFile)
	}

	// THE RUN-AS-YOU MODE (slice 9 of docs/design/macos-user-model-plan.md): the deny-by-default
	// capture profile, which keeps the machine root and the shared store out of reach where the
	// account's keeps out the account's home; a staging tree that is yours alone; no access grant
	// on the env file, since the sandbox is you; and the bootstrap confined under the capture's
	// profile, as a run-as-you launch confines its own (MMP-D9). The account mode's are today's.
	seatbelt := SeatbeltCaptureProfile(stagingRoot)
	prepare := CaptureStagingCommands(stagingRoot, opts.HostUser)
	envDirCommands := SandboxEnvDirCommands(envFile, "")
	envGrantCommands := SandboxEnvGrantCommands(envFile, "")
	bootstrapArgv := DarwinBootstrapArgv(runAs, stagedYolo, stagingHome, bootstrapEnv, "")
	if self {
		seatbelt = SeatbeltSelfCaptureProfile(stagingRoot, runAs.Homes, opts.HostUser, opts.DarwinUserTempDir,
			opts.SharedTmp)
		prepare = SelfCaptureStagingCommands(stagingRoot)
		envDirCommands, _ = withoutAccessGrants(envDirCommands, nil)
		envGrantCommands = nil
		bootstrapArgv = darwinBootstrapArgvUnder(runAs, stagedYolo, stagingHome, bootstrapEnv, profilePath)
	}

	return CapturePlan{
		RunAs:           runAs,
		Bin:             opts.Bin,
		Cname:           cname,
		StagingRoot:     stagingRoot,
		StagingHome:     stagingHome,
		OutDir:          outDir,
		ProfilePath:     profilePath,
		Seatbelt:        seatbelt,
		StagedDir:       stateDir,
		StagedYolo:      stagedYolo,
		PackRoot:        packRoot,
		PrepareCommands: prepare,
		StageCommands: append(StageBinaryCommands(opts.SelfExe, ""),
			StagePackCommands(opts.HostPackRoot, cname, "")...),
		CleanupCommands: append(append(append(CaptureCleanupCommands(stagingRoot, profilePath),
			SandboxEnvRemoveCommands(envFile)...), SandboxEnvRemoveCommands(caBundleFile)...),
			SandboxEnvRemoveCommands(caExtrasFile)...),
		ScratchGuestCommands: CaptureScratchGuestCommands(runAs, stagingRoot),
		BootstrapArgv:        bootstrapArgv,
		DriverArgv: CaptureDriverArgv(runAs, stagedYolo, stagingHome, outDir, opts.Bin, profilePath,
			envFile, darwinPrefix),
		EnvFile:              envFile,
		EnvFileContent:       envFileContent,
		EnvFileCommands:      envDirCommands,
		EnvFileGrantCommands: envGrantCommands,
		CATrust:              opts.CATrust,
		CABundleFile:         caBundleFile,
		CABundleContent:      caBundleContent,
		CAExtrasFile:         caExtrasFile,
		CAExtrasContent:      caExtrasContent,
		CAFollows:            caFollows,
		DarwinPathPrefix:     darwinPrefix,
		OffendingHome:        offendingHome,
		OffendingHomeSet:     offendingSet,
	}
}

// CaptureStagingCommands provision the throwaway staging tree: a clean root owned by the
// invoking user with the sandbox group's inheriting ACLs, holding an empty home and an empty out
// dir. The invoking user runs them, never root: the capture root is theirs (`yolo yolo-account create`
// gives it to them, and captureGatesPass refuses one that is not), it is made 0755 explicitly
// rather than left to their umask, and the leaves are put in the sandbox group with
// /usr/bin/chgrp, which a non-root owner may do because setup made them a member of it.
//
// It STARTS BY DELETING. A staging tree left by a killed capture would merge into this one, and
// the driver's baseline walk would then file the dead run's files as this installer's — the same
// reason capture.Store.Stage clears its scratch dir before creating it.
//
// The ACLs are WorkspaceACLAces, reused verbatim from the workspace path, because the requirement
// is identical: one directory that both the invoking user and `_yolojail` can write, with the
// grants inheriting to everything created inside. The host half needs them to move the finished
// proto-entry (and to chmod it read-only at admit) over files the sandbox uid created.
func CaptureStagingCommands(stagingRoot, hostUser string) [][]string {
	cmds := [][]string{{rmBin, "-rf", stagingRoot}}
	cmds = append(cmds, categoryDirCommands(filepath.Dir(stagingRoot))...)
	cmds = append(cmds, SharedRootProvisionCommands(stagingRoot, hostUser)...)
	for _, leaf := range []string{CaptureStagingHome(stagingRoot), CaptureStagingOut(stagingRoot)} {
		cmds = append(cmds, captureLeafCommands(leaf)...)
	}
	return cmds
}

// SelfCaptureStagingCommands are CaptureStagingCommands in the run-as-you mode: the same clear,
// capture root and leaves, but the tree is yours alone, 0700, with no group, no setgid and no
// access list. The installer runs as you, so nothing else needs to write it, and no other
// account may read what it writes. (slice 9 of docs/design/macos-user-model-plan.md)
func SelfCaptureStagingCommands(stagingRoot string) [][]string {
	cmds := [][]string{{rmBin, "-rf", stagingRoot}}
	cmds = append(cmds, categoryDirCommands(filepath.Dir(stagingRoot))...)
	for _, dir := range []string{stagingRoot, CaptureStagingHome(stagingRoot), CaptureStagingOut(stagingRoot),
		CaptureStagingTmp(stagingRoot)} {
		cmds = append(cmds, selfCaptureLeafCommands(dir)...)
	}
	return cmds
}

// selfCaptureLeafCommands make one directory of a run-as-you staging tree: yours, 0700.
func selfCaptureLeafCommands(dir string) [][]string {
	return [][]string{{mkdirBin, "-p", dir}, {chmodBin, "0700", dir}}
}

// chgrpBin puts a staging leaf in the sandbox group. A non-root `chown user:group` does the same
// only because the user is in the group; chgrp says what it does.
const chgrpBin = "/usr/bin/chgrp"

// captureLeafCommands make one leaf of a staging tree: the invoking user's, in the sandbox group,
// setgid, so both can write it.
func captureLeafCommands(leaf string) [][]string {
	return [][]string{
		{mkdirBin, "-p", leaf},
		{chgrpBin, SandboxGroup, leaf},
		{chmodBin, "2770", leaf},
	}
}

// CaptureCleanupCommands remove what a capture leaves on the machine: the whole staging tree
// (a bootstrapped home plus everything the installer wrote that was not delta) and the
// per-capture Seatbelt profile.
//
// As the host user, never under sudo. The tree's contents are the sandbox uid's, but the shared
// group's inherited ACE lets the host remove them; where an installer stripped that ACE from files
// it wrote, RunCaptureCleanup falls back to ScratchGuestCommands (PL-7). Best-effort at the call
// site: a capture that succeeded must not be reported as failed because its litter could not be
// swept, and the next capture of the same program clears the same paths anyway.
func CaptureCleanupCommands(stagingRoot, profilePath string) [][]string {
	return [][]string{
		{rmBin, "-rf", stagingRoot},
		{rmBin, "-f", profilePath},
	}
}

// CaptureScratchGuestCommands are PL-7's guest pass over a staging tree
// (docs/design/macos-user-privilege-lifecycle-plan.md): as the yolo account, through the one
// run-as rule, give the owner write on everything it owns, then remove what it can. It cannot
// remove the tree's root, which is the host's; the host's own removal, run again after this,
// takes the rest. The yolo account owns what the installer wrote, so this needs no root.
func CaptureScratchGuestCommands(runAs RunAs, stagingRoot string) [][]string {
	return [][]string{
		runAs.Argv(false, chmodBin, "-R", "--", "u+w", stagingRoot),
		runAs.Argv(false, rmBin, "-rf", "--", stagingRoot),
	}
}

// isCaptureScratchClear reports whether cmd is the host's removal of the staging tree, the one
// removal PL-7 backs with the guest pass.
func isCaptureScratchClear(cmd []string, stagingRoot string) bool {
	return len(cmd) == 3 && cmd[0] == rmBin && cmd[1] == "-rf" && cmd[2] == stagingRoot
}

// removeCaptureScratch removes a staging tree per PL-7: as the host; on failure, the guest pass
// through the rule, then as the host again. The guest pass's own status is not asked: it cannot
// remove the host-owned root, so it fails whenever it does its job, and the host's second try is
// the answer.
func removeCaptureScratch(deps Deps, plan CapturePlan) bool {
	clear := []string{rmBin, "-rf", plan.StagingRoot}
	if deps.Run(clear) == 0 {
		return true
	}
	for _, cmd := range plan.ScratchGuestCommands {
		_ = deps.Run(cmd)
	}
	return deps.Run(clear) == 0
}

// staleCaptureFileCommands remove, as the host, the files this capture is about to write: its
// profile, env file and CA files. They are keyed by the program (or the build), so an earlier
// capture whose cleanup failed leaves files at exactly these paths, and Deps.WriteHostFile is an
// exclusive create that refuses to replace one.
func (p CapturePlan) staleCaptureFileCommands() [][]string {
	var cmds [][]string
	for _, f := range []string{p.ProfilePath, p.EnvFile, p.CABundleFile, p.CAExtrasFile} {
		if f != "" {
			cmds = append(cmds, []string{rmBin, "-f", f})
		}
	}
	return cmds
}

// CaptureDriverArgv builds the argv that runs the capture driver as the yolo account, inside the
// narrowed Seatbelt profile:
//
//	/usr/bin/sudo -n --user=_yolojail /usr/bin/env -i HOME=<staging> … \
//	  /usr/bin/sandbox-exec -f <profile> -- \
//	  <stagedYolo> internal capture-run --home=<staging> --out=<out> --scan-content-refs -- \
//	  /usr/bin/env YOLO_INSTALL_ONLY=1 <bin>
//
// Four things in it are load-bearing and each is pinned by an invariant:
//
//   - HOME is the STAGING home. The driver's whole contract is "a process with a HOME"
//     (capture.Options.Home), and --home repeats it explicitly so the argv says what it does
//     rather than depending on an environment a reader has to reconstruct.
//   - sandbox-exec carries the CAPTURE profile, so the shared /Users/_yolojail is unwritable
//     and unreadable for the duration. Without it a vendor installer would run as the sandbox
//     user against the machine's real agent home.
//   - The installer is `env YOLO_INSTALL_ONLY=1 <bin>`, which resolves through PATH to the
//     generated native launcher (~/.yolo/bin/launch is last, and a fresh staging home has
//     nothing else by that name) and takes its `_do_install` path. InstallOnlyEnv is what stops
//     the launcher exec'ing the tool afterwards and capturing its first-run state.
//   - --scan-content-refs, because a staging path that is not the final home path makes the
//     absolute-reference scan the difference between a relocatable entry and a useless one.
//
// No `--login` and no `zsh -c`: there is nothing to cd into and no login rc to re-assert PATH
// against macOS path_helper, because path_helper never runs on this argv.
func CaptureDriverArgv(runAs RunAs, stagedYolo, stagingHome, outDir, bin, profilePath, envFile string,
	pathPrefix []string) []string {
	// The crossing is the one run-as rule (RunAsSandboxArgv): `-n`, so it never prompts. In the
	// run-as-you mode it is /usr/bin/env alone and USER is yours; HOME is the staging home in both,
	// which holds every tier, so the installer's mise store is the throwaway home's too.
	out := runAs.Argv(false, "-i")
	out = append(out, sandboxEnvPairs(accountHomes(stagingHome), runAs.user(""),
		SandboxPath(stagingHome, pathPrefix), envFile)...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	out = append(out, ExecWithEnvFile(envFile, []string{
		stagedYolo, "internal", "capture-run",
		"--home=" + stagingHome,
		"--out=" + outDir,
		captureScanFlag,
		"--", "/usr/bin/env", captureInstallOnlyVar + "=1", bin,
	})...)
	return out
}

// captureInstallOnlyVar is entrypoint.InstallOnlyEnv, spelled here rather than imported.
//
// Neither package imports the other, and that is deliberate: entrypoint takes
// macosuser-produced strings as parameters rather than importing it (see
// DarwinBootstrapOptions.YoloLogScript), so importing entrypoint here to reach one constant
// would create the edge that arrangement exists to avoid. A duplicated constant is a drift risk,
// so capture_test.go's TestInstallOnlyEnvMatchesTheMacosUserCaptureArgv — a TEST-only import,
// which costs the production graph nothing — asserts the two spellings agree.
const captureInstallOnlyVar = "YOLO_INSTALL_ONLY"

// CapturePlanInvariants returns static-check violation messages over a CapturePlan.
//
// These are the assertions that make slice 6 a slice rather than a string generator: each one
// fails if a CALL SITE is deleted or swapped, not merely if a helper misbehaves. The two that
// matter most are the profile check (swap SeatbeltCaptureProfile for SeatbeltProfile and the
// shared home becomes writable) and the bootstrap-home check (pass SandboxHome() and the capture
// provisions the machine's real agent home instead of a throwaway one).
func CapturePlanInvariants(plan CapturePlan) []string {
	problems := capturePlanProblems(plan)
	if !containsArg(plan.DriverArgv, captureInstallOnlyVar+"=1") {
		problems = append(problems,
			"the capture driver argv omits "+captureInstallOnlyVar+"=1; the launcher would "+
				"exec the tool after installing it and the capture would record its "+
				"first-run state as part of the vendor's package")
	}
	return problems
}

// capturePlanProblems is CapturePlanInvariants less the one check that is an installer's alone (the
// launcher told to install and stop): every other rule holds for a fork's build too
// (ForkBuildPlanInvariants), which runs a build line where an installer capture runs the launcher.
func capturePlanProblems(plan CapturePlan) []string {
	var problems []string

	// Neutral ground, exactly as a workspace must be. The staging tree is shared with the
	// sandbox uid through a group ACL, so a tree inside a user's home would hand that uid a
	// writable foothold in the home this backend exists to isolate it from.
	if plan.OffendingHomeSet {
		problems = append(problems,
			"capture staging tree "+plan.StagingRoot+" is inside the home directory "+
				plan.OffendingHome+"; a capture stages on neutral ground. Move it under "+
				CaptureRootDefault()+".")
	}
	// The two children must be inside the root — the profile allows exactly one subtree, so
	// anything outside it is a write the kernel refuses halfway through a capture.
	for _, pair := range [][2]string{{"staging home", plan.StagingHome}, {"out dir", plan.OutDir}} {
		if !strings.HasPrefix(pair[1], plan.StagingRoot+"/") {
			problems = append(problems,
				"capture "+pair[0]+" "+pair[1]+" is not under the staging tree "+
					plan.StagingRoot+"; the Seatbelt profile makes only that tree writable")
		}
	}
	// The out dir must not be inside a capture surface of the staging home. The driver
	// refuses this at run time; catching it here names it before anything has run.
	for _, s := range paths.HomeSurfaces() {
		surface := filepath.Join(plan.StagingHome, filepath.FromSlash(s.HomeRel))
		if plan.OutDir == surface || strings.HasPrefix(plan.OutDir, surface+"/") {
			problems = append(problems,
				"capture out dir "+plan.OutDir+" is inside the capture surface "+surface+
					"; it would capture itself")
		}
	}

	// THE PROFILE MUST BE THE CAPTURE PROFILE. Two independent checks, because either alone
	// passes for a profile that is wrong in the other way: the shared home must not appear in
	// the write-allow block, and it must be denied AFTER that block (SBPL is last-match-wins,
	// so a deny before the allow is no deny at all).
	// In the run-as-you mode the profile is the deny-by-default capture profile instead, and what
	// it must keep out is the machine root and the shared store (selfCaptureProfileProblems).
	if plan.RunAs.Mode == ModeSelf {
		problems = append(problems, selfCaptureProfileProblems(plan.Seatbelt, plan.RunAs.Homes)...)
		problems = append(problems, selfCapturePlanProblems(plan)...)
	} else {
		problems = append(problems, captureProfileProblems(plan.Seatbelt)...)
	}

	// THE ONE RUN-AS RULE (docs/design/macos-user-privilege-lifecycle-plan.md, task 6): every
	// crossing into the yolo account is the rule's shape, and nothing the host runs (prepare,
	// stage, cleanup) is a sudo at all. A crossing off the shape prompts or fails under the
	// narrowed grant; a host step under sudo is a root act the ruling removed. (The run-as-you
	// mode's crossings are checked by selfCapturePlanProblems: RunAsArgvProblems passes a non-sudo
	// argv.)
	for _, c := range []struct {
		name string
		argv []string
	}{{"capture bootstrap", plan.BootstrapArgv}, {"capture driver", plan.DriverArgv}} {
		problems = append(problems, RunAsArgvProblems(c.name, c.argv)...)
	}
	for _, cmd := range plan.ScratchGuestCommands {
		problems = append(problems, RunAsArgvProblems("capture scratch guest pass", cmd)...)
	}
	for _, group := range []struct {
		name string
		cmds [][]string
	}{{"prepare", plan.PrepareCommands}, {"stage", plan.StageCommands}, {"cleanup", plan.CleanupCommands}} {
		for _, cmd := range group.cmds {
			if runsSudo(cmd) {
				problems = append(problems, "a capture "+group.name+" command runs sudo ("+
					shquote.JoinDisplay(cmd)+"); the host user runs it, in a tree it owns")
			}
		}
	}

	// The staged yolo must live under the host-owned staging root, and BOTH self-execs must run
	// THAT path — the same B2 rule a launch has, for the same reason: a binary the sandbox
	// could rewrite is a sandbox that chooses its own launch code.
	if !strings.HasPrefix(plan.StagedYolo, plan.StagedDir+"/") {
		problems = append(problems,
			"staged yolo "+plan.StagedYolo+" is not under the host-owned staging root "+
				plan.StagedDir+"; the sandbox could rewrite its own capture binary")
	}
	// The driver argv is under the same two-sided env-file contract a launch's argvs are
	// (envfile.go): no composed value as a command-line word, and the file actually read.
	// It matters here for a reason a launch does not have — a capture runs a VENDOR
	// INSTALLER, so a credential on this argv is visible to the very program yolo is
	// running for the first time.
	problems = append(problems, SandboxArgvEnvProblems("capture driver", plan.DriverArgv)...)
	if plan.EnvFile != "" && !containsArg(plan.BootstrapArgv, SandboxEnvFileEnv+"="+plan.EnvFile) {
		problems = append(problems,
			SandboxEnvFileEnv+"="+plan.EnvFile+" is not baked into the capture bootstrap env; "+
				"the staging home would be generated against a different environment than the "+
				"one the driver runs the installer in")
	}
	if !SandboxArgvReadsEnvFile(plan.EnvFile, plan.DriverArgv) {
		problems = append(problems,
			"the capture driver argv never reads the session env file ("+plan.EnvFile+
				"); the installer would run under a different environment than the launch "+
				"whose bytes the capture claims to record")
	}
	if plan.EnvFile != "" && !strings.HasPrefix(plan.EnvFile, plan.StagedDir+"/") {
		problems = append(problems,
			"session env file "+plan.EnvFile+" is not under the host-owned staging root "+
				plan.StagedDir+"; the sandbox could rewrite the environment it is launched with")
	}

	for _, pair := range [][2]string{{"bootstrap", strings.Join(plan.BootstrapArgv, " ")},
		{"capture driver", strings.Join(plan.DriverArgv, " ")}} {
		if !strings.Contains(pair[1], plan.StagedYolo) {
			problems = append(problems,
				"the "+pair[0]+" argv does not self-exec the staged yolo ("+plan.StagedYolo+
					"); it would run an unstaged or unreadable binary")
		}
	}
	if stageCopySourceEmpty(plan.StageCommands) {
		problems = append(problems,
			"no source yolo binary resolved to stage (os.Executable failed); "+
				"the capture would have no binary to exec")
	}
	if !stageCommandsUseFreshInode(plan.StageCommands) {
		problems = append(problems,
			"stage commands overwrite the staged binary in place; macOS signature "+
				"caching requires a fresh inode (copy-to-temp then mv)")
	}

	// BOTH self-execs must carry the STAGING home. The bootstrap generates into it and the
	// driver captures it; either one pointed at SandboxHome() would touch the machine's one
	// shared agent home, which is the thing this whole slice exists not to do.
	for _, self := range []struct {
		name string
		argv []string
	}{{"bootstrap", plan.BootstrapArgv}, {"capture driver", plan.DriverArgv}} {
		name, argv := self.name, self.argv
		if !containsArg(argv, "HOME="+plan.StagingHome) {
			problems = append(problems,
				"the "+name+" argv does not set HOME="+plan.StagingHome+
					"; it would run against the shared sandbox home "+SandboxHome())
		}
		if containsArg(argv, "HOME="+SandboxHome()) {
			problems = append(problems,
				"the "+name+" argv sets HOME="+SandboxHome()+
					" — a capture must never run against the shared sandbox home")
		}
	}

	// The driver must run INSIDE the profile, write where the host expects to find the
	// proto-entry, and ask for the scan that makes the entry relocatable. Each is a silent
	// failure otherwise: an unsandboxed installer, an admit that finds nothing, or an entry
	// admitted relocatable:false that can never be materialized into /Users/_yolojail.
	if !containsArgPair(plan.DriverArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		problems = append(problems,
			"the capture driver argv does not run under `sandbox-exec -f "+plan.ProfilePath+
				"`; the vendor installer would run unconfined as "+SandboxUser)
	}
	if !containsArg(plan.DriverArgv, "--out="+plan.OutDir) {
		problems = append(problems,
			"the capture driver argv does not write --out="+plan.OutDir+
				"; the host act would find no proto-entry to admit")
	}
	if !containsArg(plan.DriverArgv, captureScanFlag) {
		problems = append(problems,
			"the capture driver argv omits "+captureScanFlag+"; the staging path is not the "+
				"materialize path on this backend, so the entry would be admitted "+
				"relocatable:false and could never be materialized into "+SandboxHome())
	}

	// The pack tree must be staged AND named to the bootstrap, or no launcher for Bin exists
	// in the staging home and the capture runs whatever else answers to that name.
	if plan.PackRoot != "" {
		if !strings.HasPrefix(plan.PackRoot, plan.StagedDir+"/") {
			problems = append(problems,
				"staged pack root "+plan.PackRoot+" is not under the host-owned staging root "+
					plan.StagedDir+"; the sandbox could rewrite a pack manifest")
		}
		if !stagesPackTreeAt(plan.StageCommands, plan.PackRoot) {
			problems = append(problems,
				"nothing stages the pack tree at "+plan.PackRoot+
					"; the bootstrap would render zero pack surfaces and no launcher for "+
					plan.Bin+" would exist")
		}
		if !containsArg(plan.BootstrapArgv, "YOLO_PACK_ROOT="+plan.PackRoot) {
			problems = append(problems,
				"YOLO_PACK_ROOT="+plan.PackRoot+" is not baked into the bootstrap env; "+
					"LoadJailPacks would find no packs and no launcher for "+plan.Bin+
					" would exist")
		}
	}
	return problems
}

// captureProfileProblems checks the generated profile is a CAPTURE profile: the shared sandbox
// home is absent from the write-allow block and denied after it.
//
// Textual, because the profile IS text — SBPL is what the kernel reads, so a check against a
// parsed model would be checking something else. Both halves are needed: a profile whose allow
// block lists the shared home is wrong even with a deny after it (the deny would win, but the
// intent has drifted), and a deny that precedes the allow is not a deny at all under
// last-match-wins.
func captureProfileProblems(profile string) []string {
	var problems []string
	home := sbplStr(SandboxHome())
	allowAt := strings.Index(profile, "(allow file-write*")
	denyAt := strings.Index(profile, "(deny file-write* (subpath "+home+"))")
	if allowAt < 0 {
		return []string{"the capture Seatbelt profile has no `(allow file-write*` block; " +
			"the capture could not write its own staging tree"}
	}
	// The allow block runs to its closing line; anything naming the shared home inside it is
	// a write grant on the machine's one credential store.
	end := strings.Index(profile[allowAt:], "\n\n")
	if end < 0 {
		end = len(profile) - allowAt
	}
	if strings.Contains(profile[allowAt:allowAt+end], home) {
		problems = append(problems,
			"the capture Seatbelt profile ALLOWS writes to the shared sandbox home "+
				SandboxHome()+" — a capture must not touch it (this is what a session "+
				"profile does, and SeatbeltCaptureProfile is the one to use here)")
	}
	if denyAt < 0 {
		problems = append(problems,
			"the capture Seatbelt profile never denies writes to the shared sandbox home "+
				SandboxHome())
	} else if denyAt < allowAt {
		problems = append(problems,
			"the capture Seatbelt profile denies the shared sandbox home BEFORE the "+
				"write-allow block; SBPL is last-match-wins, so that deny does nothing")
	}
	return problems
}

// selfCapturePlanProblems is capturePlanProblems' half for a run-as-you capture (slice 9 of
// docs/design/macos-user-model-plan.md), each rule the mode's promise read back off the plan:
//
//   - the plan was given the invoking user's absolute home, which places what the profile denies;
//   - every crossing — bootstrap, driver, scratch pass — is /usr/bin/env alone: no sudo, and no
//     argv names the second account;
//   - the bootstrap runs under the capture's profile (MMP-D9: unconfined as you, it could write
//     anywhere you can);
//   - nothing the host runs grants an access entry or hands a file to the sandbox group: there is
//     no yolo account to open anything to.
func selfCapturePlanProblems(plan CapturePlan) []string {
	var problems []string
	if real := plan.RunAs.Homes.Real; !filepath.IsAbs(real) {
		problems = append(problems, "the run-as-you capture was given no absolute home for you ("+
			quoteOrNone(real)+"); the machine root and shared store it must keep out would be unknown")
	}
	crossings := [][]string{plan.BootstrapArgv, plan.DriverArgv}
	crossings = append(crossings, plan.ScratchGuestCommands...)
	for _, argv := range crossings {
		if len(argv) == 0 {
			continue
		}
		if argv[0] != envBin || slices.Contains(argv, sudoBin) {
			problems = append(problems, "a run-as-you capture argv does not cross through "+envBin+
				" alone: "+shquote.JoinDisplay(argv))
		}
		if w, ok := argvWordNamingAccount(argv); ok {
			problems = append(problems, "a run-as-you capture argv names the yolo account "+SandboxUser+
				": "+shquote.JoinDisplay([]string{w}))
		}
	}
	// THE BOOTSTRAP IS TOLD YOUR REAL HOME (MMP-D22), and its profile lets it stat that home.
	// Without the variable the bootstrap judges itself an account-mode one and stats
	// /Users/_yolojail; without the stat, paths.GuestRealHome answers "" and it does the same.
	if real := plan.RunAs.Homes.Real; filepath.IsAbs(real) && len(plan.BootstrapArgv) > 0 {
		if !containsArg(plan.BootstrapArgv, paths.DarwinRealHomeEnv+"="+real) {
			problems = append(problems, "the run-as-you capture bootstrap is not told your real home ("+
				paths.DarwinRealHomeEnv+"="+real+"); it would take the account mode's identity check, "+
				"which reads "+SandboxHome()+" and refuses")
		}
		if allow := HostHomeStatAllow(real); !strings.Contains(plan.Seatbelt, allow) {
			problems = append(problems, "the run-as-you capture profile does not let its bootstrap stat your "+
				"real home "+real+" (#seatbelt-test-id:"+HostHomeStatID+"#); the identity check would fail "+
				"as if no real home were named")
		}
	}
	if len(plan.BootstrapArgv) > 0 &&
		!containsArgPair(plan.BootstrapArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		problems = append(problems, "the run-as-you capture bootstrap does not run under `sandbox-exec -f "+
			plan.ProfilePath+"`; unconfined as you, it could write anywhere you can (MMP-D9)")
	}
	host := append(append(append([][]string(nil), plan.PrepareCommands...), plan.StageCommands...),
		plan.EnvFileCommands...)
	host = append(host, plan.EnvFileGrantCommands...)
	for _, f := range plan.caTrustFilePlans() {
		host = append(append(host, f.dir...), f.grant...)
	}
	for _, c := range host {
		grant := len(c) >= 2 && c[0] == chmodBin && c[1] == "+a"
		group := slices.Contains(c, SandboxGroup) || slices.ContainsFunc(c, func(w string) bool {
			return strings.HasSuffix(w, ":"+SandboxGroup)
		})
		if grant || group {
			problems = append(problems, "the run-as-you capture grants access to another account ("+
				shquote.JoinDisplay(c)+"); the sandbox is you, so the staging tree and its files are yours alone")
		}
	}
	return problems
}

// containsArgPair reports whether argv contains the three given args CONSECUTIVELY — the shape
// `sandbox-exec -f <profile>` has, where three separate membership tests would pass for an argv
// that mentioned all three in unrelated places.
func containsArgPair(argv []string, a, b, c string) bool {
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] == a && argv[i+1] == b && argv[i+2] == c {
			return true
		}
	}
	return false
}

// RunCapturePlan executes a capture plan: gates, prepare, stage, profile, bootstrap, drive.
//
// It does NOT admit the result and does NOT clean up. The proto-entry it leaves at plan.OutDir
// is the deliverable, and the host act owns what happens to it — see internal/cli/capturemacos.go
// for the move into the store and CaptureCleanupCommands for the sweep, which must run after
// that move and not before it.
//
// The gates are RunMacosUser's, in its order and for its reasons: fail closed before any
// subprocess when we cannot run here, refuse under sudo because running as root misassigns the
// identity the staging tree belongs to, and refuse where the one-time setup's run-as rule or
// staging root is missing, as a launch does.
func RunCapturePlan(deps Deps, plan CapturePlan) int {
	return runCaptureSteps(deps, plan, CapturePlanInvariants(plan), nil)
}

// runCaptureSteps is RunCapturePlan with the plan's violations computed by its caller — an
// installer's (CapturePlanInvariants) or a fork's build's (ForkBuildPlanInvariants) — and the
// commands asUser run between the staging and the profile: a fork's build copies its checkout into
// the staging tree there (ForkBuildPlan.SourceCommands).
//
// EVERY STEP BUT THE CROSSINGS RUNS AS THE INVOKING USER, never under sudo
// (docs/design/macos-user-privilege-lifecycle-plan.md, task 6): the staging tree and the capture
// root are theirs, and so is the staging root the binary, packs, profile and env files go in. Only
// the bootstrap and the driver cross into the yolo account, through the one run-as rule.
func runCaptureSteps(deps Deps, plan CapturePlan, problems []string, asUser [][]string) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !captureGatesPass(deps, out, plan.RunAs.Mode, filepath.Dir(plan.StagingRoot)) {
		return 1
	}
	if len(problems) > 0 {
		out.print("[bold red]macos-user capture plan is not viable:[/bold red]")
		for _, p := range problems {
			out.printf("  ✗ %s", p)
		}
		return 1
	}

	packTree := beginPackTree(deps, out, plan.PackRoot, plan.RunAs.Mode)
	defer finishPackTree(deps, out, packTree)
	out.printf("[dim]Preparing the capture sandbox at %s.[/dim]", plan.StagingRoot)
	for _, group := range [][2]any{
		{"prepare the capture staging tree", plan.PrepareCommands},
		{"stage the capture binary and packs", plan.StageCommands},
	} {
		for _, cmd := range group[1].([][]string) {
			// The clear of a tree a killed capture left: the host's removal, backed by PL-7's
			// guest pass, so files an installer stripped the shared ACE from cannot wedge every
			// later capture of the program.
			if isCaptureScratchClear(cmd, plan.StagingRoot) {
				if !removeCaptureScratch(deps, plan) {
					who := "Neither you nor " + SandboxUser + " could"
					if plan.RunAs.Mode == ModeSelf {
						who = "You could not"
					}
					out.printf("[bold red]Could not clear the capture staging tree %s[/bold red], left by an "+
						"earlier capture. %s remove it; remove it as an administrator "+
						"(`sudo rm -rf -- %s`), then run the capture again.", plan.StagingRoot, who,
						shquote.QuoteDisplay(plan.StagingRoot))
					return 1
				}
				continue
			}
			if deps.Run(cmd) != 0 {
				if isPackTreeReservationCommand(cmd, plan.PackRoot) {
					printPackTreeReservationFailure(out, plan.PackRoot)
				} else {
					out.printf("[bold red]Could not %s (%s).[/bold red]",
						group[0].(string), shquote.JoinDisplay(cmd))
				}
				return 1
			}
			if isPackTreeReservationCommand(cmd, plan.PackRoot) {
				packTree.owned = true
			}
		}
	}
	for _, cmd := range asUser {
		if deps.Run(cmd) != 0 {
			out.printf("[bold red]Could not copy the source into the staging tree (%s).[/bold red]",
				shquote.JoinDisplay(cmd))
			return 1
		}
	}
	// A stale profile, env or CA file at this capture's paths is an earlier capture's whose cleanup
	// failed; the host write below is an exclusive create, so it goes first.
	for _, cmd := range plan.staleCaptureFileCommands() {
		if deps.Run(cmd) != 0 {
			out.printf("[bold red]Could not remove a file an earlier capture left (%s).[/bold red] "+
				"Remove it yourself, then run the capture again.", shquote.JoinDisplay(cmd))
			return 1
		}
	}
	if !deps.WriteHostFile(plan.ProfilePath, plan.Seatbelt, 0o444) {
		out.printf("[bold red]Could not write the capture Seatbelt profile %s[/bold red]",
			plan.ProfilePath)
		return 1
	}
	// The session env file, on the same terms a launch installs it (envfile.go): the
	// capture's composed env is the launch's profile/provider channel, hydrated credentials
	// included. Its removal is already in CleanupCommands, which RunCaptureAct defers.
	if !installSandboxEnvFile(deps, out, plan) {
		return 1
	}
	// Its CA files, which its env file names, on the launch's terms (cabundle.go); removed by
	// CleanupCommands too.
	if !installCATrustFiles(deps, out, plan.caTrustFilePlans()) {
		return 1
	}
	packTree.consumerDispatched = true
	// THE BOOTSTRAP'S OWN OUTPUT GOES TO OUT, bounded, whatever its exit (recordedrun.go): a build
	// jail's log keeps Out and not this process's inherited descriptors, and the bootstrap's
	// stderr is the only record of why a staging home got no launcher.
	boot := runRecorded(deps, plan.BootstrapArgv, bootstrapOutputKept)
	printRecordedOutput(out, "capture bootstrap", boot)
	if boot.Status != 0 {
		refuseCaptureBootstrap(out, boot)
		return 1
	}
	if rc := deps.Run(plan.DriverArgv); rc != 0 {
		out.printf("[bold red]the capture driver exited %d[/bold red] — nothing was captured.", rc)
		return rc
	}
	return 0
}

// captureGatesPass is RunCapturePlan's gates, in its order: fail closed, before any subprocess, where
// a capture cannot run here, saying why on out. A fork's build asks them before its toolchain's nix
// build too (RunForkBuildAct), so a machine that would refuse the build never pays for that first.
//
// After the account, the setup's privilege facts, in the launch's words (PrivilegeUnmet): the
// run-as rule every crossing goes through and the staging root the binary and files are written
// to. Then captureRoot, the directory the staging tree is made in, which the host creates when it
// is absent and must otherwise own.
//
// THE RUN-AS-YOU MODE (slice 9 of docs/design/macos-user-model-plan.md) asks no account and no
// run-as rule: its privilege facts reduce to the staging root and the capture root
// (PrivilegeUnmetFor), and an absent staging root is made here, as you, as a run-as-you launch
// makes it (unmetSelfModePrecondition).
func captureGatesPass(deps Deps, out printer, mode Mode, captureRoot string) bool {
	if !deps.IsMacOS() {
		out.print("[bold red]`yolo capture` on the macos-user backend requires macOS.[/bold red] " +
			"Capture on a container backend instead.")
		return false
	}
	if deps.Geteuid() == 0 {
		how := "It runs as you and crosses into the yolo account through the one-time setup's rule; " +
			"running as root would own the staging tree as root and the yolo account could not write it."
		if mode == ModeSelf {
			how = "It runs the installer as you (macos_user_mode \"self\"); running as root would run it " +
				"as root and own the staging tree as root."
		}
		out.print("[bold red]Don't run `yolo capture` under sudo for the macos-user " +
			"backend.[/bold red]  " + how)
		return false
	}
	if !deps.Which("sandbox-exec") {
		out.print("[bold red]sandbox-exec not found[/bold red] — a capture is only contained " +
			"because Apple Seatbelt confines it, so there is no unsandboxed fallback.")
		return false
	}
	if mode == ModeSelf {
		p := deps.launchProbes()
		if p.stagingRootVerdict() == StagingRootAbsent {
			if ensureHostOwnedRoot(deps, out, StagingRoot(), "", false) != 0 {
				out.print("[bold red]Could not create the macos-user staging directory " + StagingRoot() +
					".[/bold red] The run-as-you mode makes it itself, as you, with no admin step; the step " +
					"that failed is above. Create it yourself and capture again:\n" +
					"  [bold]mkdir -m 0755 " + StagingRoot() + "[/bold]")
				return false
			}
			p = deps.launchProbes()
		}
		if c, unmet := PrivilegeUnmetFor(ModeSelf, p); unmet {
			out.print(c.Refusal(""))
			return false
		}
		return captureRootUsable(deps, out, captureRoot)
	}
	if !deps.SandboxUserExists() {
		out.printf("[bold red]The yolo account '%s' does not exist.[/bold red]\n"+
			"Run the one-time setup first (`yolo yolo-account create`).", SandboxUser)
		return false
	}
	if c, unmet := PrivilegeUnmet(deps.launchProbes()); unmet {
		out.print(c.Refusal(""))
		return false
	}
	return captureRootUsable(deps, out, captureRoot)
}

// captureRootUsable judges the capture root as the staging root is judged (JudgeStagingRoot): the
// host makes the staging tree in it, so it must be the host's, writable by no one else, with no
// access list. Absent is fine: the prepare commands create it, 0755. A root another account owns
// is what captures before the one-rule contract left (root's), which `yolo yolo-account create` hands over.
func captureRootUsable(deps Deps, out printer, captureRoot string) bool {
	f := StagingRootFacts{Err: errNoStagingRootProbe}
	if deps.StagingRootFacts != nil {
		f = deps.StagingRootFacts(captureRoot)
	}
	switch JudgeStagingRoot(f, deps.Geteuid()) {
	case StagingRootOK, StagingRootAbsent:
		return true
	case StagingRootUnknown:
		out.printf("[bold red]Refusing the capture: %s could not be inspected[/bold red] (%s). "+
			"Look at it with `ls -lde %s`; if it is not yours, run [bold]yolo yolo-account create[/bold] as your "+
			"normal admin user; it gives the directory to you.", captureRoot, errStr(f.Err),
			shquote.QuoteDisplay(captureRoot))
		return false
	}
	out.printf("[bold red]Refusing the capture: %s belongs to another account or is not safe.[/bold red] "+
		"Run [bold]yolo yolo-account create[/bold] as your normal admin user; it gives the directory to you.",
		captureRoot)
	return false
}

// captureRootOf is the capture root an act stages under: override, or CaptureRootDefault().
func captureRootOf(override string) string {
	if override == "" {
		return CaptureRootDefault()
	}
	return override
}

// RunCaptureCleanup runs a plan's cleanup commands as the host user, best-effort, and warns about
// what it could not remove.
//
// The staging tree goes per PL-7 (removeCaptureScratch): the host's removal, then on failure the
// sandbox account's own pass through the run-as rule, then the host's again. Never root.
// Best-effort is the whole contract: a capture that succeeded must not be reported as failed
// because its scratch could not be removed, so a failure is a warning naming what is left, and
// the next capture of the same program clears the same paths before it starts.
func RunCaptureCleanup(deps Deps, plan CapturePlan) {
	var left []string
	for _, cmd := range plan.CleanupCommands {
		if isCaptureScratchClear(cmd, plan.StagingRoot) {
			if !removeCaptureScratch(deps, plan) {
				left = append(left, plan.StagingRoot)
			}
			continue
		}
		if deps.Run(cmd) != 0 && len(cmd) > 0 {
			left = append(left, cmd[len(cmd)-1])
		}
	}
	if len(left) == 0 {
		return
	}
	out := printer{w: deps.Out, color: deps.Color}
	out.printf("[yellow]Warning: could not remove what the capture left behind:[/yellow] %s\n"+
		"The capture itself is unaffected, and the next capture of %s clears its staging tree first. "+
		"To remove them now, run [bold]rm -rf -- %s[/bold]; if that is refused, an administrator "+
		"removes them with `sudo rm -rf`.", strings.Join(left, ", "), plan.Bin, shquote.JoinDisplay(left))
}

// runsSudo reports whether cmd runs sudo, by any spelling: on PATH or by absolute path.
func runsSudo(cmd []string) bool {
	return len(cmd) > 0 && filepath.Base(cmd[0]) == filepath.Base(sudoBin)
}

// PrintCapturePlan renders a CapturePlan for a dry run. Plain text, like PrintPlan, and for the
// same reason: it is the artifact a human reads to decide whether to let it run.
func PrintCapturePlan(w io.Writer, plan CapturePlan, problems []string) {
	p := printer{w: w, color: false}
	p.print("[bold]macos-user install-capture plan[/bold] (dry-run — nothing executed)\n")
	p.printf("program:     %s", plan.Bin)
	p.printf("session:     %s", plan.Cname)
	p.printf("staging:     %s", plan.StagingRoot)
	p.printf("  home:      %s", plan.StagingHome)
	p.printf("  out:       %s", plan.OutDir)
	p.printf("profile:     %s", plan.ProfilePath)
	p.printf("staged yolo: %s", plan.StagedYolo)
	if plan.PackRoot == "" {
		p.print("packs:       [dim]none staged — no launcher would exist to run[/dim]")
	} else {
		p.printf("packs:       %s", plan.PackRoot)
	}
	// Names, never values — PrintPlan's rule, and it bites harder here: a capture runs a
	// vendor installer, so the environment it runs under is exactly what a reader is
	// checking when they read this plan before letting it run.
	if plan.EnvFile == "" {
		p.print("env file:    [dim]none — this capture composed no environment[/dim]")
	} else if plan.RunAs.Mode == ModeSelf {
		p.printf("env file:    %s [dim](0600, yours, read by you only)[/dim]", plan.EnvFile)
		p.printf("  [dim]sets, values not shown:[/dim] %s",
			strings.Join(SandboxEnvFileKeys(plan.EnvFileContent), ", "))
	} else {
		p.printf("env file:    %s [dim](0600, yours, read by %s only)[/dim]",
			plan.EnvFile, SandboxUser)
		p.printf("  [dim]sets, values not shown:[/dim] %s",
			strings.Join(SandboxEnvFileKeys(plan.EnvFileContent), ", "))
	}
	p.print("")
	// WHAT THE CAPTURE DOES AS YOU, with no sudo, in the order it runs (PrintPlan's shape,
	// docs/design/macos-user-privilege-lifecycle-plan.md task 6): the staging tree and the files
	// are the host user's, so nothing here can ask for a password.
	p.print("[bold]── as you (no sudo) ──[/bold]")
	for _, cmd := range append(append([][]string{}, plan.PrepareCommands...), plan.StageCommands...) {
		p.print("  " + shquote.JoinDisplay(cmd))
	}
	p.print("  [dim]clearing what an earlier capture may have left at the paths it writes:[/dim]")
	for _, cmd := range plan.staleCaptureFileCommands() {
		p.print("  " + shquote.JoinDisplay(cmd))
	}
	p.printf("  write %s  [dim](0444, content never on argv)[/dim]", shquote.QuoteDisplay(plan.ProfilePath))
	for _, cmd := range plan.EnvFileCommands {
		p.print("  " + shquote.JoinDisplay(cmd))
	}
	if plan.EnvFile != "" {
		p.printf("  write %s  [dim](0600, content never on argv)[/dim]", shquote.QuoteDisplay(plan.EnvFile))
	}
	for _, cmd := range plan.EnvFileGrantCommands {
		p.print("  " + shquote.JoinDisplay(cmd))
	}
	for _, f := range plan.caTrustFilePlans() {
		for _, cmd := range f.dir {
			p.print("  " + shquote.JoinDisplay(cmd))
		}
		p.printf("  write %s  [dim](0600, content never on argv)[/dim]", shquote.QuoteDisplay(f.path))
		for _, cmd := range f.grant {
			p.print("  " + shquote.JoinDisplay(cmd))
		}
	}
	p.print("")
	p.print("[bold]── capture Seatbelt profile ──[/bold]")
	p.print(strings.TrimRight(plan.Seatbelt, "\n"))
	p.print("")
	// THE CROSSINGS, each the whole argv: the one-time setup's rule (RunAsSandboxArgv), `-n`
	// included, so none prompts.
	if plan.RunAs.Mode == ModeSelf {
		p.print("[bold]── as you, inside the capture profile (macos_user_mode \"self\": no yolo account, no sudo) ──[/bold]")
	} else {
		p.printf("[bold]── as %s (the one-time setup's sudo rule, no password) ──[/bold]", SandboxUser)
	}
	p.print("  [dim]bootstrap (staging home):[/dim]")
	p.print("  " + shquote.JoinDisplay(plan.BootstrapArgv))
	p.print("  [dim]capture driver:[/dim]")
	p.print("  " + shquote.JoinDisplay(plan.DriverArgv))
	p.print("")
	// The cleanup, after the move into the store, best-effort: a failure warns and never fails the
	// capture. The guest pass runs only when your removal of the staging tree fails (PL-7).
	p.print("[bold]── cleanup, as you (best-effort) ──[/bold]")
	for _, cmd := range plan.CleanupCommands {
		p.print("  " + shquote.JoinDisplay(cmd))
	}
	if plan.RunAs.Mode == ModeSelf {
		p.print("  [dim]if the staging tree resists, made writable and removed, then removed again:[/dim]")
	} else {
		p.printf("  [dim]if the staging tree resists, as %s, then as you again:[/dim]", SandboxUser)
	}
	for _, cmd := range plan.ScratchGuestCommands {
		p.print("  " + shquote.JoinDisplay(cmd))
	}
	p.print("")
	if len(problems) > 0 {
		p.print("[bold red]plan invariant violations:[/bold red]")
		for _, pr := range problems {
			p.printf("  ✗ %s", pr)
		}
	} else {
		p.print("[green]✓ all capture plan invariants hold[/green]")
	}
}

// RunCaptureAct is the WHOLE macos-user side of `yolo capture <bin>`: build the plan, run it,
// move the finished proto-entry to `dest`, and sweep the staging tree.
//
// `dest` is where the host act expects to find an entry-shaped directory — in practice
// <CapturesDir>/staging/<bin>/out, which capture.Store.AdmitEntry then renames into the store.
// It is a parameter and not derived here because internal/macosuser must not know what a capture
// store is; it produces a proto-entry and hands it over.
//
// # THE MOVE REFUSES RATHER THAN COPYING
//
// The staging tree is on neutral ground and the store is in the invoking user's home (see the
// file comment for why they cannot be the same place), so the proto-entry has to cross between
// them. On a stock Mac both are on the one APFS Data volume and `rename(2)` succeeds — READ FROM
// CODE, not measured: nothing in this repo has run a Mac. If it does not, this refuses and says
// so instead of copying, because a silent multi-gigabyte copy is precisely the cost the whole
// subsystem exists to delete, and capture.Store.Admit already takes exactly this stance about
// exactly this rename.
//
// # NO CALL SITE YET — deliberately, and it is a hand-off, not an oversight
//
// `yolo capture`'s macos-user arm still prints slice 3's refusal; wiring this in is one closure
// in internal/cli/capturehost.go, written out in docs/reference/install-capture.md's slice 6
// section. That file is being edited by the slice this one must not collide with. The precedent
// for landing the mechanism ahead of its wiring is EndpointGrantCommands in this same package.
func RunCaptureAct(deps Deps, opts CaptureOptions, dest string, dryRun bool) int {
	out := printer{w: deps.Out, color: deps.Color}
	if opts.SelfExe == "" && deps.SelfExe != nil {
		opts.SelfExe = deps.SelfExe()
	}
	if opts.HostUser == "" && deps.HostUser != nil {
		opts.HostUser = deps.HostUser()
	}
	// The TLS trust a launch would compose, when the capture has a tool profile to compose it from
	// (cabundle.go). A read of the host, so never in a dry run, and after the gates, as a fork's
	// build asks them before its toolchain: a Mac whose setup would refuse the capture refuses it
	// before any work.
	if !dryRun {
		if !captureGatesPass(deps, out, opts.Mode, captureRootOf(opts.CaptureRoot)) {
			return 1
		}
		opts.CATrust = ComposeCATrust(deps, opts.Darwin)
		opts.CATrust.SeparateAccount = opts.Mode != ModeSelf
		// The run-as-you profile grants your DARWIN_USER_TEMP_DIR under the shared-/tmp ruling, as a
		// run-as-you launch's does; a read of the host, so never in a dry run.
		if opts.DarwinUserTempDir == "" {
			opts.DarwinUserTempDir = deps.darwinUserTempDir(opts.Mode)
		}
	}
	plan := BuildCapturePlan(opts)
	if dryRun {
		PrintCapturePlan(deps.Out, plan, CapturePlanInvariants(plan))
		if len(CapturePlanInvariants(plan)) > 0 {
			return 1
		}
		return 0
	}
	printCATrust(noticePrinter(deps), plan.CATrust, plan.EnvFileContent, plan.CABundleFile, plan.CAExtrasFile, plan.CAFollows)
	rc := RunCapturePlan(deps, plan)
	// Sweep whatever the run got as far as, INCLUDING on failure: a half-provisioned staging
	// tree left behind would merge into the next capture's baseline. It runs after the move
	// below on the success path, so the two orderings are one deferred call.
	defer RunCaptureCleanup(deps, plan)
	if rc != 0 {
		return rc
	}
	if err := moveCaptureOut(plan.OutDir, dest); err != nil {
		out.printf("[bold red]Could not move the capture into the store:[/bold red] %s", err.Error())
		return 1
	}
	return 0
}

// moveCaptureOut renames the finished proto-entry from the neutral staging tree to the store's
// staging directory, creating the destination's parent and refusing a cross-device move.
func moveCaptureOut(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}
	var le *os.LinkError
	if errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV) {
		return fmt.Errorf("%s and %s are on different MOUNTS, so moving the capture there "+
			"would copy every byte of it rather than rename it — which is the cost this "+
			"whole subsystem exists to delete. Put the capture root (%s) and the capture "+
			"store on one volume, or set a capture root that is: %w",
			src, dest, CaptureRootDefault(), err)
	}
	return err
}

// caTrustFilePlans are the capture's CA files as installable files (cabundle.go).
func (p CapturePlan) caTrustFilePlans() []sessionFilePlan {
	files := caTrustFiles(p.EnvFile, p.CABundleFile, p.CABundleContent, p.CAExtrasFile, p.CAExtrasContent)
	// The run-as-you mode grants no access: the files are yours, and so is the sandbox.
	if p.RunAs.Mode == ModeSelf {
		for i := range files {
			files[i].dir, _ = withoutAccessGrants(files[i].dir, nil)
			files[i].grant = nil
		}
	}
	return files
}

// envFile and envFileCommands make a CapturePlan a sandboxEnvPlan (envfile.go).
func (p CapturePlan) envFile() (string, string) { return p.EnvFile, p.EnvFileContent }
func (p CapturePlan) envFileCommands() ([][]string, [][]string) {
	return p.EnvFileCommands, p.EnvFileGrantCommands
}

// ---------------------------------------------------------------------------
// A FORK'S BUILD on this backend (docs/design/forked-programs-as-packs.md FP-D24)
// ---------------------------------------------------------------------------
//
// The capture act above, run for a fork's build line instead of an installer, under the SEALED
// capture profile (SeatbeltSealedCaptureProfile), so the host agent floor of a Mac holds a fork's
// program built for darwin: a container build jail on a Mac makes a Linux build, which no program on
// the Mac runs. Five things differ from an installer's capture, each pinned by
// ForkBuildPlanInvariants:
//
//   - THE STAGING TREE IS THE BUILD'S, <root>/fork-<id>, never <root>/<bin>: an installer capture
//     of the same program starts by deleting <root>/<bin> (CaptureStagingCommands), which would
//     remove a build running beside it.
//   - A src/ SIBLING holds the checkout, copied in by the INVOKING user from the host's staging
//     directory (which sits under that user's home, where the profile denies every read), into a
//     directory the user owns with the shared group's inherited ACLs, so the build can write it.
//   - THE DRIVER RUNS THE BUILD LINE, `env YOLO_BYPASS_SHIMS=1 /bin/bash -c 'cd src && <build>'`,
//     under the full reference scan, as the container build's does (internal/cli's forkBuildJailArgv).
//     The bash is macOS's own: a build for this platform runs on its base system, and a recipe must
//     work there (PPX-D46).
//   - THE TOOLCHAIN RECORD names yolo, the darwin floor's store path and the macOS release, where a
//     container build names its image's identity; the script writes it first, beside out/.
//   - THE TOOLCHAIN IS THE DARWIN FLOOR every launch on this backend materializes (mise, node, git
//     and the rest), the config's darwin `packages:` with it, built before the plan. A base's
//     `node_floor` above the floor's Node and the config's `mise_tools` are not installed: whether
//     they should be is forked-programs-as-packs.md OQ-FP11, open. The provisioning stage is not
//     the way in any case: its tier layout links the capture surfaces out of the home the driver
//     walks.

const (
	// forkBuildLeafPrefix starts a fork build's staging tree's name under the capture root, the build's
	// id after it.
	forkBuildLeafPrefix = "fork-"
	// forkSrcLeaf and forkToolchainLeaf are the build's checkout and the record its script writes
	// first: siblings of home/ and out/, so neither is in the delta nor carried with the entry. They
	// are the leaves the container build's workspace names too (internal/cli's forkSourceLeaf and
	// forkToolchainLeaf), so the host act finds the record where it finds a container build's.
	forkSrcLeaf       = "src"
	forkToolchainLeaf = "toolchain"
	// forkBypassShimsVar is the blocked-tool bypass the build line runs under, as the container
	// build's does: a build script that runs `find` must not meet a refusal meant for an agent.
	forkBypassShimsVar = "YOLO_BYPASS_SHIMS"
	// captureEndGroupFlag has the capture driver end the build line's whole process group when it
	// exits (capture.Options.EndProcessGroup, PPX-D49): a child the line left running would otherwise
	// outlive the act and keep its guest pack tree.
	captureEndGroupFlag = "--end-process-group"
	// buildScriptEnv is what the build's script exports before its line: the install prefixes a
	// container build jail is started with (NPM_CONFIG_PREFIX, its cache, GOPATH), which no login
	// shell sets here, and npm's update check and funding line off, as a container jail has them, so
	// a build makes no registry request and prints no box it did not ask for (PPX-D49).
	buildScriptEnv = "export NPM_CONFIG_PREFIX=\"$HOME/.npm-global\" NPM_CONFIG_CACHE=\"$HOME/.cache/npm\" " +
		"GOPATH=\"$HOME/go\" NPM_CONFIG_UPDATE_NOTIFIER=false NPM_CONFIG_FUND=false\n"
)

// ForkBuildStagingRoot is the staging tree of the fork build whose id is id: <captureRoot>/fork-<id>.
// An empty captureRoot means CaptureRootDefault().
func ForkBuildStagingRoot(captureRoot, id string) string {
	return CaptureStagingRoot(captureRoot, forkBuildLeafPrefix+id)
}

// ForkBuildSourceDir is a fork build's checkout in its staging tree: where the build line runs, and
// what a link the build leaves must not point into, since the tree is deleted when the build ends
// (internal/cli's linksIntoTheBuild).
func ForkBuildSourceDir(stagingRoot string) string { return filepath.Join(stagingRoot, forkSrcLeaf) }

// ForkBuildOptions are the inputs the host act resolves for one fork's build: the capture's, and
// the build's own.
type ForkBuildOptions struct {
	CaptureOptions
	// BuildID is the build's id (internal/cli's forkBuild.id: source, revision, recipe and platform),
	// which keys the staging tree.
	BuildID string
	// Build is the fork's build line, one command line (packdecl refuses a newline in it).
	Build string
	// Source is the host directory holding the checkout of the pinned commit, which the plan copies
	// into the staging tree's src/.
	Source string
	// RepoRoot is the yolo-jail flake the darwin floor is built from (Deps.MaterializeDarwin).
	RepoRoot string
	// Toolchain is what the host knows of the build's toolchain, the record's head: "yolo <version>".
	Toolchain string
	// LineStatusDest is where the act moves the driver's record of the build line's exit status
	// (ForkBuildPlan.LineStatusFile), whether or not the build succeeded; "" moves none.
	LineStatusDest string
	// TreeDir is, for a PATCHED EXTENSION's build (docs/reference/pi-extensions.md §7.1), the
	// home-relative directory its fixed final step copies the checkout into
	// (packdecl.TreeReservedDir); "" for a fork's build. With it set, Build may be "" (a tree with no
	// build line is still copied and admitted).
	TreeDir string
}

// ForkBuildPlan is a CapturePlan for a fork's build, with the checkout and the record beside it.
type ForkBuildPlan struct {
	CapturePlan
	// BuildID and Build are the options' own; Source the host checkout the plan copies.
	BuildID, Build, Source string
	// TreeDir is the options' own: for a patched extension's build, the home-relative directory the
	// tree script's fixed final step copies the checkout into; "" for a fork's build.
	TreeDir string
	// SrcDir is the staging tree's checkout and ToolchainFile the record the build's script writes.
	SrcDir, ToolchainFile string
	// LineStatusFile is where the capture driver records the build line's own exit status
	// (capture.InstallerStatusLeaf, beside the toolchain record): how the host act tells a build line
	// that failed, and its status, from the driver failing after one that succeeded.
	LineStatusFile string
	// SourceCommands copy the checkout into SrcDir, run as the INVOKING user and never under sudo:
	// the source is that user's, and a copy made as root would leave files the build cannot write.
	SourceCommands [][]string
}

// BuildForkBuildPlan assembles the plan for one fork's build (pure — no shelling out, no
// filesystem): the capture plan over the build's own staging tree, the sealed profile, the src/
// leaf and its copy, and the driver running the build line.
func BuildForkBuildPlan(opts ForkBuildOptions) ForkBuildPlan {
	inner := opts.CaptureOptions
	inner.SandboxEnv = withDarwinEnv(inner.SandboxEnv, inner.Darwin)
	stagingRoot := ForkBuildStagingRoot(inner.CaptureRoot, opts.BuildID)
	plan := buildCapturePlanAt(inner, stagingRoot)
	plan.Seatbelt = SeatbeltSealedCaptureProfile(stagingRoot)
	plan.PrepareCommands = ForkBuildStagingCommands(stagingRoot, inner.HostUser)
	if plan.RunAs.Mode == ModeSelf {
		// The run-as-you build (slice 9 of docs/design/macos-user-model-plan.md): the sealed
		// deny-by-default capture profile, and a staging tree that is yours alone.
		plan.Seatbelt = SeatbeltSealedSelfCaptureProfile(stagingRoot, plan.RunAs.Homes, inner.HostUser,
			inner.DarwinUserTempDir, inner.SharedTmp)
		plan.PrepareCommands = append(SelfCaptureStagingCommands(stagingRoot),
			selfCaptureLeafCommands(ForkBuildSourceDir(stagingRoot))...)
	}
	// A SEALED BUILD'S BOOT: the staging home gets no agent's config and the build's output no line
	// about one (PPX-D48), as a container build jail's boot (YOLO_SEALED_BUILD).
	plan.BootstrapArgv = append(slices.Clone(plan.BootstrapArgv), DarwinBootstrapSealedFlag)
	src := ForkBuildSourceDir(stagingRoot)
	toolchain := filepath.Join(stagingRoot, forkToolchainLeaf)
	if opts.TreeDir != "" {
		plan.DriverArgv = TreeBuildDriverArgv(plan.RunAs, plan.StagedYolo, plan.StagingHome, plan.OutDir, src, toolchain,
			forkToolchainHead(opts), opts.Build, opts.TreeDir, plan.ProfilePath, plan.EnvFile,
			plan.DarwinPathPrefix)
	} else {
		plan.DriverArgv = ForkBuildDriverArgv(plan.RunAs, plan.StagedYolo, plan.StagingHome, plan.OutDir, src, toolchain,
			forkToolchainHead(opts), opts.Build, plan.ProfilePath, plan.EnvFile, plan.DarwinPathPrefix)
	}
	var copySource [][]string
	if opts.Source != "" {
		copySource = [][]string{{cpBin, "-R", opts.Source + "/.", src}}
	}
	return ForkBuildPlan{CapturePlan: plan, BuildID: opts.BuildID, Build: opts.Build, Source: opts.Source,
		TreeDir: opts.TreeDir, SrcDir: src, ToolchainFile: toolchain, LineStatusFile: lineStatusFile(toolchain),
		SourceCommands: copySource}
}

// withDarwinEnv is env with the darwin floor's build variables (PKG_CONFIG_PATH and the like) set
// over it, as a launch layers them (BuildRunPlanWithDaemons: darwin's win on conflict); env itself
// when the floor sets none.
func withDarwinEnv(env *jsonx.OrderedMap, d *Darwin) *jsonx.OrderedMap {
	if d == nil || d.Env == nil || d.Env.Len() == 0 {
		return env
	}
	merged := jsonx.NewOrderedMap()
	if env != nil {
		for _, k := range env.Keys() {
			v, _ := env.Get(k)
			merged.Set(k, v)
		}
	}
	for _, k := range d.Env.Keys() {
		v, _ := d.Env.Get(k)
		merged.Set(k, v)
	}
	return merged
}

// forkToolchainHead is the toolchain record's head: what the host knows, the darwin floor's store
// path after it when the plan has one. The build's script appends the macOS release.
func forkToolchainHead(opts ForkBuildOptions) string {
	head := opts.Toolchain
	if head == "" {
		head = "yolo"
	}
	if opts.Darwin != nil && opts.Darwin.ProfilePath != "" {
		head += ", darwin floor " + opts.Darwin.ProfilePath
	}
	return head
}

// ForkBuildStagingCommands are CaptureStagingCommands with the src/ leaf beside home/ and out/, made
// the same way: owned by the invoking user, in the sandbox group, setgid, the shared group's ACLs
// inherited from the root — so the user can copy the checkout in and the yolo account can build
// in it.
func ForkBuildStagingCommands(stagingRoot, hostUser string) [][]string {
	cmds := CaptureStagingCommands(stagingRoot, hostUser)
	return append(cmds, captureLeafCommands(ForkBuildSourceDir(stagingRoot))...)
}

// ForkBuildDriverArgv is CaptureDriverArgv for a fork's build: the same account, environment,
// profile and capture driver, around `env YOLO_BYPASS_SHIMS=1 /bin/bash -c <ForkBuildScript>`.
func ForkBuildDriverArgv(runAs RunAs, stagedYolo, stagingHome, outDir, srcDir, toolchainFile, toolchainHead, build,
	profilePath, envFile string, pathPrefix []string) []string {
	return buildDriverArgv(runAs, stagedYolo, stagingHome, outDir, lineStatusFile(toolchainFile),
		ForkBuildScript(srcDir, toolchainFile, toolchainHead, build), profilePath, envFile, pathPrefix)
}

// TreeBuildDriverArgv is ForkBuildDriverArgv for a PATCHED EXTENSION's build
// (docs/reference/pi-extensions.md §7.1): the same crossing, around TreeBuildScript.
func TreeBuildDriverArgv(runAs RunAs, stagedYolo, stagingHome, outDir, srcDir, toolchainFile, toolchainHead, build,
	treeDir, profilePath, envFile string, pathPrefix []string) []string {
	return buildDriverArgv(runAs, stagedYolo, stagingHome, outDir, lineStatusFile(toolchainFile),
		TreeBuildScript(srcDir, toolchainFile, toolchainHead, build, treeDir), profilePath, envFile, pathPrefix)
}

// buildDriverArgv is the crossing both build drivers share: the yolo account, its environment,
// the profile and the capture driver, around `env YOLO_BYPASS_SHIMS=1 /bin/bash -c <script>`. The
// shell is macOS's own /bin/bash: a build runs on the platform's base system, and a recipe must work
// there (pi-extensions.md PPX-D46).
func buildDriverArgv(runAs RunAs, stagedYolo, stagingHome, outDir, lineStatus, script, profilePath, envFile string,
	pathPrefix []string) []string {
	// The crossing is the one run-as rule (RunAsSandboxArgv), or /usr/bin/env alone as you in the
	// run-as-you mode (CaptureDriverArgv).
	out := runAs.Argv(false, "-i")
	out = append(out, sandboxEnvPairs(accountHomes(stagingHome), runAs.user(""),
		SandboxPath(stagingHome, pathPrefix), envFile)...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	out = append(out, ExecWithEnvFile(envFile, []string{
		stagedYolo, "internal", "capture-run",
		"--home=" + stagingHome,
		"--out=" + outDir,
		captureScanFlag,
		captureEndGroupFlag,
		"--line-status=" + lineStatus,
		"--", "/usr/bin/env", forkBypassShimsVar + "=1", "/bin/bash", "-c", script,
	})...)
	return out
}

// lineStatusFile is where a build's driver records its build line's exit status: beside the
// toolchain record, in the staging tree's root.
func lineStatusFile(toolchainFile string) string {
	return filepath.Join(filepath.Dir(toolchainFile), capture.InstallerStatusLeaf)
}

// ForkBuildScript is the build's bash script: the toolchain record first (head, then the macOS
// release), so a record names every build that reached its line; the install prefixes a container
// build jail is started with (NPM_CONFIG_PREFIX, its cache, GOPATH), which no login shell sets here;
// then the build line in the checkout. The line is last, on a line of its own, so a build line that
// ends in a `# comment` comments out nothing of the script's.
func ForkBuildScript(srcDir, toolchainFile, head, build string) string {
	return "{ printf '%s' " + shQuote(head) + "; printf ' macOS %s' \"$(/usr/bin/sw_vers -productVersion 2>/dev/null)\"; } > " +
		shQuote(toolchainFile) + " 2>/dev/null || true\n" + buildScriptEnv +
		"cd " + shQuote(srcDir) + " && " + build
}

// TreeBuildScript is a PATCHED EXTENSION's build script (docs/reference/pi-extensions.md §7.1),
// the container's tree script (internal/cli's treeBuildJailArgv) on this backend: the toolchain
// record first (head, the macOS release, then the Node and npm the sandbox PATH resolves — the darwin
// floor's, since macOS has no /bin/node to put first as the container's script does), the install
// prefixes ForkBuildScript exports, then in the checkout the build line, if any, in a subshell on
// lines of its own, so a `cd` in it cannot move the copy's source and a trailing `# comment` cannot
// eat the step after it. Last, the FIXED final step: the checkout copied, links kept as links
// (BSD cp's -P), modes and times kept (-p), to ~/<treeDir>, a directory under a capture surface.
func TreeBuildScript(srcDir, toolchainFile, head, build, treeDir string) string {
	script := "{ printf '%s' " + shQuote(head) + "; printf ' macOS %s' \"$(/usr/bin/sw_vers -productVersion 2>/dev/null)\"; " +
		"printf ' node %s npm %s' \"$(node --version 2>/dev/null)\" \"$(npm --version 2>/dev/null)\"; } > " +
		shQuote(toolchainFile) + " 2>/dev/null || true\n" + buildScriptEnv +
		"cd " + shQuote(srcDir)
	if strings.TrimSpace(build) != "" {
		script += " && (\n" + build + "\n)"
	}
	return script + treeCopyStep(srcDir, treeDir)
}

// treeCopyStep is TreeBuildScript's fixed final step, chained after what precedes it.
func treeCopyStep(srcDir, treeDir string) string {
	return " && /bin/mkdir -p \"$HOME\"/" + shQuote(filepath.Dir(treeDir)) +
		" && /bin/cp -pPR " + shQuote(srcDir) + " \"$HOME\"/" + shQuote(treeDir)
}

// ForkBuildPlanInvariants returns static-check violation messages over a ForkBuildPlan: every
// capture rule but the installer's own (capturePlanProblems), then the build's. Each fails when a
// call site in BuildForkBuildPlan is deleted or swapped.
func ForkBuildPlanInvariants(plan ForkBuildPlan) []string {
	problems := capturePlanProblems(plan.CapturePlan)
	if plan.BuildID == "" || filepath.Base(plan.StagingRoot) != forkBuildLeafPrefix+plan.BuildID {
		problems = append(problems,
			"the build's staging tree "+plan.StagingRoot+" is not keyed by the build ("+forkBuildLeafPrefix+
				plan.BuildID+"); an installer capture of "+plan.Bin+" clears "+
				CaptureStagingRoot(filepath.Dir(plan.StagingRoot), plan.Bin)+" before it starts, which would "+
				"delete a build staged there")
	}
	for _, pair := range [][2]string{{"checkout", plan.SrcDir}, {"toolchain record", plan.ToolchainFile}} {
		inHome := pair[1] == plan.StagingHome || strings.HasPrefix(pair[1], plan.StagingHome+"/")
		inOut := pair[1] == plan.OutDir || strings.HasPrefix(pair[1], plan.OutDir+"/")
		if !strings.HasPrefix(pair[1], plan.StagingRoot+"/") || inHome || inOut {
			problems = append(problems,
				"the build's "+pair[0]+" "+pair[1]+" is not a sibling of the staging home and out dir under "+
					plan.StagingRoot+"; under the home the capture would record it, under out it would be "+
					"stored with the entry, and outside the tree the profile makes it unwritable")
		}
	}
	problems = append(problems, sealedProfileProblems(plan.Seatbelt)...)
	if n := len(plan.BootstrapArgv); n == 0 || plan.BootstrapArgv[n-1] != DarwinBootstrapSealedFlag {
		problems = append(problems,
			"the build's bootstrap is not told it is a sealed build ("+DarwinBootstrapSealedFlag+"); it would "+
				"render every selected pack's agent config into the staging home and print each one's notices")
	}
	if !containsArg(plan.DriverArgv, captureEndGroupFlag) {
		problems = append(problems,
			"the build driver argv omits "+captureEndGroupFlag+"; a process the build line left running would "+
				"outlive the act, and the guest pack tree it reads would be kept")
	}
	if !containsArg(plan.DriverArgv, forkBypassShimsVar+"=1") {
		problems = append(problems,
			"the build driver argv omits "+forkBypassShimsVar+"=1; a build line that runs a blocked tool "+
				"would meet the refusal meant for an agent")
	}
	if plan.TreeDir == "" {
		script := ForkBuildScript(plan.SrcDir, plan.ToolchainFile, "", plan.Build)
		script = script[strings.Index(script, "\n")+1:]
		if !argvMentions(plan.DriverArgv, script) {
			problems = append(problems,
				"the build driver argv does not run the fork's build line in "+plan.SrcDir+"; the build would "+
					"run somewhere else, or not at all")
		}
	} else {
		problems = append(problems, treePlanProblems(plan)...)
	}
	if plan.Source == "" {
		problems = append(problems, "the build has no checkout to copy into "+plan.SrcDir+
			"; the build line would run in an empty directory")
	} else if !commandsCopyInto(plan.SourceCommands, plan.Source, plan.SrcDir) {
		problems = append(problems,
			"nothing copies the checkout "+plan.Source+" into "+plan.SrcDir+
				"; the build line would run in an empty directory")
	}
	for _, cmd := range plan.SourceCommands {
		if runsSudo(cmd) {
			problems = append(problems,
				"the checkout is copied under sudo ("+shquote.JoinDisplay(cmd)+"); a copy made as root "+
					"leaves files the yolo account cannot write")
		}
	}
	if !commandsInclude(plan.PrepareCommands, []string{mkdirBin, "-p", plan.SrcDir}) {
		problems = append(problems,
			"the prepare commands never make "+plan.SrcDir+"; the checkout's copy would have nowhere to land")
	}
	return problems
}

// treePlanProblems are a patched extension's build's own invariants: its tree directory is a plain
// home-relative path, and the driver runs the tree script — the build line, if any, in the
// checkout, then the fixed copy into ~/<TreeDir>, without which the admit finds no tree. A tree's
// Build may be empty; its copy may not be missing.
func treePlanProblems(plan ForkBuildPlan) []string {
	var problems []string
	clean := filepath.Clean(plan.TreeDir)
	if filepath.IsAbs(plan.TreeDir) || clean != plan.TreeDir || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, "../") {
		problems = append(problems, "the tree directory "+plan.TreeDir+" is not a plain home-relative path; "+
			"the build's copy would land outside the staging home the capture walks")
	}
	if !argvMentions(plan.DriverArgv, treeCopyStep(plan.SrcDir, plan.TreeDir)) {
		problems = append(problems,
			"the build driver argv never copies the checkout to ~/"+plan.TreeDir+
				"; the admit would find no tree")
	}
	script := TreeBuildScript(plan.SrcDir, plan.ToolchainFile, "", plan.Build, plan.TreeDir)
	script = script[strings.Index(script, "\n")+1:]
	if !argvMentions(plan.DriverArgv, script) {
		problems = append(problems,
			"the build driver argv does not run the tree build in "+plan.SrcDir+" and then copy the checkout "+
				"to ~/"+plan.TreeDir+"; the build would run somewhere else, or leave no tree to admit")
	}
	return problems
}

// commandsCopyInto reports whether cmds hold the copy of src's contents into dst.
func commandsCopyInto(cmds [][]string, src, dst string) bool {
	return commandsInclude(cmds, []string{cpBin, "-R", src + "/.", dst})
}

// commandsInclude reports whether cmds hold want, word for word.
func commandsInclude(cmds [][]string, want []string) bool {
	for _, c := range cmds {
		if strings.Join(c, "\x00") == strings.Join(want, "\x00") {
			return true
		}
	}
	return false
}

// RunForkBuildPlan executes a fork build's plan: RunCapturePlan's steps, with the plan's own
// invariants, and the checkout copied in as the invoking user before the profile is installed.
// Like RunCapturePlan it neither moves the result nor cleans up: RunForkBuildAct does both.
func RunForkBuildPlan(deps Deps, plan ForkBuildPlan) int {
	return runCaptureSteps(deps, plan.CapturePlan, ForkBuildPlanInvariants(plan), plan.SourceCommands)
}

// PrintForkBuildPlan renders a ForkBuildPlan for a dry run: the capture plan, then the build's own.
func PrintForkBuildPlan(w io.Writer, plan ForkBuildPlan, problems []string) {
	p := printer{w: w, color: false}
	p.print("[bold]macos-user fork build[/bold] (dry-run — nothing executed)\n")
	p.printf("build:       %s", plan.BuildID)
	p.printf("checkout:    %s → %s", plan.Source, plan.SrcDir)
	p.printf("toolchain:   %s", plan.ToolchainFile)
	if plan.TreeDir != "" {
		p.printf("tree:        %s → ~/%s", plan.SrcDir, plan.TreeDir)
	}
	p.print("")
	PrintCapturePlan(w, plan.CapturePlan, problems)
}

// RunForkBuildAct is the WHOLE macos-user side of a fork's build: the gates, the darwin floor the
// build line runs on, the plan, its run, and the move of the finished proto-entry to dest and of the
// toolchain record to toolchainDest, then the sweep of the staging tree. It is RunCaptureAct's shape;
// dest is where the host act admits from (internal/cli's buildFork: <CapturesDir>/staging/fork-<id>/out)
// and toolchainDest the record beside it, which the build receipt carries.
func RunForkBuildAct(deps Deps, opts ForkBuildOptions, dest, toolchainDest string, dryRun bool) int {
	out := printer{w: deps.Out, color: deps.Color}
	if opts.SelfExe == "" && deps.SelfExe != nil {
		opts.SelfExe = deps.SelfExe()
	}
	if opts.HostUser == "" && deps.HostUser != nil {
		opts.HostUser = deps.HostUser()
	}
	if !dryRun {
		// The gates before the toolchain: its nix build can take half an hour on a machine where no
		// launch has built the floor yet, and a machine with no sandbox account would refuse after it.
		if !captureGatesPass(deps, out, opts.Mode, captureRootOf(opts.CaptureRoot)) {
			return 1
		}
		d, rc := materializeForkToolchain(deps, out, opts)
		if rc != 0 {
			return rc
		}
		opts.Darwin = d
		opts.CATrust = ComposeCATrust(deps, opts.Darwin)
		opts.CATrust.SeparateAccount = opts.Mode != ModeSelf
		// The run-as-you profile grants your DARWIN_USER_TEMP_DIR under the shared-/tmp ruling, as a
		// run-as-you launch's does; a read of the host, so never in a dry run.
		if opts.DarwinUserTempDir == "" {
			opts.DarwinUserTempDir = deps.darwinUserTempDir(opts.Mode)
		}
	}
	plan := BuildForkBuildPlan(opts)
	if dryRun {
		problems := ForkBuildPlanInvariants(plan)
		PrintForkBuildPlan(deps.Out, plan, problems)
		if len(problems) > 0 {
			return 1
		}
		return 0
	}
	printCATrust(noticePrinter(deps), plan.CATrust, plan.EnvFileContent, plan.CABundleFile, plan.CAExtrasFile, plan.CAFollows)
	rc := RunForkBuildPlan(deps, plan)
	defer RunCaptureCleanup(deps, plan.CapturePlan)
	// The record, when the script wrote one: a build that ran without it is still a build, and its
	// receipt then names no toolchain. MOVED WHEN THE BUILD FAILED TOO: the script writes it first,
	// so it is how the host act tells a build line that ran and failed from a sandbox that never got
	// to it (internal/cli's forkBuildNotStarted, forkBuildLineFailed), as a container build jail's
	// workspace keeps it. Swept with the staging tree, a failed build line read as a build jail that
	// refused to start (PPX-D47).
	if _, err := os.Lstat(plan.ToolchainFile); err == nil {
		if err := moveCaptureOut(plan.ToolchainFile, toolchainDest); err != nil {
			out.printf("[bold red]Could not move the build's toolchain record beside the store:[/bold red] %s",
				err.Error())
			if rc == 0 {
				rc = 1
			}
			return rc
		}
	}
	// THE BUILD LINE'S OWN EXIT STATUS, moved as the record is: the act's exit is the driver's, 1 for
	// every failure, so this is how the host says whether the line or the driver after it failed,
	// and the line's real status. Best-effort: without it the host says only that the line started.
	if _, err := os.Lstat(plan.LineStatusFile); err == nil && opts.LineStatusDest != "" {
		if err := moveCaptureOut(plan.LineStatusFile, opts.LineStatusDest); err != nil {
			out.printf("[bold red]Could not move the build line's exit status beside the store:[/bold red] %s",
				err.Error())
		}
	}
	if rc != 0 {
		return rc
	}
	if err := moveCaptureOut(plan.OutDir, dest); err != nil {
		out.printf("[bold red]Could not move the build into the store:[/bold red] %s", err.Error())
		return 1
	}
	return 0
}

// materializeForkToolchain builds the darwin floor and the config's darwin `packages:` for a fork's
// build, as every launch on this backend does (orchestrator.go's materialize step), and refuses as
// that step refuses: a build that failed, one that produced no tool directory, and a declared package
// with no darwin build, which would be missing from the build's PATH.
func materializeForkToolchain(deps Deps, out printer, opts ForkBuildOptions) (*Darwin, int) {
	if deps.MaterializeDarwin == nil {
		out.print("[bold red]This yolo cannot build the sandbox's tools with nix here, so a fork's build " +
			"has no toolchain.[/bold red] That is yolo's to wire: report it at " + entrypoint.IssuesURL + ".")
		return nil, 1
	}
	if opts.RepoRoot == "" {
		out.print("[bold red]No yolo-jail flake was found to build the sandbox's tools from.[/bold red] " +
			"Every macos-user act builds them from it; `yolo check` names the flake a launch would use.")
		return nil, 1
	}
	// A SIGNAL SENT TO YOLO ALONE WHILE THIS NIX RUNS STOPS IT (internal/nixchildren), as RunMacosUser
	// stops the same build: `yolo capture` and the host floor call this act with no launch arm
	// (Deps.Ending nil), so without the stop a SIGTERM, a SIGHUP or a Ctrl-C ended yolo by the
	// signal's default action and left a build of up to half an hour running with no parent. The
	// stop covers this build alone and is removed when it returns: from the privileged steps on, a
	// signal is theirs. Never with an arm wired: two handlers on one signal race to the exit.
	if deps.Ending == nil {
		defer nixchildren.StopOnSignal()()
	}
	build := deps.Progress.Start(deps.Out, "Building the fork build's tools with nix (the floor a "+
		"macos-user launch runs on)")
	d, ok, err := deps.MaterializeDarwin(opts.RepoRoot, config.EffectivePackages(opts.Config, config.PlatformDarwin))
	if ok {
		build.Done("done")
	} else {
		build.Done("failed")
	}
	if !ok {
		out.printf("[bold red]Could not build the fork build's tools natively:[/bold red] %s\n"+
			"[dim]Fix what it names, then run the build again.[/dim]", errStr(err))
		return nil, 1
	}
	if d == nil || len(d.PathPrefix) == 0 {
		out.print("[bold red]The native package build reported success but produced no tool " +
			"directory, so a fork's build has no toolchain.[/bold red] This is a yolo bug: report it at " +
			entrypoint.IssuesURL + ".")
		return nil, 1
	}
	if len(d.Skipped) > 0 {
		out.printf("[bold red]These packages have no %s build:[/bold red] %s\n"+
			"The fork was not built, because a package you declared would have been missing from its "+
			"build. Check the spelling, or mark it Linux-only ({\"name\": \"<pkg>\", \"platforms\": "+
			"[\"linux\"]}), then run the build again.", darwinSystemLabel(d), strings.Join(d.Skipped, ", "))
		return nil, 1
	}
	return d, 0
}
