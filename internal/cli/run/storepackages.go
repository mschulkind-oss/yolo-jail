package run

// storepackages.go is the HOST half of C4/C5 — the launch's decision about WHERE this
// jail's packages come from (docs/reference/image-staging-vs-baking.md, "Store-delivered
// packages").
//
// THE DEFAULT IS UNCHANGED AND THAT IS THE RULING, NOT A HEDGE. OQ-1 fixed the shape:
// C4/C5 ship as an OPT-IN FAST PATH WITH THE BAKED PATH RETAINED, and "retained" is per
// LAUNCH, never per package. A package both baked and staged silently runs the baked copy
// (R2, and the PATH ordering in internal/entrypoint that makes it true), so the only shape
// that is honest is: a launch that opts in builds the STOCK image with no
// YOLO_EXTRA_PACKAGES and gets its packages from the mounted store; a launch that does not
// builds the baked image and gets them from /bin. Exactly one mechanism is live in any
// jail. imageload.go is where that alternative is enforced, because the image build is the
// only place that can enforce it.
//
// WHAT IT BUYS. The image stops depending on `builtins.getEnv`, so an opt-in machine has
// ONE image no matter how many workspaces declare how many different `packages:` lists —
// §1.5's multiplication deleted at the root, and with it a guaranteed binary-cache miss
// (§6 item 2) and ~3 GB of podman storage per distinct list (§1.9).
//
// WHAT IT COSTS, stated rather than discovered: two package-delivery mechanisms
// maintained on purpose (R1). Apple Container cannot reach a store path at all and macOS
// podman shares no store, so both keep baking. That asymmetry is the accepted price of
// OQ-1's ruling.
//
// THE HOST DECIDES, NOT THE JAIL. From inside, "this host cannot share its store" and
// "the operator did not opt in" are the same observation — the same reason the in-jail
// reachability witness cannot derive YOLO_HOST_LOOPBACK. So eligibility is settled here,
// before the image build, and the jail is told the ANSWER on YOLO_STORE_PROFILES.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// StorePackagesOptInEnv is the opt-in. An ENVIRONMENT VARIABLE rather than a config key,
// and the reason is scope: what it selects is a property of the MACHINE and of this
// launch (podman, Linux, a running nix daemon, and an operator who wants the fast path),
// while `packages:` is workspace-scope by ruling (OQ-4). A workspace config key would let
// a repo assert something about the host it is checked out on, and a user-scope key would
// still be answering a per-launch question with persistent state. It sits beside the
// launcher's other machine-shaped dials — YOLO_NIX_HOST_DAEMON, YOLO_NO_HOST_LOOPBACK,
// YOLO_ALLOW_STALE_IMAGE — and is read the same way.
const StorePackagesOptInEnv = "YOLO_STORE_PACKAGES"

// storePackagesPlan is what one launch decided about store delivery.
//
// Active is the ONE field downstream reads, and it is deliberately not derivable from
// len(Profiles): an opt-in launch whose `packages:` list is empty still has an Active
// plan, because Active is what tells the image build to leave YOLO_EXTRA_PACKAGES unset.
// Under C5 that same flag also selects the leaner image, which has content to move even
// when the workspace declares no packages at all.
type storePackagesPlan struct {
	// Active: this launch delivers packages from the mounted store, and the image it
	// builds does not contain them.
	Active bool
	// Profiles are the workspace's own `packages:` buildEnv store paths, in PRECEDENCE
	// order — the jail's farm is first-wins, so they lead. Their libraries go on
	// LD_LIBRARY_PATH, which is what keeps a `packages:` library dlopen-able by bare soname.
	Profiles []string
	// FHSProfiles are yolo's own image extras (`.#yoloImageExtras`, C5), linked BEHIND
	// Profiles. Their libraries reach FHS binaries only, through nix-ld's compiled-in
	// path, and never LD_LIBRARY_PATH: the chromium stack in them (glib, pixman) needs a
	// newer glibc than an older nix program has, which is the baked image's yolo-fhs rule
	// (docs/reference/mise-node-dynamic-linking.md).
	FHSProfiles []string
}

// env returns the `-e YOLO_STORE_PROFILES=…` and `-e YOLO_STORE_FHS_PROFILES=…` pairs for
// an active plan, each only when its list is non-empty, and nil otherwise. An active plan
// with no profiles at all emits nothing: there is no farm to build, and an empty variable
// would make the jail claim store delivery is live while linking nothing.
func (p storePackagesPlan) env() []string {
	if !p.Active {
		return nil
	}
	var out []string
	if len(p.Profiles) > 0 {
		out = append(out, "-e", entrypoint.StoreProfilesEnv+"="+strings.Join(p.Profiles, ":"))
	}
	if len(p.FHSProfiles) > 0 {
		out = append(out, "-e", entrypoint.StoreFHSProfilesEnv+"="+strings.Join(p.FHSProfiles, ":"))
	}
	return out
}

// envTruthy is the launcher's spelling of "the operator said yes", matching
// shouldMountHostNix's own switch so two opt-in dials cannot disagree about what "true"
// looks like.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// storePackagesEligible reports whether a launch CAN take the store-delivery fast path,
// and names the reason when it cannot. The three conditions are §3.2's table, restated:
// the mechanism is store paths resolved from inside the jail, so it needs a runtime that
// bind-mounts the host store, a host whose store is the jail's architecture, and a store
// to mount.
//
// storeMounted is the SAME predicate the assembler uses to decide whether to emit the
// mount (shouldMountHostNix), passed in rather than recomputed: a launch that promised
// store delivery and then did not mount the store is the one failure this must not be
// able to produce.
func storePackagesEligible(rt string, isMacOS, storeMounted bool) (bool, string) {
	if rt != "podman" {
		return false, "it needs podman, and this launch uses the " + rt + " runtime " +
			"(Apple Container cannot bind-mount the host nix store at all)"
	}
	if isMacOS {
		return false, "it needs a Linux host: a macOS podman runs in a VM that shares no " +
			"/nix/store with the host, and the jail's packages are Linux builds"
	}
	if !storeMounted {
		return false, "the host nix daemon socket and /nix/store are not both present, " +
			"so nothing inside the jail could resolve a store path"
	}
	return true, ""
}

// planStorePackages settles store delivery for this launch. It returns (plan, ok); ok is
// false only when the launch must abort — a package the workspace declared could not be
// built, or nix failed outright.
//
// A REQUESTED-BUT-INELIGIBLE LAUNCH FALLS BACK TO BAKING, LOUDLY, and does not refuse.
// The fallback direction matters: baking yields a jail with all its tools, so refusing
// would cost the user a working launch to enforce a preference about where bytes live.
// What is NOT acceptable is doing it silently — a fast path that quietly is not one is how
// a measurement gets attributed to the wrong mechanism.
//
// A DECLARED PACKAGE THAT DID NOT BUILD IS FATAL, matching the macos-user arm's A2 ruling
// exactly (internal/macosuser/orchestrator.go): the eval filters unavailable packages via
// yoloUnavailablePackages and builds the rest, so nix stays green and the CLI decides. The
// alternative — launch anyway — masks a typo and a genuinely-unavailable package behind
// the same message, and the user meets it later as a command that mysteriously does not
// exist. Here it is worse than on macos-user, because an opt-in image does not contain the
// package either.
func (o *Options) planStorePackages(cfg *jsonx.OrderedMap, rt, repoRoot string, storeMounted bool) (storePackagesPlan, bool) {
	// STDERR, matching the refusals below rather than diverging from them: every
	// print in this file is a launch notice, and stdout belongs to the jailed
	// command. The failures here were already on stderr while the two progress
	// lines were not, which is the split that let C8's twin break CI.
	out := o.pr(o.Stderr)
	if !envTruthy(o.Getenv(StorePackagesOptInEnv)) {
		return storePackagesPlan{}, true
	}
	if ok, why := storePackagesEligible(rt, o.IsMacOS, storeMounted); !ok {
		out.printf("[bold yellow]%s=1 ignored:[/bold yellow] %s.\n"+
			"[dim]Building the image with its packages baked in, as usual.[/dim]",
			StorePackagesOptInEnv, why)
		return storePackagesPlan{}, true
	}

	// The IMAGE is Linux whatever the host is, and eligibility already refused every
	// non-Linux host — so the jail's platform filter is the right one here and the
	// materializer's native system IS the image's system.
	pkgs := config.EffectivePackages(cfg, config.PlatformLinux)
	plan := storePackagesPlan{Active: true}
	if len(pkgs) == 0 {
		return plan, true
	}

	out.print("[dim]Realizing `packages:` from the nix store (the image will not " +
		"contain them)…[/dim]")
	var profile string
	var skipped []string
	var err error
	o.withStderrProgress("Building `packages:` with nix", func() bool {
		profile, skipped, err = o.materializeStorePackages()(repoRoot, pkgs)
		return err == nil
	})
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Could not realize `packages:` from the nix "+
			"store:[/bold red] %s\n"+
			"[dim]Unset %s to build an image with them baked in instead.[/dim]",
			err.Error(), StorePackagesOptInEnv)
		return storePackagesPlan{}, false
	}
	if len(skipped) > 0 {
		o.pr(o.Stderr).printf("[bold red]These packages have no build for this "+
			"jail:[/bold red] %s\n\n"+
			"The jail did not start, because a package you declared would have been "+
			"silently missing inside it. A TYPO is the most common cause — an unknown "+
			"attribute name is indistinguishable from a package with no build for this "+
			"platform, so check the spelling first.",
			strings.Join(skipped, ", "))
		return storePackagesPlan{}, false
	}
	plan.Profiles = append(plan.Profiles, profile)
	return plan, true
}

// addImageExtras is C5: realize `.#yoloImageExtras` — the `fullPackages` set plus the
// chromium graphics stack the /lib farm used to link — and append it to the plan's
// profiles, so the launch can build the LEAN image instead.
//
// A SEPARATE LIST, BEHIND THE USER'S, and the order is the whole of the collision rule.
// The jail's farm is first-wins and links YOLO_STORE_PROFILES before
// YOLO_STORE_FHS_PROFILES, so the workspace's own `packages:` profile leads and yolo's
// stock extras fill in behind it. The list is separate because the two differ in where
// their libraries go: the extras' stay off LD_LIBRARY_PATH (see FHSProfiles). That reproduces the precedence a baked image already has —
// `packages:` and `fullPackages` both land in the image's `contents`, and a workspace that
// declares a version of a tool yolo also ships expects its own.
//
// It runs AFTER the user profile for the same reason, and only for an active plan: a
// launch that bakes must not pay for a build whose output it will not use.
func (o *Options) addImageExtras(plan storePackagesPlan, repoRoot string) (storePackagesPlan, bool) {
	if !plan.Active {
		return plan, true
	}
	o.pr(o.Stderr).print("[dim]Realizing the image's bulk extras from the nix store " +
		"(the image will be the lean variant)…[/dim]")
	var profile string
	var err error
	o.withStderrProgress("Building the image's bulk extras with nix", func() bool {
		profile, err = o.buildImageExtras()(repoRoot)
		return err == nil
	})
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Could not realize the image's bulk extras from "+
			"the nix store:[/bold red] %s\n"+
			"[dim]Unset %s to build the full image instead.[/dim]",
			err.Error(), StorePackagesOptInEnv)
		return storePackagesPlan{}, false
	}
	plan.FHSProfiles = append(plan.FHSProfiles, profile)
	return plan, true
}

// buildImageExtras returns the seam or the real `nix build .#yoloImageExtras`, rooted
// through gcRooter.
func (o *Options) buildImageExtras() func(string) (string, error) {
	if o.BuildImageExtras != nil {
		return o.BuildImageExtras
	}
	root := o.gcRooter()
	return func(repoRoot string) (string, error) {
		return realBuildImageExtras(repoRoot, root, o.Stderr)
	}
}

// realBuildImageExtras realizes `.#yoloImageExtras` and roots it with root, which is nil in a
// jail whose launcher stated no host path map: that build stays unrooted, as every in-jail
// build was before translated roots (gcRooter).
//
// TWO-STEP ROOTING, not an `--out-link`, and the difference from the user profile is that
// the store path is not knowable before the build: the extras profile is keyed by the
// FLAKE, so its content-addressed root name only exists once nix has printed the path.
// This is `image.RegisterImageRoot`'s pattern, with its window, for the same reason — and
// it files under paths.PackageRootsDir rather than build/roots because
// `prune.PruneOrphanImageRoots` sweeps the latter for anything no loaded IMAGE needs, which
// this is not (paths.go says so in as many words).
func realBuildImageExtras(repoRoot string, root image.Rooter, out io.Writer) (string, error) {
	if out == nil {
		out = io.Discard
	}
	profile, err := buildImageExtrasProfile(repoRoot)
	if err != nil {
		return "", err
	}
	if root != nil {
		// On the host this is what keeps a `nix-collect-garbage` from deleting the toolset
		// of a jail that is running right now, since the lean image no longer references
		// this closure; in a jail it is the same root, translated (NR-D2).
		rootExtrasProfile(profile, root, out)
	}
	return profile, nil
}

// buildImageExtrasProfile is the nix build half of realBuildImageExtras. A package variable
// so a test can drive the rooting half without a nix daemon.
var buildImageExtrasProfile = nixBuildImageExtrasProfile

// nixBuildImageExtrasProfile runs `nix build .#yoloImageExtras` and returns its store path.
func nixBuildImageExtrasProfile(repoRoot string) (string, error) {
	argv := []string{"nix"}
	argv = append(argv, image.NixFlakeFlags()...)
	// --impure is carried for consistency with every other flake-evaluating call here,
	// not because this attr needs it: `yoloImageExtras` forces no `builtins.getEnv`, and
	// that purity is the point of C5 — the extras derivation must not vary with the
	// environment or it multiplies the way §1.5 measured the image multiplying.
	argv = append(argv, "build", "--impure", "--no-link", "--print-out-paths",
		"--print-build-logs", ".#yoloImageExtras")
	argv = reporoot.FlakeArgv(repoRoot, argv)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := nixchildren.Run(cmd); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	var profile string
	for _, ln := range strings.Split(stdout.String(), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			profile = s
		}
	}
	if profile == "" {
		return "", errors.New("nix build of .#yoloImageExtras produced no store path")
	}
	return profile, nil
}

// rootExtrasProfile creates the durable GC root for the extras closure through root.
// Best-effort and warned-about rather than fatal, matching image.RegisterImageRoot: an
// unrooted-but-running jail is the state that existed before any of this, not a regression
// to hard-fail on.
func rootExtrasProfile(storePath string, root image.Rooter, out io.Writer) {
	rootPackageProfile(extrasProfileRootLink(storePath), storePath, root, out,
		"could not register a GC root for the store-delivered image extras "+
			"(a nix-collect-garbage could reclaim them)")
}

// rootPackageProfile registers link, under paths.PackageRootsDir, as storePath's GC root
// through root: the one mechanism behind both profile roots registered after their build —
// the extras' everywhere, and the store-delivered packages' in a jail. Best-effort, a roots
// directory that cannot be made being a warning like any other failure to root.
func rootPackageProfile(link, storePath string, root image.Rooter, out io.Writer, failMsg string) {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		fmt.Fprintln(out, "Warning: could not create GC-root dir: "+err.Error())
		return
	}
	_ = root(link, storePath, out, failMsg)
}

// extrasProfileRootLink is the extras closure's GC root, keyed by its store path.
func extrasProfileRootLink(storePath string) string {
	sum := sha256.Sum256([]byte(storePath))
	return filepath.Join(paths.PackageRootsDir(), "extras-"+hex.EncodeToString(sum[:])[:16])
}

// materializeStorePackages returns the seam or the real implementation.
//
// C4's host half is `internal/darwinpkg` VERBATIM (§3.3), which is why there is almost
// nothing here. That package's own doc comment already declares the mechanism
// platform-neutral with Linux as the next consumer: `system` defaults to the RUNNING
// platform, the flake attr is `yoloNoncontainerPackages`, and nixpkgs' availableOn is a
// per-system predicate. On the only hosts store delivery is eligible on — Linux — the
// running platform IS the jail's platform, so the default is correct rather than merely
// convenient.
func (o *Options) materializeStorePackages() func(string, []any) (string, []string, error) {
	if o.MaterializeStorePackages != nil {
		return o.MaterializeStorePackages
	}
	inJail := o.inJail()
	root := o.gcRooter()
	return func(repoRoot string, packages []any) (string, []string, error) {
		link := storeProfileRootLink(packages)
		// IN A JAIL THE BUILD TAKES NO OUT-LINK: nix would register it under the jail's
		// spelling, a root the host daemon prunes as stale (internal/darwinpkg/gcroot.go
		// verified it), so the root is registered after the build instead, translated
		// (in-jail-nix-roots.md NR-D2) — with the two-step's window, which the image root
		// has always had. That root is best-effort like every in-jail one (§4, "Failure"),
		// so only the host's build needs the link's directory before it runs: nix does not
		// create an --out-link's parent, and there the out-link IS the root.
		outLink := ""
		if !inJail {
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				return "", nil, err
			}
			outLink = link
		}
		res, err := materializeProfileAt(repoRoot, packages, "", outLink, nil)
		if err != nil {
			return "", nil, err
		}
		if inJail && root != nil {
			rootPackageProfile(link, res.ProfilePath, root, o.Stderr,
				"could not register a GC root for the store-delivered packages "+
					"(a nix-collect-garbage could reclaim them)")
		}
		return res.ProfilePath, res.Skipped, nil
	}
}

// materializeProfileAt is darwinpkg.MaterializeAt. A package variable so a test can drive
// the rooting around it without a nix daemon.
var materializeProfileAt = darwinpkg.MaterializeAt

// storeProfileRootLink is where this launch's profile is rooted against a host
// `nix store gc` — on the host nix's own `--out-link`, which registers the root as part of
// the build it is already running rather than in a second process with a window in between,
// and in a jail a translated root registered after the build (materializeStorePackages).
//
// KEYED BY CONTENT, unlike darwinpkg.ProfileRootLink's fixed leaf, and gcroot.go states
// exactly which case is which. A fixed leaf is right when at most one profile is current
// per home — a changed `packages:` retargets the link and the old closure becomes
// collectable. A machine running JAILS is the other case: several workspaces with
// different lists are live at once, so a fixed leaf would let one launch unroot the
// closure another jail is executing from. This is image.ImageRootsDir's rule, applied to
// the same problem.
func storeProfileRootLink(packages []any) string {
	key, err := jsonx.DumpsCompact(packages)
	if err != nil {
		// Unreachable for a config-derived list, and the fallback still keys on the
		// packages rather than collapsing every list onto one leaf.
		key = fmt.Sprintf("%v", packages)
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(paths.PackageRootsDir(), "jail-"+hex.EncodeToString(sum[:])[:16])
}
