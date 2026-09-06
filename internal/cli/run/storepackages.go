package run

// storepackages.go is the HOST half of C4/C5 — the launch's decision about WHERE this
// jail's packages come from (docs/design/image-staging-vs-baking.md §4 C4/C5).
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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	// Profiles are the buildEnv store paths, in PRECEDENCE order — the jail's farm is
	// first-wins, so the user's own `packages:` profile leads.
	Profiles []string
}

// storePackagesEnv returns the `-e YOLO_STORE_PROFILES=…` pair for an active plan with
// something to deliver, and nil otherwise. An active plan with no profiles emits nothing:
// there is no farm to build, and an empty variable would make the jail claim store
// delivery is live while linking nothing.
func (p storePackagesPlan) env() []string {
	if !p.Active || len(p.Profiles) == 0 {
		return nil
	}
	return []string{"-e", entrypoint.StoreProfilesEnv + "=" + strings.Join(p.Profiles, ":")}
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
	out := o.pr(o.Stdout)
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
	profile, skipped, err := o.materializeStorePackages()(repoRoot, pkgs)
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
	return func(repoRoot string, packages []any) (string, []string, error) {
		outLink := storeProfileRootLink(packages, inJail)
		if outLink != "" {
			// nix does not create an --out-link's parent.
			if err := os.MkdirAll(filepath.Dir(outLink), 0o755); err != nil {
				return "", nil, err
			}
		}
		res, err := darwinpkg.MaterializeAt(repoRoot, packages, "", outLink, nil)
		if err != nil {
			return "", nil, err
		}
		return res.ProfilePath, res.Skipped, nil
	}
}

// storeProfileRootLink is where this launch's profile is rooted against a host
// `nix store gc` — nix's own `--out-link`, which registers the root as part of the build
// it is already running rather than in a second process with a window in between.
//
// KEYED BY CONTENT, unlike darwinpkg.ProfileRootLink's fixed leaf, and gcroot.go states
// exactly which case is which. A fixed leaf is right when at most one profile is current
// per home — a changed `packages:` retargets the link and the old closure becomes
// collectable. A machine running JAILS is the other case: several workspaces with
// different lists are live at once, so a fixed leaf would let one launch unroot the
// closure another jail is executing from. This is image.ImageRootsDir's rule, applied to
// the same problem.
//
// IN-JAIL IT RETURNS "" — the unrooted `--no-link` build — for the reason imageload.go's
// rootImageFn already gives for images: the gcroots dir is unmounted and the host daemon
// prunes a root that points into a jail home as stale (verified, gcroot.go). Rooting there
// is a lie, and an out-link into a read-only home would fail the build outright.
func storeProfileRootLink(packages []any, inJail bool) string {
	if inJail {
		return ""
	}
	key, err := jsonx.DumpsCompact(packages)
	if err != nil {
		// Unreachable for a config-derived list, and the fallback still keys on the
		// packages rather than collapsing every list onto one leaf.
		key = fmt.Sprintf("%v", packages)
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(paths.PackageRootsDir(), "jail-"+hex.EncodeToString(sum[:])[:16])
}
