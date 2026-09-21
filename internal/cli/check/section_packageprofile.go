package check

// section_packageprofile.go reports the `packages:` tool closure of a notch that has NO
// BAKED IMAGE. It was the tail of the macos-user readiness section until this file existed,
// and the move IS the change: the predicate is the PROVISIONING MECHANISM, never the
// platform (docs/design/provisioner-sets.md §9 step 2, the narrow half of its OQ-NX9).
//
// `describe` has gated the same report on `render.PrimBakedImage` being ABSENT since
// 2026-08-05 — "the question 'where does my toolset come from' has a nix-profile answer
// only below the jail notch" (printPackageProfile, internal/cli/describe.go). `check` asked
// a platform question instead: the report sat behind `isNativeRuntime` and then behind
// `checkMacosUserBackend`'s own `IsMacOS` return, so two commands disagreed about which
// environments have a nix profile at all, and a `confinement: guest` or `confinement: host`
// workspace got no diagnosis of its tool closure from `check` on any platform.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// sectionPackageProfile is THE GATE, and it is the same one `describe` uses: resolve this
// config's notch to its primitive vector and report the profile iff the vector composes no
// `PrimBakedImage`. A jail's tools come from the image, so naming a profile path there would
// name a closure no launch uses; below the jail notch the profile is the whole answer.
//
// It reads the notch and the MECHANISM through jailcontent.ConfinementProfile — the one
// lookup `describe` and the agent briefing already share, so a third spelling of "which
// primitives does this environment compose" cannot drift from those two. mechanism is the
// runtime `check` resolved (`runtimeForCheck`), which is what makes `macos-user` land below
// the jail notch whatever its `confinement` says.
//
// NO HEADER WHEN THE GATE IS CLOSED, following sectionInlineLoopholes: a section that does
// not apply prints nothing at all rather than a header over a claim. Nothing is counted and
// no [PASS] is manufactured for work that did not happen.
func (o *Options) sectionPackageProfile(r *reporter, merged *jsonx.OrderedMap, mechanism string) {
	notch, ok := render.KindForNotch(string(config.ResolveConfinement(merged)))
	if !ok {
		// Unreachable: ResolveConfinement defaults an absent or unknown value to jail, and
		// an unknown one is a Merged-Configuration [FAIL] that already returned. Written out
		// rather than discarded so a zero Kind can never reach ConfinementProfile as if it
		// were a notch — describe's own boundary crossing makes the same choice.
		return
	}
	prof := jailcontent.ConfinementProfile(notch, mechanism, o.IsMacOS)
	if prof.Has(render.PrimBakedImage) {
		return
	}
	// WHETHER A LAUNCH HERE BUILDS THE PROFILE AT ALL, which decides what an absent root
	// MEANS. Only the macos-user backend materializes it today (darwinpkg.Materialize has
	// exactly one caller, the macos-user launch); `guest` and `host` have no package layer
	// yet (provisioner-evidence.md §3.4, "not orthogonal to confinement: the provisioning
	// primitive below jail"). Reported through the same mechanism question the gate
	// asks, not through IsMacOS: a native runtime is the thing that provisions, and the
	// platform it happens to require is not what makes the sentence true.
	materializes := jailcontent.MechanismHasNoContainer(mechanism)
	platform := config.PlatformLinux
	if o.IsMacOS {
		platform = config.PlatformDarwin
	}
	declared := len(config.EffectivePackages(merged, platform))
	if !materializes && declared == 0 {
		// Nothing declared and nothing that would provision it: there is no closure to
		// describe, so the honest report is no report. A [WARN] here would be a false alarm
		// on every `confinement: host` workspace that never asked for a package.
		return
	}
	r.sectionHeader("Declared packages")
	o.checkPackageProfile(r, notch, materializes, declared)
	r.blank()
}

// checkPackageProfile reports the RESOLVED `packages:` nix profile and its GC root — the
// two facts that make a non-container notch's tool closure inspectable (N2's fourth
// sub-item; docs/design/provisioner-sets.md §10 alternative H, formerly
// noncontainer-nix-environment.md's Option 1).
//
// Read from the GC-ROOT SYMLINK, never by invoking nix: check already owns the one place a
// real build is allowed (the --build-gated image section), and resolving a profile here
// would be a second surprise build. The root is also the exactly-right oracle — it is what
// the last materialization pointed at, so it answers "which closure would a launch use".
//
// PASS/WARN split by what the user can act on. A root that resolves is the healthy state
// worth naming; an ABSENT root is a WARN rather than a FAIL because it is also the normal
// pre-first-run state — a launch creates it — and the remedy is the same either way. The
// one genuinely bad state, a root pointing at a store path that is GONE, gets its own FAIL:
// that means a GC collected the closure despite the root, which is the defect N1 fixed and
// therefore the thing worth reporting loudly if it ever recurs.
//
// materializes is what keeps the absent-root WARN TRUE now that the gate is the mechanism
// and not the platform. "A run materializes it" is a fact about the macos-user backend; at
// a notch with no provisioner it would be a remedy that does not exist, so that cell states
// the inertness instead and says there is nothing to run — P2's "a loss with no remedy says
// so" rather than borrowing one it cannot cash (docs/reference/report-tiers.md#principles).
// notch and declared are what that cell reports; the other three cells are about the ROOT
// and depend on neither. A false materializes reaches here only at `guest` or `host` — the
// gate has already returned for every notch composing an image, and the one mechanism that
// provisions is the one that makes materializes true — so naming the notch there is always
// naming one of those two.
func (o *Options) checkPackageProfile(r *reporter, notch render.Kind, materializes bool, declared int) {
	link := darwinpkg.ProfileRootLink(paths.Home())
	target, err := os.Readlink(link)
	if err != nil {
		if !materializes {
			noun := "entries"
			if declared == 1 {
				noun = "entry"
			}
			r.warn("`packages:` declares "+itoa(declared)+" "+noun+" that nothing "+
				"materializes at the `"+notch.String()+"` notch",
				"A notch with no baked image gets its tools from a nix profile, and only "+
					"the macos-user backend builds one today — `guest` and `host` have no "+
					"package layer yet (docs/design/provisioner-evidence.md §3.4).  "+
					"Nothing to "+
					"run: the entries are inert here, not wrong.")
			return
		}
		r.warn("No `packages:` nix profile resolved yet",
			"A run materializes it and GC-roots it at "+link+".  Nothing is "+
				"wrong if you have not launched this backend yet.")
		return
	}
	if !o.PathExists(target) {
		r.fail("The `packages:` GC root points at a store path that no longer exists",
			"Root: "+link+"\nTarget: "+target+"\nA nix GC collected a ROOTED "+
				"closure, which should be impossible — the next run rebuilds it, "+
				"but please report this.")
		return
	}
	r.ok("`packages:` profile resolved and GC-rooted: " + target)
}
