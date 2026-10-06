package macosuser

// capture.go is the macos-user half of INSTALL CAPTURE (docs/design/program-delivery.md §6.3,
// docs/plans/install-capture.md slice 6): run a vendor installer once, against a throwaway home,
// under the narrowed Seatbelt profile in seatbeltcapture.go, and leave an entry-shaped
// proto-entry the host act admits into the machine store.
//
// # The shape, and the two things that make it different from a launch
//
//	prepare    a throwaway staging tree on NEUTRAL GROUND, shared-group ACLs
//	stage      the yolo binary + this launch's pack trees into the root-owned state dir
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
// selected installer program's entry, the launch stages a root-owned copy of it under the state
// dir (StagedCapturesRoot, StageCaptureCommands), and the bootstrap bakes that store into the
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
	"strings"
	"syscall"

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
// them (`yolo macos-fix-permissions`, a human running `ls`).
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
}

// CapturePlan is the fully-resolved, ordered artifacts + commands for one install capture — the
// analogue of RunPlan, and a real gate rather than a pretty-printer (CapturePlanInvariants).
type CapturePlan struct {
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
	// PackRoot is the root-owned staged pack tree the bootstrap renders from, or "".
	PackRoot string
	// PrepareCommands provision the staging tree (sudo); StageCommands stage the binary and
	// the pack trees; CleanupCommands remove what the capture leaves behind.
	PrepareCommands [][]string
	StageCommands   [][]string
	CleanupCommands [][]string
	// BootstrapArgv generates the STAGING HOME's shims, launchers and pack surfaces.
	BootstrapArgv []string
	// DriverArgv runs `yolo internal capture-run` under sandbox-exec as the sandbox user.
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
	outDir := CaptureStagingOut(stagingRoot)
	cname := cnameFor(stagingRoot)
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
		packRoot = StagedPackRoot(cname, "")
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
	bootstrapEnv := buildBootstrapEnv(stagingRoot, opts.Config, gitIdentity, opts.SandboxEnv,
		packRoot, "", "", "", HostContext{}, "", stagingHome, darwinPrefix, opts.BlockedTools)
	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(stagingRoot)

	// The session env file, keyed on this capture's own cname — never a launch's, which is
	// what keeps a capture from reading (or sweeping) the environment of a session running
	// beside it. Its REMOVAL rides CaptureCleanupCommands rather than a list of its own,
	// because a capture already sweeps everything it created in one deferred call.
	//
	// THE TLS VARIABLES, as a launch sets them (cabundle.go): defaults under the composed env, and
	// the capture's own CA files beside its env file, keyed on the same cname.
	sandboxEnv, caBundleFile, caExtrasFile, caFollows := applyCATrust(opts.SandboxEnv, opts.CATrust, cname)
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

	return CapturePlan{
		Bin:             opts.Bin,
		Cname:           cname,
		StagingRoot:     stagingRoot,
		StagingHome:     stagingHome,
		OutDir:          outDir,
		ProfilePath:     profilePath,
		Seatbelt:        SeatbeltCaptureProfile(stagingRoot),
		StagedDir:       stateDir,
		StagedYolo:      stagedYolo,
		PackRoot:        packRoot,
		PrepareCommands: CaptureStagingCommands(stagingRoot, opts.HostUser),
		StageCommands: append(StageBinaryCommands(opts.SelfExe, ""),
			StagePackCommands(opts.HostPackRoot, cname, "")...),
		CleanupCommands: append(append(append(CaptureCleanupCommands(stagingRoot, profilePath),
			SandboxEnvRemoveCommands(envFile)...), SandboxEnvRemoveCommands(caBundleFile)...),
			SandboxEnvRemoveCommands(caExtrasFile)...),
		BootstrapArgv: DarwinBootstrapArgv(stagedYolo, stagingHome, bootstrapEnv, ""),
		DriverArgv: CaptureDriverArgv(stagedYolo, stagingHome, outDir, opts.Bin, profilePath,
			envFile, darwinPrefix),
		EnvFile:              envFile,
		EnvFileContent:       envFileContent,
		EnvFileCommands:      SandboxEnvDirCommands(envFile, ""),
		EnvFileGrantCommands: SandboxEnvGrantCommands(envFile, ""),
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
// dir.
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
	cmds = append(cmds, SharedRootProvisionCommands(stagingRoot, hostUser)...)
	for _, leaf := range []string{CaptureStagingHome(stagingRoot), CaptureStagingOut(stagingRoot)} {
		cmds = append(cmds, []string{"mkdir", "-p", leaf})
		if hostUser != "" {
			cmds = append(cmds, []string{"chown", hostUser + ":" + SandboxGroup, leaf})
		}
		cmds = append(cmds, []string{"chmod", "2770", leaf})
	}
	return cmds
}

// CaptureCleanupCommands remove what a capture leaves on the machine: the whole staging tree
// (a bootstrapped home plus everything the installer wrote that was not delta) and the
// per-capture Seatbelt profile.
//
// Under sudo because the tree's contents are the sandbox uid's. Best-effort at the call site:
// a capture that succeeded must not be reported as failed because its litter could not be swept,
// and the next capture of the same program clears the same paths anyway.
func CaptureCleanupCommands(stagingRoot, profilePath string) [][]string {
	return [][]string{
		{rmBin, "-rf", stagingRoot},
		{rmBin, "-f", profilePath},
	}
}

// CaptureDriverArgv builds the argv that runs the capture driver as the sandbox user, inside the
// narrowed Seatbelt profile:
//
//	sudo --user=_yolojail /usr/bin/env -i HOME=<staging> … \
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
func CaptureDriverArgv(stagedYolo, stagingHome, outDir, bin, profilePath, envFile string,
	pathPrefix []string) []string {
	out := []string{"sudo", "--user=" + SandboxUser, "/usr/bin/env", "-i"}
	out = append(out, sandboxEnvPairs(stagingHome, SandboxUser,
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
	// refuses this at run time; catching it here names it before a sudo has run.
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
	problems = append(problems, captureProfileProblems(plan.Seatbelt)...)

	// The staged yolo must live under the root-owned state dir, and BOTH self-execs must run
	// THAT path — the same B2 rule a launch has, for the same reason: a binary the sandbox
	// could rewrite is a sandbox that chooses its own launch code.
	if !strings.HasPrefix(plan.StagedYolo, plan.StagedDir+"/") {
		problems = append(problems,
			"staged yolo "+plan.StagedYolo+" is not under the root-owned state dir "+
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
			"session env file "+plan.EnvFile+" is not under the root-owned state dir "+
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
				"staged pack root "+plan.PackRoot+" is not under the root-owned state dir "+
					plan.StagedDir+"; the sandbox could rewrite a pack manifest")
		}
		if !stagesTreeAt(plan.StageCommands, plan.PackRoot) {
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
// subprocess when we cannot run here, and refuse under sudo because the launch self-escalates
// and running as root misassigns the identity the staging tree is chowned to.
func RunCapturePlan(deps Deps, plan CapturePlan) int {
	return runCaptureSteps(deps, plan, CapturePlanInvariants(plan), nil)
}

// runCaptureSteps is RunCapturePlan with the plan's violations computed by its caller — an
// installer's (CapturePlanInvariants) or a fork's build's (ForkBuildPlanInvariants) — and the
// commands asUser run as the INVOKING user, never under sudo, between the staging and the profile:
// a fork's build copies its checkout into the staging tree there (ForkBuildPlan.SourceCommands).
func runCaptureSteps(deps Deps, plan CapturePlan, problems []string, asUser [][]string) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !captureGatesPass(deps, out) {
		return 1
	}
	if len(problems) > 0 {
		out.print("[bold red]macos-user capture plan is not viable:[/bold red]")
		for _, p := range problems {
			out.printf("  ✗ %s", p)
		}
		return 1
	}

	out.printf("[dim]Preparing the capture sandbox at %s — sudo may prompt once.[/dim]",
		plan.StagingRoot)
	for _, group := range [][2]any{
		{"prepare the capture staging tree", plan.PrepareCommands},
		{"stage the capture binary and packs", plan.StageCommands},
	} {
		for _, cmd := range group[1].([][]string) {
			if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
				out.printf("[bold red]Could not %s (%s).[/bold red]",
					group[0].(string), shquote.JoinDisplay(cmd))
				return 1
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
	if !deps.InstallRootFile(plan.ProfilePath, plan.Seatbelt, "0444") {
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
	if deps.Run(plan.BootstrapArgv) != 0 {
		out.print("[bold red]capture bootstrap failed[/bold red] — the staging home has no " +
			"generated launcher, so there is nothing for the capture to run. Aborting.")
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
func captureGatesPass(deps Deps, out printer) bool {
	if !deps.IsMacOS() {
		out.print("[bold red]`yolo capture` on the macos-user backend requires macOS.[/bold red] " +
			"Capture on a container backend instead.")
		return false
	}
	if deps.Geteuid() == 0 {
		out.print("[bold red]Don't run `yolo capture` under sudo for the macos-user " +
			"backend.[/bold red]  It escalates each step itself; running as root would own " +
			"the staging tree as root and the sandbox user could not write it.")
		return false
	}
	if !deps.Which("sandbox-exec") {
		out.print("[bold red]sandbox-exec not found[/bold red] — a capture is only contained " +
			"because Apple Seatbelt confines it, so there is no unsandboxed fallback.")
		return false
	}
	if !deps.SandboxUserExists() {
		out.printf("[bold red]Sandbox user '%s' does not exist.[/bold red]\n"+
			"Run the one-time setup first (`yolo macos-setup`).", SandboxUser)
		return false
	}
	return true
}

// RunCaptureCleanup runs a plan's cleanup commands, best-effort, and reports nothing.
//
// Best-effort is the whole contract: a capture that succeeded must not be reported as failed
// because a `rm -rf` under sudo did not take, and the next capture of the same program clears
// the same paths before it starts.
func RunCaptureCleanup(deps Deps, plan CapturePlan) {
	for _, cmd := range plan.CleanupCommands {
		_ = deps.Run(append([]string{"sudo"}, cmd...))
	}
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
	} else {
		p.printf("env file:    %s [dim](0600, root-owned, read by %s only)[/dim]",
			plan.EnvFile, SandboxUser)
		p.printf("  [dim]sets, values not shown:[/dim] %s",
			strings.Join(SandboxEnvFileKeys(plan.EnvFileContent), ", "))
	}
	p.print("")
	p.print("[bold]── privileged commands (run via sudo) ──[/bold]")
	for _, cmd := range append(append(append([][]string{}, plan.PrepareCommands...),
		plan.StageCommands...), plan.EnvFileCommands...) {
		p.print("  sudo " + shquote.JoinDisplay(cmd))
	}
	if plan.EnvFile != "" {
		p.printf("  sudo %s %s  [dim](content on stdin, never argv)[/dim]", teeBin, shquote.QuoteDisplay(plan.EnvFile))
		p.printf("  sudo %s 0600 %s", chmodBin, shquote.QuoteDisplay(plan.EnvFile))
	}
	for _, cmd := range plan.EnvFileGrantCommands {
		p.print("  sudo " + shquote.JoinDisplay(cmd))
	}
	for _, f := range plan.caTrustFilePlans() {
		for _, cmd := range f.dir {
			p.print("  sudo " + shquote.JoinDisplay(cmd))
		}
		p.printf("  sudo %s %s  [dim](content on stdin, never argv)[/dim]", teeBin, shquote.QuoteDisplay(f.path))
		p.printf("  sudo %s 0600 %s", chmodBin, shquote.QuoteDisplay(f.path))
		for _, cmd := range f.grant {
			p.print("  sudo " + shquote.JoinDisplay(cmd))
		}
	}
	p.print("")
	p.print("[bold]── capture Seatbelt profile ──[/bold]")
	p.print(strings.TrimRight(plan.Seatbelt, "\n"))
	p.print("")
	p.print("[bold]── bootstrap argv (staging home) ──[/bold]")
	p.print("  " + shquote.JoinDisplay(plan.BootstrapArgv))
	p.print("")
	p.print("[bold]── capture driver argv ──[/bold]")
	p.print("  " + shquote.JoinDisplay(plan.DriverArgv))
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
// in internal/cli/capturehost.go, written out in docs/plans/install-capture.md's slice 6
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
	// (cabundle.go). A read of the host, so never in a dry run.
	if !dryRun {
		opts.CATrust = ComposeCATrust(deps, opts.Darwin)
	}
	plan := BuildCapturePlan(opts)
	if dryRun {
		PrintCapturePlan(deps.Out, plan, CapturePlanInvariants(plan))
		if len(CapturePlanInvariants(plan)) > 0 {
			return 1
		}
		return 0
	}
	printCATrust(out, plan.CATrust, plan.EnvFileContent, plan.CABundleFile, plan.CAExtrasFile, plan.CAFollows)
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
	return caTrustFiles(p.EnvFile, p.CABundleFile, p.CABundleContent, p.CAExtrasFile, p.CAExtrasContent)
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
//   - THE DRIVER RUNS THE BUILD LINE, `env YOLO_BYPASS_SHIMS=1 bash -c 'cd src && <build>'`, under
//     the full reference scan, as the container build's does (internal/cli's forkBuildJailArgv).
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
}

// ForkBuildPlan is a CapturePlan for a fork's build, with the checkout and the record beside it.
type ForkBuildPlan struct {
	CapturePlan
	// BuildID and Build are the options' own; Source the host checkout the plan copies.
	BuildID, Build, Source string
	// SrcDir is the staging tree's checkout and ToolchainFile the record the build's script writes.
	SrcDir, ToolchainFile string
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
	src := ForkBuildSourceDir(stagingRoot)
	toolchain := filepath.Join(stagingRoot, forkToolchainLeaf)
	plan.DriverArgv = ForkBuildDriverArgv(plan.StagedYolo, plan.StagingHome, plan.OutDir, src, toolchain,
		forkToolchainHead(opts), opts.Build, plan.ProfilePath, plan.EnvFile, plan.DarwinPathPrefix)
	var copySource [][]string
	if opts.Source != "" {
		copySource = [][]string{{cpBin, "-R", opts.Source + "/.", src}}
	}
	return ForkBuildPlan{CapturePlan: plan, BuildID: opts.BuildID, Build: opts.Build, Source: opts.Source,
		SrcDir: src, ToolchainFile: toolchain, SourceCommands: copySource}
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
// inherited from the root — so the user can copy the checkout in and the sandbox account can build
// in it.
func ForkBuildStagingCommands(stagingRoot, hostUser string) [][]string {
	cmds := CaptureStagingCommands(stagingRoot, hostUser)
	src := ForkBuildSourceDir(stagingRoot)
	cmds = append(cmds, []string{"mkdir", "-p", src})
	if hostUser != "" {
		cmds = append(cmds, []string{"chown", hostUser + ":" + SandboxGroup, src})
	}
	return append(cmds, []string{"chmod", "2770", src})
}

// ForkBuildDriverArgv is CaptureDriverArgv for a fork's build: the same account, environment,
// profile and capture driver, around `env YOLO_BYPASS_SHIMS=1 /bin/bash -c <ForkBuildScript>`.
func ForkBuildDriverArgv(stagedYolo, stagingHome, outDir, srcDir, toolchainFile, toolchainHead, build,
	profilePath, envFile string, pathPrefix []string) []string {
	out := []string{"sudo", "--user=" + SandboxUser, "/usr/bin/env", "-i"}
	out = append(out, sandboxEnvPairs(stagingHome, SandboxUser,
		SandboxPath(stagingHome, pathPrefix), envFile)...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	out = append(out, ExecWithEnvFile(envFile, []string{
		stagedYolo, "internal", "capture-run",
		"--home=" + stagingHome,
		"--out=" + outDir,
		captureScanFlag,
		"--", "/usr/bin/env", forkBypassShimsVar + "=1", "/bin/bash", "-c",
		ForkBuildScript(srcDir, toolchainFile, toolchainHead, build),
	})...)
	return out
}

// ForkBuildScript is the build's bash script: the toolchain record first (head, then the macOS
// release), so a record names every build that reached its line; the install prefixes a container
// build jail is started with (NPM_CONFIG_PREFIX, its cache, GOPATH), which no login shell sets here;
// then the build line in the checkout. The line is last, on a line of its own, so a build line that
// ends in a `# comment` comments out nothing of the script's.
func ForkBuildScript(srcDir, toolchainFile, head, build string) string {
	return "{ printf '%s' " + shQuote(head) + "; printf ' macOS %s' \"$(/usr/bin/sw_vers -productVersion 2>/dev/null)\"; } > " +
		shQuote(toolchainFile) + " 2>/dev/null || true\n" +
		"export NPM_CONFIG_PREFIX=\"$HOME/.npm-global\" NPM_CONFIG_CACHE=\"$HOME/.cache/npm\" GOPATH=\"$HOME/go\"\n" +
		"cd " + shQuote(srcDir) + " && " + build
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
	if !containsArg(plan.DriverArgv, forkBypassShimsVar+"=1") {
		problems = append(problems,
			"the build driver argv omits "+forkBypassShimsVar+"=1; a build line that runs a blocked tool "+
				"would meet the refusal meant for an agent")
	}
	script := ForkBuildScript(plan.SrcDir, plan.ToolchainFile, "", plan.Build)
	script = script[strings.Index(script, "\n")+1:]
	if !argvMentions(plan.DriverArgv, script) {
		problems = append(problems,
			"the build driver argv does not run the fork's build line in "+plan.SrcDir+"; the build would "+
				"run somewhere else, or not at all")
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
		if len(cmd) > 0 && cmd[0] == "sudo" {
			problems = append(problems,
				"the checkout is copied under sudo ("+shquote.JoinDisplay(cmd)+"); a copy made as root "+
					"leaves files the sandbox account cannot write")
		}
	}
	if !commandsInclude(plan.PrepareCommands, []string{"mkdir", "-p", plan.SrcDir}) {
		problems = append(problems,
			"the prepare commands never make "+plan.SrcDir+"; the checkout's copy would have nowhere to land")
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
		if !captureGatesPass(deps, out) {
			return 1
		}
		d, rc := materializeForkToolchain(deps, out, opts)
		if rc != 0 {
			return rc
		}
		opts.Darwin = d
		opts.CATrust = ComposeCATrust(deps, opts.Darwin)
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
	printCATrust(out, plan.CATrust, plan.EnvFileContent, plan.CABundleFile, plan.CAExtrasFile, plan.CAFollows)
	rc := RunForkBuildPlan(deps, plan)
	defer RunCaptureCleanup(deps, plan.CapturePlan)
	if rc != 0 {
		return rc
	}
	// The record, when the script wrote one: a build that ran without it is still a build, and its
	// receipt then names no toolchain.
	if _, err := os.Lstat(plan.ToolchainFile); err == nil {
		if err := moveCaptureOut(plan.ToolchainFile, toolchainDest); err != nil {
			out.printf("[bold red]Could not move the build's toolchain record beside the store:[/bold red] %s",
				err.Error())
			return 1
		}
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
