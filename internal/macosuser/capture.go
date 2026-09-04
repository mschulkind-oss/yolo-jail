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
// NOT MEASURED, anywhere: that any of it works on a Mac. No Seatbelt profile has been loaded by
// a kernel, no `sudo dscl` has run, this backend's installer pipeline is itself unverified on
// hardware (docs/design/macos-user-nix-and-features.md), and podman-in-podman cannot exercise
// this backend at all. The hardware checklist is in install-capture.md's slice 6 section.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	DriverArgv       []string
	DarwinPathPrefix []string
	// OffendingHome is the user home containing StagingRoot, when there is one — the same
	// neutral-ground check a launch makes about its workspace, applied to the staging tree.
	OffendingHome    string
	OffendingHomeSet bool
}

// BuildCapturePlan assembles the whole capture plan (pure — no shelling out, no filesystem).
func BuildCapturePlan(opts CaptureOptions) CapturePlan {
	darwinPrefix := []string{}
	if opts.Darwin != nil {
		darwinPrefix = append(darwinPrefix, opts.Darwin.PathPrefix...)
	}
	stagingRoot := CaptureStagingRoot(opts.CaptureRoot, opts.Bin)
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
	bootstrapEnv := buildBootstrapEnv(stagingRoot, opts.Config, gitIdentity, opts.SandboxEnv,
		packRoot, "", stagingHome, darwinPrefix, opts.BlockedTools)
	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(stagingRoot, "")

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
		CleanupCommands:  CaptureCleanupCommands(stagingRoot, profilePath),
		BootstrapArgv:    DarwinBootstrapArgv(stagedYolo, stagingHome, bootstrapEnv, ""),
		DriverArgv:       CaptureDriverArgv(stagedYolo, stagingHome, outDir, opts.Bin, profilePath, opts.SandboxEnv, darwinPrefix),
		DarwinPathPrefix: darwinPrefix,
		OffendingHome:    offendingHome,
		OffendingHomeSet: offendingSet,
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
func CaptureDriverArgv(stagedYolo, stagingHome, outDir, bin, profilePath string,
	sandboxEnv *jsonx.OrderedMap, pathPrefix []string) []string {
	out := []string{"sudo", "--user=" + SandboxUser, "/usr/bin/env", "-i"}
	out = append(out, sandboxEnvPairs(stagingHome, SandboxUser,
		SandboxPath(stagingHome, pathPrefix), sandboxEnv)...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	out = append(out,
		stagedYolo, "internal", "capture-run",
		"--home="+stagingHome,
		"--out="+outDir,
		captureScanFlag,
		"--", "/usr/bin/env", captureInstallOnlyVar+"=1", bin,
	)
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
	if !containsArg(plan.DriverArgv, captureInstallOnlyVar+"=1") {
		problems = append(problems,
			"the capture driver argv omits "+captureInstallOnlyVar+"=1; the launcher would "+
				"exec the tool after installing it and the capture would record its "+
				"first-run state as part of the vendor's package")
	}

	// The pack tree must be staged AND named to the bootstrap, or no launcher for Bin exists
	// in the staging home and the capture runs whatever else answers to that name.
	if plan.PackRoot != "" {
		if !strings.HasPrefix(plan.PackRoot, plan.StagedDir+"/") {
			problems = append(problems,
				"staged pack root "+plan.PackRoot+" is not under the root-owned state dir "+
					plan.StagedDir+"; the sandbox could rewrite a pack manifest")
		}
		if !stagesPackRoot(plan.StageCommands, plan.PackRoot) {
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
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		out.print("[bold red]`yolo capture` on the macos-user backend requires macOS.[/bold red] " +
			"Capture on a container backend instead.")
		return 1
	}
	if deps.Geteuid() == 0 {
		out.print("[bold red]Don't run `yolo capture` under sudo for the macos-user " +
			"backend.[/bold red]  It escalates each step itself; running as root would own " +
			"the staging tree as root and the sandbox user could not write it.")
		return 1
	}
	if !deps.Which("sandbox-exec") {
		out.print("[bold red]sandbox-exec not found[/bold red] — a capture is only contained " +
			"because Apple Seatbelt confines it, so there is no unsandboxed fallback.")
		return 1
	}
	if !deps.SandboxUserExists() {
		out.printf("[bold red]Sandbox user '%s' does not exist.[/bold red]\n"+
			"Run the one-time setup first (`yolo macos-setup`).", SandboxUser)
		return 1
	}
	if problems := CapturePlanInvariants(plan); len(problems) > 0 {
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
					group[0].(string), strings.Join(cmd, " "))
				return 1
			}
		}
	}
	if !deps.InstallRootFile(plan.ProfilePath, plan.Seatbelt, "0444") {
		out.printf("[bold red]Could not write the capture Seatbelt profile %s[/bold red]",
			plan.ProfilePath)
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
	p.print("")
	p.print("[bold]── privileged commands (run via sudo) ──[/bold]")
	for _, cmd := range append(append([][]string{}, plan.PrepareCommands...), plan.StageCommands...) {
		p.print("  sudo " + strings.Join(cmd, " "))
	}
	p.print("")
	p.print("[bold]── capture Seatbelt profile ──[/bold]")
	p.print(strings.TrimRight(plan.Seatbelt, "\n"))
	p.print("")
	p.print("[bold]── bootstrap argv (staging home) ──[/bold]")
	p.print("  " + strings.Join(plan.BootstrapArgv, " "))
	p.print("")
	p.print("[bold]── capture driver argv ──[/bold]")
	p.print("  " + strings.Join(plan.DriverArgv, " "))
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
	plan := BuildCapturePlan(opts)
	if dryRun {
		PrintCapturePlan(deps.Out, plan, CapturePlanInvariants(plan))
		if len(CapturePlanInvariants(plan)) > 0 {
			return 1
		}
		return 0
	}
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
