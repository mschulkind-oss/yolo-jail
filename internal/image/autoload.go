package image

// How this relates to the native (macos-user) nix path — what each produces, the
// rebuild/reload cost model, and the macOS Linux-builder offload:
// docs/reference/nix-across-backends.md

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// AutoLoadOptions carries the injectable seams for AutoLoadImage so the load
// pipeline is testable without a real nix/podman. Zero fields get real
// implementations.
type AutoLoadOptions struct {
	// Runtime is "podman" or "container".
	Runtime string
	// RepoRoot is the nix build cwd.
	RepoRoot string
	// SkipBuild suppresses the nix build (and the macOS build-offload) entirely,
	// jumping straight to the existing-image / cached-tar fallback. Set by the
	// run slice on a DEGRADED launch (D2): when repo-root resolution failed there
	// is no flake to build from, so building would either error in an empty cwd
	// or — worse — evaluate against the process's own cwd. A cached image is the
	// only honest option; if none exists AutoLoadImage fails with a degraded
	// diagnosis rather than a nix-build one.
	SkipBuild bool
	// Perf is the launch's timing collector, so the phases INSIDE an image load
	// are visible individually. It arrives nil on every non-timing launch and
	// every method on it is a no-op then, so the spans below are unconditional.
	//
	// This exists because `launch.auto_load_image` was ONE span over four
	// unrelated things — the nix build, the stream, podman's load, and the tar
	// materialize — and a real host measured 175s in it with no way to say which
	// part that was. The fixes for a slow nix build and a slow 3.2 GB stream have
	// nothing in common, so the number had to be split before it could be acted
	// on.
	Perf *perf.Log
	// ExtraPackages is the config `packages` list (JSON-encoded into
	// YOLO_EXTRA_PACKAGES). nil/empty → unset.
	ExtraPackages []any
	// Attr is the flake attribute to build — ImageAttrDefault or, for a launch that
	// delivers the image's bulk extras from the mounted nix store, ImageAttrLean (C5).
	// "" => ImageAttrDefault, so every caller that does not know about the lean variant
	// keeps building the image it always did.
	//
	// It reaches the load decision only through the STORE PATH the build returns: the two
	// attrs realize different derivations, so C2's content-addressed ref names them apart
	// with no further work, and a machine may hold both without either evicting the other.
	Attr string
	// Out receives the human progress/status lines (rich markup already
	// stripped by the caller's printer; here we write plain text). nil =>
	// io.Discard.
	//
	// ⚠ ON THE RUN PATH THIS IS THE JAIL COMMAND'S OWN STDOUT (`run.imageLoadOptions`
	// passes `o.Stdout`), NOT the launch report stream — which is stderr, where
	// "Flake source:" and "Jail binaries:" go. Everything written here therefore
	// lands in the output of whatever the user asked the jail to run. That is
	// survivable only because every existing line below is on a COLD path (a build
	// failure, a cache load, a first-ever load), so a warm launch writes nothing.
	// A line on the warm path belongs on Report instead: one was added here on
	// 2026-09-13 and broke two integration tests that compare a command's stdout
	// exactly, within hours.
	Out io.Writer
	// Report receives launch-stream DISCLOSURES — the lines a launch owes the user
	// about what it is about to run, which OQ-RO3 says may be compressed but never
	// suppressed. Separate from Out precisely because those two streams have
	// different readers: Out is consumed by whoever is reading the jail command's
	// output, Report by the human watching the launch. nil => Out, which keeps
	// every existing caller and test unchanged.
	Report io.Writer
	// IsMacOS overrides the platform for the build-offload branch.
	IsMacOS bool
	// Getpid names the PID-unique out-link. nil => os.Getpid.
	Getpid func() int
	// BuildStorePath runs the nix build and returns (storePath, stderrTail).
	// nil => the real nix build. Injected for tests.
	BuildStorePath func(repoRoot string, extra []any, outLink string) (string, []string)
	// BuildOffload attempts the macOS container-builder offload after a plain
	// build fails: it starts a Linux builder container and retries the nix build
	// with a --builders line pointing at it. Returns (storePath, stderrTail);
	// "" if the offload is unavailable or also failed. nil => the real offload
	// (containerbuilder session); a nil-returning stub disables it (Linux, tests).
	BuildOffload func(repoRoot string, extra []any, outLink string) (string, []string)
	// Run runs a subprocess (image inspect / load), returning (rc, ran). nil =>
	// real exec. Used for the runtime-side probes only.
	Run func(argv []string) (rc int, ran bool)
	// LayerCopy is C9's delivery seam: `skopeo copy nix:<image.json> <dest>`,
	// one process, no pipe and no archive on either side. imageJSON is the store
	// path the nix build resolved to (a nix2container manifest, not a stream
	// script); dest is the transport-qualified destination the runtime needs —
	// ContainersStorageDest for podman, OCIArchiveDest for Apple Container.
	// Returns (report, ok); ok=false means the image is NOT delivered and the
	// reason was already printed, by this seam, because only it holds skopeo's
	// stderr. nil => the real copy.
	//
	// dest is a PARAMETER rather than something the seam derives from imageJSON
	// so a test can assert that the ref the pipeline RETURNS is the ref it asked
	// the copier to create — two values that must agree and previously could not
	// be compared. It is also the whole of C2 now: nix2container's image.json
	// carries no repo:tag, so this argv is the only name the image can get and
	// there is nothing left to retag afterwards (ContainersStorageDest says so
	// at length).
	//
	// It is a seam of its own rather than a widening of Run because Run's
	// contract is "run an argv and give me its exit status": it captures no
	// stderr, and a copy that fails without saying what skopeo said is the C1
	// silent-fallback defect one layer down.
	//
	// prefix is the namespace the copier must run in, decided by the CALLER before
	// anything runs (StoreWritePrefix): nil for a destination that needs none, and
	// `podman unshare --` for a rootless containers-storage, which cannot have its
	// layer ownership mapped by the copier acting alone (storewrite.go measures
	// why). It is a PARAMETER for the same reason dest is — two values that must
	// agree with what the pipeline decided, and a seam that derived it for itself
	// could be right while the pipeline's own decision rotted unread.
	LayerCopy func(imageJSON, dest string, prefix []string) (CopyReport, bool)
	// Rootless reports whether the runtime's containers-storage is a ROOTLESS
	// store, which is the whole of the namespace decision above. nil => the real
	// `podman info` probe.
	//
	// A seam rather than a direct call so the three-way decision is drivable from a
	// table test: this jail's podman is rootful and no test host can be trusted to
	// be otherwise, so an injected answer is the only way every branch is reachable.
	Rootless func() PodmanRootless
	// BuildCopier realizes `.#imageCopier` and returns (skopeoPath, stderrTail);
	// "" means the build failed. nil => the real build.
	//
	// A SEPARATE SEAM FROM BuildStorePath, and not folded into it, because the
	// two builds have different LIFETIMES and different triggers: the image is
	// built whenever the launch needs a content ref (which is how one is
	// computed), while the copier is built only on a launch that is about to
	// copy — a launch whose image is already loaded must build nothing. Folding
	// them would put a potential 2m27s skopeo compile in front of every warm
	// start.
	BuildCopier func(repoRoot string) (string, []string)
	// PresentDigests reports the layer digests the runtime's store already holds,
	// for the copied/skipped line the launch prints. nil => the real probe.
	//
	// REPORTING ONLY. The copy negotiates per blob with containers-storage on its
	// own and never consults this; a wrong answer changes a printed number and no
	// behavior (PresentLayerDigests says why that licenses the approximation).
	PresentDigests func() map[string]struct{}
	// DiagnoseFailure maps a nix stderr tail to (title, remedy). nil => a plain
	// join (the caller normally passes nixdiag.DiagnoseNixBuildFailure bound
	// with the resolved remedy).
	DiagnoseFailure func(stderrTail []string) (title, remedy string)
	// LoadArchive loads an archive that ALREADY EXISTS at path into the runtime —
	// the second half of delivery on the two backends that cannot be copied into
	// directly (deliverViaArchive says which and why). nil => real.
	//
	// IT NO LONGER CONVERTS ANYTHING, and that is the C9 change on Apple
	// Container. It used to take a docker-archive the stream script had written
	// and run one of two converters over it — `skopeo copy docker-archive:…
	// oci:…` plus a `tar cf`, or `podman load` + `podman tag` + `podman save
	// --format oci-archive` — each of which wrote a SECOND full-size file and
	// needed a skopeo or a podman on PATH. The copy writes the loader's own
	// format directly now, so both converters and the PATH lookup are gone, and
	// the name is still chosen going in because the copy names it.
	LoadArchive func(path string) bool
	// RegisterRoot registers a durable nix GC root for the loaded image's store
	// path so a `nix-collect-garbage` at any moment cannot delete the running
	// jail's closure (the storage-lifecycle §1 invariant). Called on every
	// success return where the store path is known — idempotent, so an
	// already-loaded image re-asserts (and self-heals) its root each run.
	//
	// MUST be a host-side registration: in-jail the gcroots dir is unmounted and
	// the host daemon prunes a jail-home root as stale, so the run slice injects
	// the real image.RegisterImageRoot ONLY when !inJail and a no-op otherwise.
	// nil => a no-op (tests, and any caller that cannot root host-side).
	RegisterRoot func(storePath string)
	// LockHousekeeping takes the machine-wide housekeeping lock and returns the
	// release. It brackets the inspect-and-record step only — see the call site
	// for the window it closes and why it must not span the stream. nil => no
	// locking, which is the right default for a caller that has no host-side
	// lock (tests, and the in-jail path where nothing else is reaping).
	LockHousekeeping func() func()
	// LockImageCopy takes the machine-wide IMAGE-COPY lock and returns the
	// release. It brackets the re-inspect-and-copy step — copylock.go carries the
	// measurement that forced it and why it is a second lock rather than a wider
	// LockHousekeeping, and the call site says what the re-inspect is for.
	//
	// nil => THE REAL FLOCK, which is the one place this seam deliberately
	// diverges from LockHousekeeping beside it. That one defaults to no locking
	// because it protects the load path against the HOST's reaper, and in-jail
	// there is no reaper to lose a race with and the lock file is not even on a
	// shared filesystem. This one protects a launch against other LAUNCHES
	// writing the same image store, and a nested jail's podman store is shared by
	// every launch out of that jail exactly as a host's is — so a caller with
	// nothing to decide here should get the lock, not silence.
	LockImageCopy func() func()
	// EvalIdentity evaluates (never builds) the image identity the flake at
	// repoRoot describes — the oracle behind the pre-build stock-image check
	// (stockimage.go). nil => the real `nix eval --raw .#imageIdentity`.
	//
	// A seam because it is the ONE input to "must this launch build at all?"
	// that cannot be faked with a Run answer, and because the check has to be
	// drivable in both directions from a unit test: a host whose nix answers and
	// a host whose nix does not are different behaviors, and the fall-back one
	// is the one a wrong implementation would silently take forever.
	EvalIdentity func(repoRoot string) (string, bool)
	// copier is the skopeo path BuildCopier resolved, cached for the duration of
	// one AutoLoadImage call. It is not a seam: the seam is BuildCopier, and this
	// is the one place its answer is remembered so the delivery does not build
	// twice — once for the span and the attr-naming failure report, once inside
	// the copy.
	copier string
	// LookupEnv resolves the StaleImageEnv escape hatch (see the fatality
	// argument on the currentPath=="" branch). nil => os.LookupEnv.
	//
	// It is a seam rather than a bare os.Getenv because the FATALITY of a failed
	// build is now behavior worth pinning in both directions, and a test that has
	// to mutate the process environment to pin it would also silently change
	// meaning on a developer machine that happens to export the variable.
	LookupEnv func(key string) (string, bool)
}

func (o *AutoLoadOptions) fill() {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Getpid == nil {
		o.Getpid = os.Getpid
	}
	if o.BuildStorePath == nil {
		o.BuildStorePath = func(repoRoot string, extra []any, outLink string) (string, []string) {
			return buildImageStorePath(o.Attr, repoRoot, extra, outLink, o.Out)
		}
	}
	if o.BuildOffload == nil {
		o.BuildOffload = func(repoRoot string, extra []any, outLink string) (string, []string) {
			return buildImageWithContainerBuilder(o.Runtime, o.Attr, repoRoot, extra, outLink, o.Out)
		}
	}
	if o.Run == nil {
		o.Run = func(argv []string) (int, bool) {
			cmd := exec.Command(argv[0], argv[1:]...)
			if err := cmd.Run(); err != nil {
				if _, ok := err.(*exec.ExitError); ok {
					return cmd.ProcessState.ExitCode(), true
				}
				return 0, false
			}
			return 0, true
		}
	}
	if o.LayerCopy == nil {
		o.LayerCopy = func(imageJSON, dest string, prefix []string) (CopyReport, bool) {
			return o.copyImageLayers(imageJSON, dest, prefix)
		}
	}
	if o.Rootless == nil {
		o.Rootless = func() PodmanRootless {
			return PodmanRootlessness(o.Runtime, runCapture)
		}
	}
	if o.BuildCopier == nil {
		o.BuildCopier = func(repoRoot string) (string, []string) {
			return BuildImageCopier(repoRoot, o.Out)
		}
	}
	if o.PresentDigests == nil {
		o.PresentDigests = func() map[string]struct{} {
			return PresentLayerDigests(o.Runtime, runCapture)
		}
	}
	if o.DiagnoseFailure == nil {
		o.DiagnoseFailure = func(tail []string) (string, string) {
			if len(tail) == 0 {
				return "nix build failed", ""
			}
			t := tail
			if len(t) > 10 {
				t = t[len(t)-10:]
			}
			return "nix build failed", strings.Join(t, "\n")
		}
	}
	if o.LoadArchive == nil {
		o.LoadArchive = func(path string) bool {
			rc, ran := o.Run(ImageLoadCmd(o.Runtime, path))
			if ran && rc == 0 {
				return true
			}
			fmt.Fprintln(o.Out, "Failed to load the image archive into "+o.Runtime+".")
			return false
		}
	}
	if o.RegisterRoot == nil {
		o.RegisterRoot = func(string) {} // no-op: no host-side rooting available
	}
	if o.LockImageCopy == nil {
		o.LockImageCopy = func() func() {
			// THE WAIT NOTICE GOES TO THE LAUNCH STREAM, not to Out, and the two
			// lines this lock adds are deliberately split across the two writers.
			// Out is the jail command's OWN STDOUT on the run path (see the Out
			// field), so a "this launch is paused" notice written there is invisible
			// to the human whose terminal is stalled — which is the entire failure
			// the notice exists to prevent. The copy's own report stays on Out
			// beside the copied/skipped line it belongs with.
			return lockImageCopy(ImageCopyLockPath(), copyLockNotices{
				waiting: func(s string) { fmt.Fprintln(o.reportWriter(o.Out), s) },
				warn:    func(s string) { fmt.Fprintln(o.reportWriter(o.Out), "Warning: "+s) },
			})
		}
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	// AFTER LookupEnv, not before: stockInputs reads the environment through it,
	// so a nil seam here would be dereferenced by the very first thing
	// AutoLoadImage does.
	if o.EvalIdentity == nil {
		o.EvalIdentity = EvalImageIdentity
	}
}

// staleImageAllowed reports whether the operator has EXPLICITLY consented to
// launching on an image this invocation could not rebuild. Any non-empty value
// counts (the repo's existing YOLO_BYPASS_SHIMS / YOLO_TEST_REBUILD_IMAGE
// idiom); consent is about intent, not about the token.
func (o *AutoLoadOptions) staleImageAllowed() bool {
	v, _ := o.LookupEnv(StaleImageEnv)
	return strings.TrimSpace(v) != ""
}

// LoadResult reports what AutoLoadImage made available — and, the half that
// makes C2 real rather than decorative, WHICH REF the caller must put in the
// container argv.
//
// It is a RETURN VALUE rather than an out-param on AutoLoadOptions on purpose.
// An out-param would have cost zero edits at the seventeen existing call sites,
// which is precisely the objection: a test could set it and assert on it while
// the run slice never read it, and deleting the run slice's read would leave the
// unit gate green. RegisterRoot is the standing proof that shape rots quietly —
// it is nil in-jail and nothing pins it. A changed return type makes every call
// site a compile error, which is the cheapest enforcement that the new value is
// acknowledged.
type LoadResult struct {
	// OK reports whether an image is ready to run. The caller MUST NOT launch
	// the jail on false — the actionable reason was already printed.
	OK bool
	// Ref is the image ref to run. On the normal path it is the
	// CONTENT-ADDRESSED ref (JailImageRef): the image built from StorePath and
	// no other. On the degraded fallbacks it is the legacy JailImage(runtime)
	// tag, which is the only name those branches can honestly claim — they have
	// no store path to hash. Empty when OK is false.
	Ref string
	// StorePath is the nix store path the image was built from, or "" on the
	// degraded fallbacks, where it is genuinely unknown.
	StorePath string
}

// AutoLoadImage ports auto_load_image: ensure the nix jail image is built +
// loaded into the container runtime. Returns OK=true when an image is ready to
// run (freshly loaded, already loaded, or a cached/existing image is usable),
// OK=false when none could be made available (the caller MUST NOT launch the
// jail on false — the actionable reason was already printed), together with the
// REF that image answers to.
//
// A build that RAN AND FAILED is fatal by default: it is reported in full (the
// classification AND nix's own stderr) and returns false rather than quietly
// running the jail on whatever image happens to be loaded. Set
// YOLO_ALLOW_STALE_IMAGE=1 to proceed on the stale image anyway — the report is
// printed either way, so a run can never look successful while silently stale.
// The argument for that default lives on the currentPath=="" branch below.
//
// The macOS from-source build-offload is wired (J3): when the plain build fails
// on macOS, BuildOffload starts a Linux builder container and retries the build
// over ssh-ng before falling back to a cached tar / failure diagnosis. On Linux
// the offload is never consulted. The behavioral end-to-end (real container +
// remote build) is the mac-ac-container-builder runbook (Track M).
func AutoLoadImage(opts AutoLoadOptions) LoadResult {
	opts.fill()
	o := &opts
	out := o.Out

	sentinel := filepath.Join(paths.BuildDir(), "last-load-"+o.Runtime)
	outLink := filepath.Join(paths.BuildDir(), fmt.Sprintf("run-result-%d", o.Getpid()))
	pkgJSON := ""
	if len(o.ExtraPackages) > 0 {
		if s, err := jsonx.DumpsCompact(o.ExtraPackages); err == nil {
			pkgJSON = s
		}
	}

	// ASK THE RUNTIME BEFORE BUILDING ANYTHING (stockimage.go).
	//
	// Until 2026-09-13 this function's first act was `nix build`, unconditionally
	// — the store path it returned was how the content ref got computed, so "is
	// the image already here?" could not be asked until after the build that
	// question exists to avoid. That is the second link of the chain in
	// docs/reference/image-staging-vs-baking.md#the-stock-tag-and-the-question-asked-before-the-build,
	// and it is NOT caused by the first: making
	// the identity content-addressed fixed the integration harness's oracle and
	// left this alone, because nothing here ever compared an identity.
	//
	// The stock check is a real short-circuit and not a hatch: it runs the image
	// only when the runtime holds one TAGGED as the stock image of the identity
	// this checkout evaluates to. A mismatch, an unanswerable oracle, a lean
	// attr, any `packages:` entry — every one of them falls through to the build
	// below, which is what happened unconditionally before.
	//
	// It is also what a darwin host has needed since the binaries left the image:
	// the image closure holds derivations no public cache serves, so a darwin
	// launch that must build needs a Linux builder it may not have. Now it only
	// needs one when the image it wants is genuinely absent.
	identity := o.stockIdentity()
	if ref := o.stockImageLoaded(identity); ref != "" {
		// A DISCLOSURE, not progress: this is the launch saying which image it is
		// about to run and on what evidence, and the launch stream has no quiet
		// mode (docs/reference/report-tiers.md, OQ-RO3).
		fmt.Fprintln(o.reportWriter(out), "Image build skipped: "+ref+" already carries "+
			"this source tree's identity ("+identity+").")
		_ = os.Remove(outLink)
		return LoadResult{OK: true, Ref: ref}
	}

	var currentPath string
	var buildTail []string
	// buildFailed is the DISTINCTION the fallback branch below could not previously
	// make: currentPath ends up empty for two unrelated reasons, and they deserve
	// opposite treatment.
	//
	//   - SkipBuild suppressed the build. Nothing ran, nothing failed; the
	//     cached-image fallback IS the plan (D2's degraded launch). Stay quiet.
	//   - A build RAN and returned "". BuildStorePath's contract is explicit that
	//     an empty store path means failure (see buildImageStorePathArgs: every
	//     early return pairs "" with a stderr tail), so inside the !SkipBuild
	//     block an empty currentPath is a failed build and nothing else — including
	//     the "nix command not found" case, which is a failure the human very much
	//     needs to hear about.
	//
	// It is deliberately set INSIDE the block and AFTER the macOS offload, so an
	// offload that rescued the build is not reported as a failure.
	buildFailed := false
	if !o.SkipBuild {
		bsp := o.Perf.Span("image.nix_build")
		currentPath, buildTail = o.BuildStorePath(o.RepoRoot, o.ExtraPackages, outLink)
		bsp.End()

		// macOS build-offload (J3): a from-source `packages:` build needs Linux. If
		// the plain build failed on macOS, start a container builder and retry the
		// build over ssh-ng before falling back to a stale cache. On Linux (or when
		// the offload is disabled) BuildOffload is a nil-returning stub.
		if currentPath == "" && o.IsMacOS {
			if off, offTail := o.BuildOffload(o.RepoRoot, o.ExtraPackages, outLink); off != "" {
				currentPath, buildTail = off, offTail
			} else if len(offTail) > 0 {
				buildTail = offTail
			}
		}
		buildFailed = currentPath == ""
	}

	if currentPath == "" {
		// IS A FAILED BUILD FATAL? Yes by default, with an explicit escape hatch.
		//
		// The alternatives, and why they lose:
		//
		//   (a) Loud but always continuing. Attractive because a jail that will
		//       not start when the cache holds a perfectly good image is worse for
		//       a developer who is offline or out of disk. It fails on the second
		//       consumer: nothing reads a warning it did not ask for. The macOS
		//       nightly printed its way through this exact failure and the humans
		//       still spent the morning on the lib farm. A warning is a hint; the
		//       problem is that the run went on to produce CONFIDENT wrong results.
		//   (b) Fatal, no way past it. Turns a transient cache timeout into "you
		//       cannot work today", with no remedy on a plane or a full disk.
		//   (c) THIS: fatal when a build was expected and failed; a one-token
		//       opt-in to proceed anyway, which still prints the whole report and
		//       states the staleness.
		//
		// (c) is the only one where a stale run is impossible to obtain by
		// accident. The escape hatch is not a weakening of the rule — it is what
		// makes the rule affordable, because the developer who takes it has SAID
		// the image is stale, which is precisely the knowledge whose absence
		// caused the bug. Note the asymmetry that makes this safe to default:
		// refusing costs a rerun with one env var, while continuing costs an
		// investigation into the wrong layer.
		//
		// SkipBuild is untouched by all of this: no build was attempted, so there
		// is no failure to report and the pre-existing degraded path runs as
		// before. Warning there would train the reader to ignore the warning.
		if buildFailed {
			title, remedy := o.DiagnoseFailure(buildTail)
			staleOK := o.staleImageAllowed()
			fmt.Fprint(out, buildFailureReport(title, remedy, buildTail, staleOK))
			if !staleOK {
				_ = os.Remove(outLink)
				return LoadResult{}
			}
		}

		// THIS BRANCH KEEPS THE LEGACY :latest REF, and it is not an oversight.
		// C2 addresses an image by the hash of the store path it was built from;
		// here there IS no store path — the build was suppressed (SkipBuild) or it
		// ran and failed. A content ref invented from nothing would name an image
		// that may not exist, so the only honest question left is the one this
		// branch has always asked: is *an* image present under the name the flake
		// bakes? This branch is why the normal path still bothers to point :latest
		// at what it loaded (pointLatestAt) instead of leaving the legacy name to
		// rot — it is the only fuel this branch has, and since C9 no backend writes
		// a tar it could find, so what it loads is always a legacy one.
		//
		// Either SkipBuild (nothing was attempted) or a failed build the operator
		// explicitly opted to ignore. If the image already exists in the runtime,
		// proceed.
		imageName := JailImage(o.Runtime)
		if rc, ran := o.Run(ImageInspectCmd(o.Runtime, imageName)); ran && rc == 0 {
			fmt.Fprintln(out, "Using existing "+imageName+" image.")
			return LoadResult{OK: true, Ref: imageName}
		}
		// No image in runtime — try the most recent cached tar. The tar's own
		// RepoTags name the loaded image, and the flake bakes `tag = "latest"`, so
		// what lands is imageName either way.
		cacheDir := filepath.Join(paths.GlobalCache(), "images")
		for _, tarFile := range newestTars(cacheDir) {
			// P4 (minimal-disk-footprint.md §5, §10 step 2): newestTars is a
			// LISTING, and by the time a candidate's turn comes a concurrent `yolo
			// prune --apply` may have taken it. This reader runs on EVERY backend,
			// which is why the guard is here and not only on the converter path
			// below. Re-verifying at the point of use keeps someone else's reclaim
			// from being announced as this launch's load and then reported as this
			// launch's load failure.
			//
			// It does not make the window empty — the file can go one instruction
			// later — but that residual is already survivable here: the loop tries
			// the next candidate, which is exactly what a vanished tar deserves. The
			// converter path that made the same race FATAL is GONE since C9 (nothing
			// holds a tar between two steps any more; deliverToAppleContainer writes
			// a temp file it removes itself), so this re-check is the whole guard.
			if !fileExists(tarFile) {
				fmt.Fprintln(out, "Skipping cached image "+filepath.Base(tarFile)+
					": it was removed after the cache was listed (a concurrent reclaim?).")
				continue
			}
			fmt.Fprintln(out, "Loading image from cache: "+filepath.Base(tarFile))
			// ONE ARM, WHERE C9 FOUND TWO. Apple Container used to come through a
			// converter here because the cached file was a docker-archive its loader
			// could not read; the tars this branch can now find are LEGACY ones, and
			// `ImageLoadCmd` already spells the per-runtime argv (`container image
			// load -i` vs `podman load -i`), so there is nothing left to convert and
			// no reason for this loop to know which backend it is on.
			if rc, ran := o.Run(ImageLoadCmd(o.Runtime, tarFile)); ran && rc == 0 {
				fmt.Fprintln(out, "Done: loaded image from cache")
				return LoadResult{OK: true, Ref: imageName}
			}
		}
		// Genuinely no image available. On a degraded (SkipBuild) launch no build
		// was attempted, so a nix-build diagnosis would be a lie — emit a
		// degraded-specific message instead.
		if o.SkipBuild {
			fmt.Fprintln(out, "Cannot start jail: no jail image is loaded or cached, "+
				"and the yolo-jail source tree could not be located to build one.")
			fmt.Fprintln(out, "Fix: reinstall so the flake bundle ships with the binary "+
				"(`just install`), or set `YOLO_REPO_ROOT` to a checkout, then run `yolo` "+
				"once to build + cache the image. The cwd is never consulted.")
			return LoadResult{}
		}
		// Only reachable with buildFailed && the stale escape hatch set: the build
		// failure was already reported in full above (title, remedy and nix's
		// stderr), so repeating the diagnosis here would just bury the one new
		// fact — that the fallback the operator opted into does not exist either.
		fmt.Fprintln(out, "Cannot start jail: the image build failed (reported above) and "+
			"there is no loaded or cached image to fall back on.")
		return LoadResult{}
	}

	// THE LOAD DECISION BELONGS TO THE RUNTIME, NOT TO THE SENTINEL (C2).
	//
	// This used to ask "is the runtime's :latest tag the image THIS store path
	// built?", answered by comparing currentPath against the single
	// most-recently-loaded sentinel entry. The comment here recorded why equality
	// beat mere membership in the last-10 history, and it is preserved rather than
	// deleted because it is the argument FOR what replaced it:
	//
	//     "Comparing against the most-recently-loaded path (not mere map/set
	//      membership across the last-10 history) matters because nix builds are
	//      content-addressed: reverting a config change can reproduce a store path
	//      that's still in the history from an earlier load, even though a
	//      different, newer path has since become :latest."
	//
	// That is a description of not knowing what :latest is — unavoidable while one
	// tag names every image, and exactly why LRU membership was the wrong answer.
	// Content addressing DISSOLVES the question instead of answering it: when the
	// ref IS the store-path hash, "is this ref present" has no ambiguity left to
	// resolve, and the reverted-config scenario stops being representable — the
	// image for path A either is in the runtime under its own name or is not, no
	// matter what has been loaded since. **Do not "simplify" this back into an LRU
	// membership test on :latest** (docs/reference/image-staging-vs-baking.md, "The
	// content-addressed image ref", WARNING block).
	//
	// The sentinel survives, demoted from authority to two jobs it is still the
	// right instrument for: the human-readable diagnosis below (which path this
	// machine used last, so "load needed" says WHY), and the store-GC REFUSAL —
	// internal/prune/imageroots_probe.go's ProtectedImagePaths, read by
	// prunecmd.go's UnrootedProtectedPaths, declines to collect the store while a
	// recently-loaded closure lacks a durable root. It is NOT liveness evidence
	// anywhere: PruneOrphanImageRoots lost its protected set to OQ-LS1 and image
	// retention lost it to OQ-LS3
	// (docs/reference/image-retention.md#why-its-this-way).
	//
	// It is also not written by every success any more: a launch that matched the
	// stock tag above built nothing and has no path to append (stockimage.go).
	contentRef := JailImageRef(o.Runtime, currentPath)
	// SERIALISED AGAINST THE REAPER (disk-levers-and-backfill.md OQ-BF5). The
	// window this closes is narrow and real: this launch inspects, decides the
	// image is present, and only records it in the sentinel much further down —
	// and in between, another launch's housekeeping pass can see an image with
	// no container on it and no sentinel entry, remove it, and leave this
	// launch's `podman run` failing on an image that existed a moment ago.
	//
	// THIS LOCK BRACKETS THE INSPECT AND THE RECORD, AND NOTHING ELSE — but the
	// reason is no longer the one that used to be written here, and the old one
	// is preserved because it is the argument against what replaced it:
	//
	//     "holding it across a multi-gigabyte load would serialise every image
	//      load on the machine to buy nothing, since a load in progress is not
	//      what the reaper can misread."
	//
	// THE SECOND HALF OF THAT SENTENCE STILL HOLDS AND THE FIRST HALF INVERTED,
	// because the mechanism it described was deleted. The C3 STREAM shipped every
	// byte on every load, so two concurrent loads cost exactly what two
	// sequential ones cost: serialising them really did buy nothing but a wait.
	// The C9 COPY negotiates per blob, and containers-storage can only answer for
	// a layer it has already COMMITTED — a copy in flight is invisible to every
	// other copy — so N launches copying one image at once do N times the work
	// that one copy plus N-1 no-ops would. MEASURED 2026-09-14: a reboot launched
	// 11 jails and 5 of them copied one identical 3.45 GB image simultaneously,
	// ~4 minutes each in the store write.
	//
	// So serialising the COPY now buys a great deal — and it is still not this
	// lock's job. The answer is a SECOND, SEPARATE lock (copylock.go) taken
	// around the re-inspect-and-copy further down, rather than a widening of this
	// one, for the reason the unchanged half of the old sentence gives: a copy in
	// progress is not what the reaper can misread, so holding the HOUSEKEEPING
	// lock across it would park every reaper pass on the machine behind a
	// four-minute copy for nothing.
	//
	// nil => unlocked (tests, and any caller with no host-side lock to take).
	unlock := func() {}
	if o.LockHousekeeping != nil {
		unlock = o.LockHousekeeping()
	}
	rc, ran := o.Run(ImageInspectCmd(o.Runtime, contentRef))
	imagePresent := ran && rc == 0
	if imagePresent {
		// Already present: record it now, under the lock, and the window is
		// closed outright. The append further down is idempotent.
		_ = AddLoadedPath(sentinel, currentPath)
	}
	unlock()
	lastLoaded, hasLastLoaded := CurrentLoadedPath(sentinel)

	// THE REF IS NO LONGER CONDITIONAL. It used to be a variable a failed retag
	// could downgrade to the legacy tag; since the image is named inside the
	// archive, a green load has produced contentRef and nothing else, and a load
	// that was not green returns below without a ref at all.
	if !imagePresent {
		// The three-way diagnosis is preserved; only its AUTHORITY moved. The
		// runtime decides whether a load happens, and the sentinel explains it.
		switch {
		case !hasLastLoaded:
			fmt.Fprintln(out, "Image load needed: first run (no images loaded into "+o.Runtime+" yet)")
		case lastLoaded == currentPath:
			fmt.Fprintln(out, "Image load needed: sentinel claims loaded, but "+contentRef+
				" is missing from "+o.Runtime+" (storage reset / pruned?)")
		default:
			fmt.Fprintln(out, "Image load needed: nix store path changed")
			fmt.Fprintln(out, "  new: "+currentPath)
			if pkgJSON != "" {
				fmt.Fprintln(out, "  packages: "+pkgJSON)
			}
		}
		// C9: THE DELIVERY IS A COPY THAT NEGOTIATES, AND IT IS THE ONLY ONE.
		//
		// The history is worth carrying because two earlier shapes are still the
		// obvious things to reach for. This branch used to materialize the whole
		// image into cache/images/<key>.tar and hand `podman load -i` the file
		// (485 GiB of retained tars on one machine — OQ-DF1, "stream, keep zero
		// tars"); C3 then joined the nix stream script to `podman load`'s stdin
		// and wrote no tar of ours, which still shipped 3.47 GB per load because
		// a docker-archive cannot ask the destination what it already has, and
		// still cost podman a 3.55 GB spool to /var/tmp on the way in.
		//
		// Now `.#ociImage` IS a manifest, so `skopeo copy nix:… <dest>` asks
		// containers-storage for each blob before sending it and there is no
		// archive on either side. MEASURED 2026-09-09: a flake.nix-only edit
		// moves ONE layer of 26.2 MiB out of 91, in 1.4 s, where the stream
		// re-shipped every byte in 39.5 s (and 12.8 s even when nothing had
		// changed at all).
		//
		// THERE IS NO SECOND MECHANISM AND NO FALLBACK (§3.5, OQ-LI5). A failed
		// copy abandons the launch — it does not stream, because there is nothing
		// left to stream with, and a fallback that hid a broken new mechanism
		// would produce confident wrong results, which is the C1 defect that made
		// a failed nix build fatal in the first place. `YOLO_ALLOW_STALE_IMAGE=1`
		// remains the hatch for "get back in on the image I already have", and it
		// is orthogonal to how the next image is delivered.
		//
		// The COPIER is built here rather than beside the image: a launch whose
		// image is already loaded never reaches this branch, and must not pay a
		// cold skopeo compile for an image it is not going to copy.
		csp := o.Perf.Span("image.copier_build")
		copierTail := o.resolveCopier()
		csp.End()
		if o.copier == "" {
			// Same treatment as any failed image build, because it IS one: the
			// classification plus nix's own stderr, and no launch. The stale hatch
			// still applies to the image already loaded, which is why this reports
			// through the same path rather than inventing a second failure shape.
			title, remedy := o.DiagnoseFailure(copierTail)
			fmt.Fprint(out, buildFailureReport(title, remedy, copierTail, false))
			fmt.Fprintln(out, "Cannot start jail: the image copier ("+ImageCopierAttr+
				") could not be built, so the image cannot be delivered.")
			_ = os.Remove(outLink)
			return LoadResult{}
		}

		// The layer inventory reads the NIX MANIFEST, which is content-addressed
		// and which no other launch can change, so it is read out here rather than
		// under the copy lock below. The store-side half of the report (which
		// digests the runtime already holds) is not like that and has moved — see
		// the probe under the lock.
		layers, invErr := ReadLayerInventory(currentPath)
		if invErr != nil {
			// A manifest we cannot parse is not a reason to refuse: skopeo reads it
			// itself and is the authority. Losing the report costs a printed line.
			fmt.Fprintln(out, "Note: could not read the image manifest's layer list ("+
				invErr.Error()+"); the copied/skipped figures below are omitted.")
		}

		// SERIALISED AGAINST EVERY OTHER LAUNCH ON THIS MACHINE (copylock.go),
		// which is a different lock, a different window and a different argument
		// from the housekeeping lock above.
		//
		// WHY THE COPIER BUILD STAYS OUTSIDE IT. A waiter that finds the image
		// already delivered will have compiled skopeo for nothing — but that build
		// is a nix derivation every launch on the machine shares, so the peer we
		// waited for has just populated the store it reads and nix serialises two
		// identical derivations by itself. Pulling it inside would put a cold
		// 2m27s compile in front of every other launch's copy to save a cache hit.
		//
		// THE SPAN IS SEPARATE FROM image.layer_copy, deliberately: that span is
		// the acceptance criterion layer-aware-image-delivery.md §3.10 sets a 15 s
		// budget against for a warm launch, and folding a lock wait into it would
		// make a launch that behaved perfectly read as a regression.
		clsp := o.Perf.Span("image.copy_lock")
		copyUnlock := o.LockImageCopy()
		clsp.End()

		// RE-INSPECT UNDER THE LOCK, because the whole reason for waiting is that
		// the answer can have changed while we waited. The inspect further up
		// decided this launch needed a copy; if the peer we just queued behind was
		// delivering the SAME content ref, that decision is now stale and acting
		// on it would copy 3.45 GB over an identical image. Without this the lock
		// would turn five simultaneous copies into five sequential ones — slower
		// than the bug it was added to fix.
		//
		// The ref is content-addressed, so "present" is the whole question: an
		// image under this name IS the image this store path builds, and nothing
		// about which launch put it there can make it the wrong one.
		peerDelivered := false
		if rc, ran := o.Run(ImageInspectCmd(o.Runtime, contentRef)); ran && rc == 0 {
			peerDelivered = true
		}

		// WHICH DESTINATION, decided from facts the launcher already has and never
		// from a failure. `containers-storage` is the only one that negotiates, and
		// it is reachable exactly when the store it writes is the store the runtime
		// reads: podman on Linux. deliverViaArchive carries the two cases where it
		// is not, and says why each is a property of the backend rather than a
		// preference.
		lcp := o.Perf.Span("image.layer_copy")
		delivered := peerDelivered
		var present map[string]struct{}
		switch {
		case peerDelivered:
			// Nothing to copy and nothing to undo. The image is in the store under
			// the ref this launch is about to run, which is the only thing the copy
			// was ever for.
		case o.Runtime == "container" || o.IsMacOS:
			// THE ARCHIVE ARM GAINS MORE FROM THE LOCK THAN THE COPY ARM DOES, and
			// it is worth saying because it is not a waste-of-bytes argument. The
			// transient archive's name is keyed by the STORE PATH ALONE
			// (archiveTempPath) with no pid and no random suffix, so two launches
			// delivering one image here were not duplicating work — they were
			// writing the same file at the same time, and then each loading
			// whatever the interleaving left.
			present = o.PresentDigests()
			delivered = o.deliverViaArchive(currentPath, contentRef)
		default:
			// The already-present probe runs BEFORE the copy or it measures nothing,
			// and now AFTER the wait as well — asked here it describes the store
			// this copy is about to negotiate with, where its old position ahead of
			// the lock described the store as it was before whichever peer we
			// queued behind finished landing its layers. Reporting only, so the
			// move is worth a truer number and nothing else (see the seam doc).
			present = o.PresentDigests()
			// AND FROM WHICH NAMESPACE, the second half of the same decision and made
			// on the same terms: asked before the copy starts, answered from what
			// podman IS, never from a failed attempt (storewrite.go). A rootless store
			// has to have its layer ownership mapped through /etc/subuid, which the
			// copier cannot arrange for itself on a host that restricts unprivileged
			// user namespaces — so the copy runs inside podman's own.
			rootless := o.Rootless()
			fmt.Fprintln(out, StoreWriteNote(rootless))
			prefix := StoreWritePrefix(o.Runtime, rootless)
			_, delivered = o.LayerCopy(currentPath, ContainersStorageDest(contentRef), prefix)
		}
		lcp.End()
		// RELEASED AS SOON AS THE STORE WRITE IS DONE, and before the tags and the
		// report below: a tag is a name, the next launch's copy is 3.45 GB, and
		// holding the lock across anything that is not the write itself spends the
		// waiter's time on work that cannot collide.
		copyUnlock()
		if !delivered {
			// The seam already printed skopeo's own words and said that no image
			// was written; this line is the headline it hangs under.
			fmt.Fprintln(out, "Error delivering image to "+o.Runtime+".")
			_ = os.Remove(outLink)
			return LoadResult{}
		}
		switch {
		case peerDelivered:
			// SAID, not silently skipped. The copied/skipped ratio is a claim this
			// branch owes the reader (§3.10), and "no bytes moved" is an answer to
			// it — one whose reason a human otherwise cannot recover, since the
			// launch that did the work was in another terminal.
			fmt.Fprintln(out, "  Copied image: nothing — "+contentRef+" was delivered by a "+
				"concurrent launch while this one waited for the image-copy lock")
		case invErr == nil:
			// §3.10: the copied-vs-skipped ratio IS the claim, so it is printed
			// rather than left to a timing span someone has to enable.
			fmt.Fprintln(out, "  Copied image: "+ReportFor(layers, present).String())
		}
		// RUN ON THE PEER-DELIVERED PATH TOO, which is a choice and not an
		// oversight: both calls are best-effort names for an image that is now in
		// the store either way, both are idempotent, and both cost one podman call
		// against a copy that cost minutes. Re-asserting them also self-heals the
		// case where the peer delivered the bytes and its own tag failed — the
		// launch that skipped the copy is the cheapest possible place to fix that.
		if o.Runtime != "container" {
			o.pointLatestAt(contentRef)
			// The SECOND name, and the one that lets the next launch skip the
			// build entirely. identity is non-empty only when this launch's
			// inputs were the stock ones (stockimage.go, stockIdentity), so a
			// lean or extra-packages image can never acquire a stock tag.
			o.tagStockImage(contentRef, identity)
		}
		fmt.Fprintln(out, "Done: loaded image")
	}

	// Record this store path as the runtime's most-recently-USED image, on EVERY
	// success — not only when a load happened, which is where this call used to
	// live.
	//
	// C2 is what forces the move. While one tag named every image, "already
	// loaded" implied the sentinel already named this path, so re-appending was a
	// no-op. Now several images stay loaded at once and a launch can legitimately
	// run image A while the sentinel's newest entry is B: leave the append on the
	// load path and A ages out of the ten-entry LRU while a jail is running on it,
	// and prune's ProtectedImagePaths stops protecting its closure from a
	// `nix-collect-garbage` (storage-lifecycle §1). Appending on use also makes
	// the LRU mean "recently used" rather than "recently built", which is the
	// property a reaper actually wants.
	_ = AddLoadedPath(sentinel, currentPath)

	// Register a DURABLE GC root for the store path we are about to run against,
	// then drop the ephemeral per-PID out-link. currentPath is guaranteed
	// non-empty here (the currentPath=="" branch returned above), and the image
	// is loaded (freshly in this call, or already present). This is the storage-
	// lifecycle §1 invariant: the running image's closure must be reachable from
	// a registered root so a `nix-collect-garbage` at any moment is safe. The
	// call re-asserts the root every run, so an already-loaded image self-heals a
	// root that was reaped or never created. RegisterRoot is a host-side no-op
	// in-jail (see the seam doc) — where rooting is futile anyway.
	o.RegisterRoot(currentPath)
	_ = os.Remove(outLink)
	return LoadResult{OK: true, Ref: contentRef, StorePath: currentPath}
}

// pointLatestAt moves the legacy `:latest` tag onto the image just streamed
// under contentRef. It is BEST EFFORT and returns nothing: the ref this launch
// runs was decided by the load, not by this call.
//
// THE DIRECTION IS THE FIX. This used to run the other way — `podman tag :latest
// <contentRef>`, copying whatever :latest named onto the content ref — because
// the flake bakes `tag = "latest"` and an un-overridden stream cannot produce a
// content-addressed name. That made the content ref's binding depend on a SECOND
// podman call reading a shared, mutable name, and nothing serializes image loads
// across workspaces (the run lock is per-container-name). A concurrent load of a
// different config landing in that window bound this ref to the wrong image —
// and because a tag is permanent, every later launch of this config found it
// present, skipped the load, and ran the wrong image forever. C2 now names the
// image inside the archive (StreamRepoTag), so nothing is left to bind.
//
// WHY MOVE :latest AT ALL, then. Two consumers, neither of which needs it to be
// right about a particular config: the degraded fallback branch above, which has
// no store path and can only ask "is *an* image present under the name the flake
// bakes", and a human typing `podman images`. A tag is a name, not bytes —
// podman's store holds one copy of the layers however many names point at them —
// so the extra name costs nothing. Losing this race now costs a stale listing.
//
// A FAILED TAG IS NOT FATAL, and is not silent. The image is loaded under its
// content ref and that is what this launch runs, so nothing is lost but the
// legacy alias; saying so keeps a degraded fallback from later looking
// mysteriously empty, and degrading in silence would be the C1 defect one layer
// down.
func (o *AutoLoadOptions) pointLatestAt(contentRef string) {
	legacy := JailImage(o.Runtime)
	if rc, ran := o.Run(ImageTagCmd(o.Runtime, contentRef, legacy)); ran && rc == 0 {
		return
	}
	fmt.Fprintln(o.Out, "Warning: could not point "+legacy+" at "+contentRef+
		" — this launch is unaffected (it runs the content ref); a later degraded "+
		"launch may not find an image under the legacy name.")
}

// deliverViaArchive is the copy-then-load pair the backends that CANNOT be
// copied into directly need: the copy writes an archive and the runtime's own
// loader reads it back.
//
// TWO BACKENDS TAKE IT, for unrelated reasons, and the reasons are worth keeping
// apart because only one of them is about a CLI:
//
//   - **Apple Container** has no `containers-storage` at all. `container image
//     load` takes a file, so a file is what it gets.
//   - **podman on macOS** has one, but it is INSIDE THE PODMAN MACHINE VM, which
//     shares the user's home and `/private` and NOT `/nix` (the same fact C8
//     measured on 2026-09-07 and guards with `prefixUnreachableFromVM`). A local
//     `skopeo copy … containers-storage:…` would write a store the VM never
//     reads — an image that exists on the Mac and cannot be run. `podman load
//     -i` streams the archive over podman's own connection INTO the VM, which is
//     what makes it the only correct destination there.
//
// ⚠ THE SECOND CASE IS THE ONE THE DESIGN LEFT UNSERVED. §3.4 said podman/macOS
// stays "unchanged (stream into `podman load`)" — but OQ-LI5 deleted the stream,
// so "unchanged" named a mechanism that no longer exists. Without this arm that
// backend writes into the wrong store and every macOS podman launch fails on an
// image it just created. Note what it is NOT: a fallback. No failure switches
// between these paths; the BACKEND decides, before anything runs, and the launch
// says which it took.
//
// Neither backend gets the layer REUSE — an archive is a sequential tar again —
// but both get the layer plan and the deleted intermediate write. Apple
// Container wrote TWO full-size files per load before this (a docker-archive
// from the stream script, then an OCI tar converted from it) and needed a skopeo
// or a podman on `PATH` to convert between them; podman/macOS wrote none of ours
// but paid the pipe.
//
// THE FILE IS TEMPORARY, AND THAT IS A DELIBERATE NARROWING OF WHAT USED TO BE
// HERE. Before C9 the Apple Container arm materialized `cache/images/<key>.tar`
// and KEPT it, so three things followed: `newestTars` could find it and the
// degraded fallback could load it; a concurrent `yolo prune --apply` could evict
// it mid-launch, which is the P4 race the two-pass recovery loop existed for;
// and the bytes stayed on disk (OQ-DF1's 485 GiB, one machine).
//
// A temp file removes all three. The degraded branch is UNCHANGED by C9 because
// nothing it can see changed: it scans `*.tar`, these names never match, and the
// only tars left in that directory are legacy docker-archives whose baked
// `tag = "latest"` is exactly what that branch assumes. Had this written its
// archive into the cache instead, the degraded branch would have loaded it and
// then claimed `:latest` for an image named by its content ref — a launch that
// fails at `podman run`, on the path that exists to rescue a launch.
//
// The cost is that a storage reset re-copies rather than re-loading a kept file,
// which is the same trade C3 already made for podman-on-Linux: preferring an
// unverified leftover file to a verified copy is how one truncated tar bricks a
// workspace until a human deletes it by hand.
//
// ⚠ ONE ARM IS MEASURED NOW; THE OTHER IS NOT. On 2026-09-19 the self-hosted arm64
// Mac ran `container image load -i` against a skopeo-written `oci-archive` and the
// jail it produced started and passed all six Apple Container parity tests. So the
// AC bytes-and-loader pair is no longer an argument.
//
// What that measurement did NOT cover is this function. It came from `just load`'s
// archive hop (Justfile, the `container` arm), which writes the same destination and
// calls the same loader through a DIFFERENT caller — so what is proven is that the
// format and the loader agree, not that this call site assembles them correctly.
//
// STILL UNMEASURED ENTIRELY: `podman load -i` against a skopeo-written
// `docker-archive`, the macOS-podman arm. Nobody has run it. OQ-LI2 makes the
// measurement a precondition of trusting these backends rather than a follow-up, and
// for that arm the precondition is still outstanding.
func (o *AutoLoadOptions) deliverViaArchive(imageJSON, contentRef string) bool {
	dest, archivePath, err := o.archiveDestination(imageJSON, contentRef)
	if err != nil {
		fmt.Fprintln(o.Out, "Error preparing the image archive: "+err.Error())
		return false
	}
	// skopeo's archive destinations will not write over an existing file, and a
	// leftover from a killed launch is exactly the state that would otherwise make
	// every later launch fail on a file nobody remembers writing.
	_ = os.Remove(archivePath)
	defer os.Remove(archivePath)
	// NO NAMESPACE PREFIX, and it is not an omission. An archive is an ordinary
	// file: the ownership recorded inside it is data, not something the filesystem
	// has to be able to represent, so nothing needs a subuid mapping and there is
	// nothing to unshare for. (It is also unavailable here — `podman unshare` is
	// meaningless on Apple Container and refuses on a podman that is not rootless.)
	if _, ok := o.LayerCopy(imageJSON, dest, nil); !ok {
		return false
	}
	return o.LoadArchive(archivePath)
}

// archiveDestination returns the transport-qualified destination and the file
// path behind it for the runtime this launch is delivering to.
func (o *AutoLoadOptions) archiveDestination(imageJSON, contentRef string) (dest, path string, err error) {
	if o.Runtime == "container" {
		path, err = archiveTempPath(imageJSON, ociArchiveSuffix)
		if err != nil {
			return "", "", err
		}
		return OCIArchiveDest(path, contentRef), path, nil
	}
	path, err = archiveTempPath(imageJSON, dockerArchiveSuffix)
	if err != nil {
		return "", "", err
	}
	return DockerArchiveDest(path, contentRef), path, nil
}

// The two transient-archive suffixes. ⚠ NEITHER MAY END IN `.tar`: `newestTars`
// filters on that glob, so a crashed launch would leave the degraded fallback a
// candidate it loads and then mis-names `:latest`.
const (
	ociArchiveSuffix    = ".oci-archive.tmp"
	dockerArchiveSuffix = ".docker-archive.tmp"
)

// archiveTempPath is where an archive-delivering backend writes its transient
// file: beside the image cache, because that directory is already sized for a
// multi-GB image and lives on the same filesystem. Keyed by store path so two
// concurrent launches of different configs cannot collide on one file.
func archiveTempPath(storePath, suffix string) (string, error) {
	dir := filepath.Join(paths.GlobalCache(), "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, keyFor(storePath)+suffix), nil
}

// resolveCopier realizes `.#imageCopier` once per AutoLoadImage call and caches
// the path, returning the nix stderr tail. A "" copier afterwards means the
// build failed and the caller must refuse the launch.
//
// It is idempotent so that a caller which reaches the copy without having gone
// through the delivery branch (a test wiring LayerCopy's real implementation
// directly) still gets a copier rather than an empty argv.
func (o *AutoLoadOptions) resolveCopier() []string {
	if o.copier != "" {
		return nil
	}
	path, tail := o.BuildCopier(o.RepoRoot)
	o.copier = path
	return tail
}

// copyImageLayers is the LayerCopy seam's real implementation: run the copier the
// delivery branch already resolved, and leave the accounting to the caller.
//
// The report is assembled by the CALLER from the manifest and the present-digest
// probe rather than here, because only the caller knows whether it wants the
// figures printed — and because the copy must not depend on a reporting probe
// having succeeded.
//
// prefix arrives from the caller's namespace decision and is passed STRAIGHT
// THROUGH to the argv. Nothing here re-derives it: a seam that decided for itself
// what the pipeline also decided is two answers that can drift, and the one this
// function ran would be the one no test could see.
func (o *AutoLoadOptions) copyImageLayers(imageJSON, dest string, prefix []string) (CopyReport, bool) {
	if tail := o.resolveCopier(); o.copier == "" {
		printTail(o.Out, "the image copier's build said", tail)
		return CopyReport{}, false
	}
	return CopyReport{}, copyImageWithRetry(copyArgv(prefix, o.copier, imageJSON, dest), o.Out)
}

// runCapture runs an argv and returns its stdout, ok=false for anything that did
// not exit 0. It backs the PresentDigests probe, which needs OUTPUT where
// AutoLoadOptions.Run only reports an exit status.
//
// Not a seam on AutoLoadOptions and deliberately package-level: PresentDigests
// is the seam, and it is the level a test wants — a stub there pins the reported
// figures without anyone having to model two `podman` argvs and their stdout.
func runCapture(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// buildImageStorePath ports _build_image_store_path for the run path: run
// `nix build .#ociImage --impure --out-link <outLink> --print-build-logs` in
// repoRoot, streaming a summary and retaining the last 30 stderr lines. Returns
// (resolvedStorePath, stderrTail); storePath "" on failure.
func buildImageStorePath(attr, repoRoot string, extra []any, outLink string, out io.Writer) (string, []string) {
	return buildImageStorePathArgs(attr, repoRoot, extra, outLink, out, nil, nil)
}

// buildImageStorePathArgs is buildImageStorePath with extra nix args
// (e.g. --builders "…") and extra env (e.g. NIX_SSHOPTS) appended — the seam the
// macOS container-builder offload uses to retry the build against a remote
// builder. extraArgs/extraEnv nil => the plain build.
func buildImageStorePathArgs(attr, repoRoot string, extra []any, outLink string, out io.Writer, extraArgs, extraEnv []string) (string, []string) {
	buildEnv := os.Environ()
	if len(extra) > 0 {
		if pkgJSON, err := jsonx.DumpsCompact(extra); err == nil {
			buildEnv = append(buildEnv, "YOLO_EXTRA_PACKAGES="+pkgJSON)
		}
	}
	buildEnv = append(buildEnv, extraEnv...)
	return runNixBuild(ociBuildArgv(attr, outLink, extraArgs), repoRoot, buildEnv, outLink, out)
}

// runNixBuild runs one `nix build` to completion, streaming SummarizeNixLine's
// digest of its stderr to out and retaining the last 30 raw lines for failure
// diagnosis. Returns (resolvedStorePath, stderrTail); a "" store path means
// FAILURE and nothing else — every early return here pairs "" with a tail, and
// AutoLoadImage's buildFailed distinction rests on that contract.
//
// Shared by the image build and the install-prefix build (prefix.go) so the two
// report failures the same way: a launch that cannot produce its own
// yolo-entrypoint deserves the same treatment as one that cannot produce an
// image, and the operator should not have to learn two failure shapes.
func runNixBuild(argv []string, repoRoot string, buildEnv []string, outLink string, out io.Writer) (string, []string) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = buildEnv
	cmd.Stdout = nil
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", []string{"could not pipe nix stderr: " + err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return "", []string{"nix command not found"}
	}
	var tail []string
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		clean := strings.TrimRight(scanner.Text(), " \t\r\n")
		if clean == "" {
			continue
		}
		tail = append(tail, clean)
		if len(tail) > 30 {
			tail = tail[1:]
		}
		if summary := SummarizeNixLine(clean); summary != "" {
			fmt.Fprintln(out, summary)
		}
	}
	_ = cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		return "", tail
	}
	if resolved, err := os.Readlink(outLink); err == nil {
		return resolved, tail
	}
	return outLink, tail
}

// buildImageWithContainerBuilder is the macOS build-offload (J3): start a Linux
// builder container and retry the nix build with a --builders line pointing at
// it over ssh-ng. Returns (storePath, stderrTail); "" if the builder couldn't be
// started or the offloaded build failed. The builder is stopped before return.
//
// The ssh key management (generate the ed25519 keypair under BuilderKeyDir,
// authorize the .pub in the container via the RunArgv pubkey env) and the actual
// remote build are behaviorally verified by the mac-ac-container-builder runbook
// (Track M); here the lifecycle is driven through the containerbuilder.Session
// seams so the decision + argv construction are exercised in unit tests.
func buildImageWithContainerBuilder(runtime, attr, repoRoot string, extra []any, outLink string, out io.Writer) (string, []string) {
	pubkey, err := ensureBuilderKey()
	if err != nil {
		return "", []string{"container builder: " + err.Error()}
	}
	sess := &containerbuilder.Session{
		Runtime: runtime,
		Pubkey:  pubkey,
		Deps:    realSessionDeps(out),
	}
	fmt.Fprintln(out, "Starting the Linux builder container for the from-source build…")
	host, port, ok := sess.Start()
	if !ok {
		return "", []string{"container builder did not start"}
	}
	defer sess.Stop()

	buildersLine := sess.BuildersLine(host, port, 4)
	// ⚠ NO `--max-jobs 0`, and that is the fix for a broken macOS nightly rather
	// than tuning. It was here to force every derivation onto the Linux builder,
	// which was harmless while the image was Linux derivations end to end. Layer-
	// aware delivery introduced two that must be built NATIVELY on darwin — the
	// nix2container tooling and the copier, which read the Mac's own /nix/store —
	// and `--max-jobs 0` forbids exactly that:
	//
	//	Failed to find a machine for remote build!
	//	derivation: …-nix2container-1.0.0.drv
	//	required (system, features): (x86_64-darwin, [])
	//	1 available machines: ([x86_64-linux], 4, [], [])
	//	error: Cannot build … Reason: local builds are disabled (max-jobs = 0)
	//
	// It also bought nothing it claimed to: a darwin nix cannot build an
	// x86_64-linux derivation locally anyway, so `--builders` alone already routes
	// every Linux derivation to the container. Dropping the flag lets nix route by
	// SYSTEM — Linux out to the builder, darwin here — which is what the offload
	// always meant. Measured 2026-09-10: the nightly's shards 1 and 4 failed with
	// the error above and took 2 and 3 with them on fail-fast.
	//
	// NO NIX_SSHOPTS, AND ITS ABSENCE IS THE POINT. This used to export
	// containerbuilder.NixSSHOpts() — `StrictHostKeyChecking=no
	// UserKnownHostsFile=/dev/null` — to stop root meeting an unknown host key.
	// It never worked HERE: `builders` is a RESTRICTED nix setting, so the
	// nix-daemon consumes it and forks ssh in the DAEMON's environment, which
	// this one is not (macOS's org.nixos.nix-daemon.plist declares no
	// EnvironmentVariables). The real fix is the host key pinned in the builders
	// line's 8th field, which Session.BuildersLine now supplies.
	//
	// ⚠ AND IT ACTIVELY DEFEATED THAT PIN wherever it DID apply. ssh takes the
	// FIRST occurrence of an option and nix appends NIX_SSHOPTS ahead of its own
	// -oUserKnownHostsFile=, so `StrictHostKeyChecking=no` outranked the pinned
	// key: MEASURED 2026-09-13, a deliberately WRONG key in field 8 still
	// connected. A pin that can be overridden is not a pin, so the override is
	// gone and verification is authoritative on every path.
	extraArgs := []string{"--builders", buildersLine}
	return buildImageStorePathArgs(attr, repoRoot, extra, outLink, out, extraArgs, nil)
}

// newestTars returns *.tar files in dir sorted newest-first by mtime. Empty when
// dir is missing.
func newestTars(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type tf struct {
		path  string
		mtime int64
	}
	var tars []tf
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		tars = append(tars, tf{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	// newest first
	for i := 0; i < len(tars); i++ {
		for j := i + 1; j < len(tars); j++ {
			if tars[j].mtime > tars[i].mtime {
				tars[i], tars[j] = tars[j], tars[i]
			}
		}
	}
	out := make([]string, len(tars))
	for i, t := range tars {
		out[i] = t.path
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
