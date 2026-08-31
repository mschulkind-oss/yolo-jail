package image

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	// ExtraPackages is the config `packages` list (JSON-encoded into
	// YOLO_EXTRA_PACKAGES). nil/empty → unset.
	ExtraPackages []any
	// Out receives the human progress/status lines (rich markup already
	// stripped by the caller's printer; here we write plain text). nil =>
	// io.Discard.
	Out io.Writer
	// ProgressTTY reports whether Out is a real terminal. When true, the byte
	// progress redraws IN PLACE (carriage return, like Python's rich status
	// spinner) instead of one line per chunk — otherwise a multi-GB image spams
	// hundreds of "… 98%" lines. When false (piped/redirected), progress is
	// suppressed to a single start line. It governs both forms: "Streaming
	// image… " on the podman pipe and "Caching image… " on the Apple Container
	// file.
	ProgressTTY bool
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
	// Materialize streams the nix image to cacheFile, returning byte count (0 on
	// failure). nil => real streaming.
	//
	// Since C3 this is the APPLE CONTAINER path only. Podman's happy path takes
	// StreamLoad and writes no tar; the one remaining caller here is the branch
	// whose converters require a real file (ImageLoadStdinCmd says which). The
	// build-failure fallback still READS tars (newestTars) — C3 removed the
	// creation of tars on the podman happy path, not the ability to consume one.
	Materialize func(storePath, cacheFile string) int64
	// StreamLoad is C3's happy path: pipe the nix image stream straight into the
	// runtime's `load`, writing NO tar. loadArgv is what ImageLoadStdinCmd
	// returned (e.g. `podman load`, no -i). Returns (bytesStreamed, ok); ok=false
	// means the image is NOT loaded and the reason was already printed — by this
	// seam, because only it knows WHICH END of the pipe failed. nil => real
	// streaming.
	//
	// repoTag is C2's half: the name the archive's RepoTags must carry, so the
	// image is created under its content ref rather than renamed onto it
	// afterwards (StreamRepoTag says why that distinction is the whole point). It
	// is a PARAMETER rather than something the seam derives from storePath so a
	// test can assert that the ref the pipeline returns is the ref it asked the
	// loader to create — two values that must agree and previously could not be
	// compared.
	//
	// It is a seam of its own rather than a widening of Run because Run's contract
	// is "run an argv and give me its exit status" — it wires no stdin and cannot
	// express an upstream process, and a pipe has two processes, two exit statuses
	// and two stderrs to reconcile. Keeping them apart also lets a test pin the
	// DECISION (podman streams, Apple Container does not) separately from the copy
	// mechanics.
	StreamLoad func(storePath, repoTag string, loadArgv []string) (int64, bool)
	// DiagnoseFailure maps a nix stderr tail to (title, remedy). nil => a plain
	// join (the caller normally passes nixdiag.DiagnoseNixBuildFailure bound
	// with the resolved remedy).
	DiagnoseFailure func(stderrTail []string) (title, remedy string)
	// LoadAppleContainer converts+loads a tar into Apple Container under `ref`
	// (an UNQUALIFIED ref — Apple Container's CLI does not carry the localhost/
	// prefix). nil => real.
	//
	// The ref is a parameter rather than a constant because Apple Container is
	// the one backend that cannot be retagged after the fact here: its
	// converters choose the name they write into the OCI archive, so C2's
	// content-addressed name has to be handed to them going in. Callers pass
	// JailImageRef(runtime, storePath) on the normal path and JailImage(runtime)
	// on the degraded fallback, where no store path is known.
	LoadAppleContainer func(tarPath, ref string) bool
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
			return buildImageStorePath(repoRoot, extra, outLink, o.Out)
		}
	}
	if o.BuildOffload == nil {
		o.BuildOffload = func(repoRoot string, extra []any, outLink string) (string, []string) {
			return buildImageWithContainerBuilder(o.Runtime, repoRoot, extra, outLink, o.Out)
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
	if o.Materialize == nil {
		o.Materialize = func(storePath, cacheFile string) int64 {
			return materializeImage(storePath, cacheFile, o.IsMacOS, o.Out, o.ProgressTTY)
		}
	}
	if o.StreamLoad == nil {
		o.StreamLoad = func(storePath, repoTag string, loadArgv []string) (int64, bool) {
			return streamImageToRuntime(storePath, repoTag, loadArgv, o.IsMacOS, o.Out, o.ProgressTTY)
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
	if o.LoadAppleContainer == nil {
		o.LoadAppleContainer = func(tarPath, ref string) bool {
			return loadImageForAppleContainer(tarPath, ref, o.Out)
		}
	}
	if o.RegisterRoot == nil {
		o.RegisterRoot = func(string) {} // no-op: no host-side rooting available
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
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
		currentPath, buildTail = o.BuildStorePath(o.RepoRoot, o.ExtraPackages, outLink)

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
		// rot — it is the only fuel this branch has, now that C3 no longer writes
		// tars for it to fall back on either.
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
			fmt.Fprintln(out, "Loading image from cache: "+filepath.Base(tarFile))
			if o.Runtime == "container" {
				if o.LoadAppleContainer(tarFile, imageName) {
					fmt.Fprintln(out, "Done: loaded image from cache")
					return LoadResult{OK: true, Ref: imageName}
				}
			} else {
				if rc, ran := o.Run(ImageLoadCmd(o.Runtime, tarFile)); ran && rc == 0 {
					fmt.Fprintln(out, "Done: loaded image from cache")
					return LoadResult{OK: true, Ref: imageName}
				}
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
	// membership test on :latest** (docs/design/image-staging-vs-baking.md §4 C2,
	// WARNING block).
	//
	// The sentinel survives, demoted from authority to two jobs it is still the
	// right instrument for: the human-readable diagnosis below (which path this
	// machine used last, so "load needed" says WHY), and prune's liveness ledger —
	// internal/prune/imageroots_probe.go reads it to protect a live jail's closure
	// from a store GC, and it is guard #2 of PruneOrphanImageRoots' three.
	contentRef := JailImageRef(o.Runtime, currentPath)
	rc, ran := o.Run(ImageInspectCmd(o.Runtime, contentRef))
	imagePresent := ran && rc == 0
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
		// C3: ON PODMAN THE HAPPY PATH WRITES NO TAR.
		//
		// This used to materialize the whole image into cache/images/<key>.tar and
		// then hand `podman load -i` the file it had just written. The tar was a
		// redundant third copy — the nix store already holds the closure and podman's
		// own store holds the loaded layers — and it was RETAINED, which is how one
		// developer machine accumulated 485 GiB of them (OQ-DF1, ruled 2026-08-25:
		// "stream, keep zero tars"). `podman load` reads a tar on stdin, so the two
		// halves join with a pipe and the file stops existing.
		//
		// THE CACHED-TAR SHORTCUT IS GONE FROM THIS BRANCH, deliberately. It used to
		// skip materialization when <key>.tar was already there, and its premise was
		// "this code wrote that tar, so don't pay twice" — a premise C3 deletes: on
		// podman nothing writes a tar any more, so a file at that name is now a
		// LEGACY artifact of a code path that no longer runs. Three reasons not to
		// prefer it over a fresh stream: it almost never fires (a new store path is a
		// new key, so the tar exists only when podman's image store was reset while
		// the cache survived); preferring an unverified file to a verified stream
		// would let one truncated leftover brick a workspace until a human deleted it
		// by hand; and keeping it would make the podman path bimodal, so "no tar is
		// written" would be a claim about the cache's contents rather than about this
		// code. Apple Container keeps the shortcut below, where the premise still
		// holds because materializeImage is still what puts the file there.
		//
		// Existing tars are NOT orphaned by this: the degraded fallback above still
		// loads whatever newestTars finds. C3 stops CREATING them; sweeping the ones
		// already on disk is minimal-disk-footprint.md's work, not this branch's.
		if loadArgv, canStream := ImageLoadStdinCmd(o.Runtime); canStream {
			// C2: THE IMAGE IS NAMED ON THE WAY IN. StreamRepoTag is written into the
			// archive's RepoTags, so a green load has already created the image under
			// contentRef — there is no second call whose answer could disagree, and
			// therefore no window in which a concurrent load of a different config
			// could bind this ref to someone else's image. StreamRepoTag carries the
			// argument in full.
			total, streamed := o.StreamLoad(currentPath, StreamRepoTag(currentPath), loadArgv)
			if !streamed {
				// The seam already printed WHICH END failed and what it said; this
				// line is the headline it hangs under, and is the same sentence a
				// failed `podman load -i` printed before C3.
				fmt.Fprintln(out, "Error loading image into "+o.Runtime+".")
				_ = os.Remove(outLink)
				return LoadResult{}
			}
			fmt.Fprintln(out, "  Streamed image: "+FormatImageSize(total))
			o.pointLatestAt(contentRef)
		} else {
			// Apple Container's converters interpolate a PATH (skopeo's
			// docker-archive: source, `podman save -o`), so this backend still
			// materializes a real file — C3 is podman-only for the pipe form and the
			// doc says so. The image is named GOING IN: the converters write the ref
			// into the OCI archive, so there is nothing to retag afterwards.
			cacheFile, err := ImageCachePath(currentPath)
			if err != nil {
				fmt.Fprintln(out, "Error preparing image cache: "+err.Error())
				_ = os.Remove(outLink)
				return LoadResult{}
			}
			if !fileExists(cacheFile) {
				totalBytes := o.Materialize(currentPath, cacheFile)
				if totalBytes == 0 {
					fmt.Fprintln(out, "Error streaming image to cache.")
					_ = os.Remove(outLink)
					return LoadResult{}
				}
				fmt.Fprintln(out, "  Cached image: "+FormatImageSize(totalBytes))
			}
			if !o.LoadAppleContainer(cacheFile, contentRef) {
				// The converters print their own diagnosis (which of skopeo/podman
				// ran, and which step of it broke).
				_ = os.Remove(outLink)
				return LoadResult{}
			}
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

// buildImageStorePath ports _build_image_store_path for the run path: run
// `nix build .#ociImage --impure --out-link <outLink> --print-build-logs` in
// repoRoot, streaming a summary and retaining the last 30 stderr lines. Returns
// (resolvedStorePath, stderrTail); storePath "" on failure.
func buildImageStorePath(repoRoot string, extra []any, outLink string, out io.Writer) (string, []string) {
	return buildImageStorePathArgs(repoRoot, extra, outLink, out, nil, nil)
}

// buildImageStorePathArgs is buildImageStorePath with extra nix args
// (e.g. --builders "…") and extra env (e.g. NIX_SSHOPTS) appended — the seam the
// macOS container-builder offload uses to retry the build against a remote
// builder. extraArgs/extraEnv nil => the plain build.
func buildImageStorePathArgs(repoRoot string, extra []any, outLink string, out io.Writer, extraArgs, extraEnv []string) (string, []string) {
	buildEnv := os.Environ()
	if len(extra) > 0 {
		if pkgJSON, err := jsonx.DumpsCompact(extra); err == nil {
			buildEnv = append(buildEnv, "YOLO_EXTRA_PACKAGES="+pkgJSON)
		}
	}
	buildEnv = append(buildEnv, extraEnv...)
	argv := ociBuildArgv(outLink, extraArgs)
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
func buildImageWithContainerBuilder(runtime, repoRoot string, extra []any, outLink string, out io.Writer) (string, []string) {
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
	extraArgs := []string{"--builders", buildersLine, "--max-jobs", "0"}
	extraEnv := []string{"NIX_SSHOPTS=" + containerbuilder.NixSSHOpts()}
	return buildImageStorePathArgs(repoRoot, extra, outLink, out, extraArgs, extraEnv)
}

// materializeImage streams the nix image to cacheFile (via a temp + rename),
// returning the byte count (0 on failure).
//
// SINCE C3 THIS IS THE APPLE CONTAINER PATH. Podman streams straight into `load`
// and writes nothing (see streamload.go); this file form survives because
// skopeo's `docker-archive:<path>` source and `podman save -o <path>` both
// interpolate a real path and cannot consume a stream. The tar it writes is
// therefore a constraint of that backend, not an exemption from OQ-DF1 —
// minimal-disk-footprint.md has to price it.
func materializeImage(storePath, cacheFile string, isMacOS bool, out io.Writer, progressTTY bool) int64 {
	streamCmd := streamImageCommand(storePath, isMacOS)
	cmd := exec.Command(streamCmd[0], streamCmd[1:]...)
	cmd.Stderr = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0
	}
	if err := cmd.Start(); err != nil {
		return 0
	}
	tmpFile := strings.TrimSuffix(cacheFile, ".tar") + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0
	}
	var total int64
	buf := make([]byte, 1024*1024)
	sentinel := SizeSentinelPath()
	estimated := estimateImageSize(storePath, sentinel)
	// Progress rendering (rich status.update — a SINGLE line
	// that redraws): on a TTY, redraw in place with \r (throttled to whole-
	// percent changes so a multi-GB stream doesn't emit hundreds of near-
	// identical updates); off a TTY, emit nothing per-chunk (a redirected log
	// must not accumulate 500 "98% 98% 99%" lines). A final newline closes the
	// redrawn line so the next message starts cleanly.
	prog := newProgressLine(out, progressTTY, "Caching image... ")
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				prog.done()
				f.Close()
				_ = os.Remove(tmpFile)
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return 0
			}
			total += int64(n)
			prog.update(total, estimated)
		}
		if rerr != nil {
			break
		}
	}
	prog.done()
	f.Close()
	_ = cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		_ = os.Remove(tmpFile)
		return 0
	}
	if err := os.Rename(tmpFile, cacheFile); err != nil {
		_ = os.Remove(tmpFile)
		return 0
	}
	// Save size for future estimates (the writer path — see the doubled-suffix
	// quirk on SizeFileForSentinel).
	_ = os.WriteFile(sentinel, []byte(strconv.FormatInt(total, 10)), 0o644)
	return total
}

// progressLine renders byte progress as a single, in-place updating line on a
// TTY (carriage return, like Python's rich status spinner), and as nothing
// per-chunk when piped (so a redirected log doesn't accumulate hundreds of
// near-identical "… 98%" lines). Updates are throttled to when the RENDERED
// string changes (whole-percent or MB/GB rollover), so a multi-GB stream
// produces ~100 redraws, not one per 1 MB chunk.
//
// The prefix is a parameter since C3 because there are now two callers and they
// are doing different things: materializeImage really is caching a file
// ("Caching image... "), while the podman path streams straight into the runtime
// and caches nothing ("Streaming image... "). Same line, same cadence, same
// information — one accurate word.
type progressLine struct {
	out    io.Writer
	tty    bool
	last   string
	shown  bool
	prefix string
}

func newProgressLine(out io.Writer, tty bool, prefix string) *progressLine {
	return &progressLine{out: out, tty: tty, prefix: prefix}
}

func (p *progressLine) update(current, estimate int64) {
	if !p.tty {
		return // no per-chunk spam on a pipe/redirect
	}
	msg := p.prefix + FormatProgress(current, estimate)
	if msg == p.last {
		return // throttle: nothing visibly changed
	}
	p.last = msg
	p.shown = true
	// \r returns to column 0; trailing spaces clear any shorter previous line.
	fmt.Fprintf(p.out, "\r%s   ", msg)
}

// done closes the in-place line with a newline so the next message starts on a
// fresh line (only when something was drawn).
func (p *progressLine) done() {
	if p.tty && p.shown {
		fmt.Fprintln(p.out)
	}
}

// estimateImageSize ports _estimate_image_size: the cached size file (read via
// the doubled-suffix quirk path, which never exists), else the nix closure-size
// probe.
func estimateImageSize(storePath, sentinel string) int64 {
	if n, ok := ReadEstimatedSizeFile(SizeFileForSentinel(sentinel)); ok {
		return n
	}
	cmd := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"path-info", "--closure-size", storePath)
	data, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	for i := len(fields) - 1; i >= 0; i-- {
		if n, err := strconv.ParseInt(fields[i], 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// streamImageArgv is streamImageCommand plus C2's naming: `--repo_tag <tag>`
// tells nixpkgs' streamLayeredImage script to "Override the RepoTags from the
// configuration" (its own `--help`), so the archive on stdout already carries
// the content-addressed name and `podman load` creates the image under it.
//
// repoTag == "" streams the flake's baked name unchanged, which is what the
// Apple Container file form wants: its converters name the image during
// conversion, and materializeImage's tar is read back by a `podman load -i`
// whose retag reads paths.JailImage.
//
// Appending is safe on BOTH forms streamImageCommand returns: locally the argv
// is the store path itself, and over ssh it is `ssh <host> <storePath>`, where
// the extra words join the remote command line (the tag has no shell
// metacharacters — it is `<repo>:<16 hex>`).
func streamImageArgv(storePath, repoTag string, isMacOS bool) []string {
	argv := streamImageCommand(storePath, isMacOS)
	if repoTag == "" {
		return argv
	}
	return append(argv, "--repo_tag", repoTag)
}

// streamImageCommand ports _stream_image_command: on Linux the store path IS
// the executable (its shebang streams the tar); the macOS remote-builder ssh
// path is a documented narrowing (falls back to local execution).
func streamImageCommand(storePath string, isMacOS bool) []string {
	if !isMacOS {
		return []string{storePath}
	}
	machines := "/etc/nix/machines"
	data, err := os.ReadFile(machines)
	if err != nil {
		return []string{storePath}
	}
	if _, sshHost, ok := LinuxBuilderFromMachines(string(data)); ok {
		// nix copy the closure to the builder, then run the script over ssh.
		copyCmd := exec.Command("nix", "copy", "--to", "ssh-ng://"+sshHost, storePath)
		if err := copyCmd.Run(); err != nil {
			return []string{storePath}
		}
		return []string{"ssh", sshHost, storePath}
	}
	return []string{storePath}
}

// loadImageForAppleContainer ports _load_image_for_apple_container: convert the
// nix V2 tar to OCI via skopeo (preferred) or podman, then load into Apple
// Container under `ref` (unqualified — Apple Container's CLI does not carry a
// localhost/ prefix).
//
// C2 names the image HERE rather than retagging after the load, because both
// converters already choose the name they write into the OCI archive: skopeo
// takes it as the destination reference, and the podman route takes it as the
// ref `podman save` exports. That is strictly better than a post-load `container
// image tag` would be — it removes a step, and it does not depend on an Apple
// Container subcommand this repo has no way to verify.
func loadImageForAppleContainer(tarPath, ref string, out io.Writer) bool {
	if _, err := exec.LookPath("skopeo"); err == nil {
		return convertViaSkopeo(tarPath, ref, out)
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return convertViaDaemon("podman", tarPath, ref, out)
	}
	fmt.Fprintln(out, "Cannot convert Nix image to OCI format for Apple Container.")
	fmt.Fprintln(out, "Install one of: skopeo (recommended, no daemon needed) or podman.")
	return false
}

func convertViaSkopeo(tarPath, ref string, out io.Writer) bool {
	ociDir, err := os.MkdirTemp("", "yolo-oci-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(ociDir)
	if err := exec.Command("skopeo", "copy",
		"docker-archive:"+tarPath, "oci:"+ociDir+":"+ref).Run(); err != nil {
		fmt.Fprintln(out, "skopeo conversion to OCI failed.")
		return false
	}
	ociTar := tarPath + ".oci.tar"
	if err := exec.Command("tar", "cf", ociTar, "-C", ociDir, ".").Run(); err != nil {
		fmt.Fprintln(out, "Failed to create OCI tar.")
		return false
	}
	loadErr := exec.Command("container", "image", "load", "-i", ociTar).Run()
	_ = os.Remove(ociTar)
	if loadErr != nil {
		fmt.Fprintln(out, "Failed to load OCI image into Apple Container.")
		return false
	}
	return true
}

func convertViaDaemon(daemon, tarPath, ref string, out io.Writer) bool {
	if err := exec.Command(daemon, "load", "-i", tarPath).Run(); err != nil {
		fmt.Fprintln(out, "Failed to load image into "+daemon+" for conversion.")
		return false
	}
	// The tar's baked RepoTags land the image under paths.JailImage in the
	// CONVERSION daemon; name it as the Apple Container store must see it before
	// exporting, so the OCI archive's ref is the one C2 asked for. The daemon
	// here is podman, which requires the localhost/ prefix Apple Container omits.
	qualified := qualifyRef(ref)
	if err := exec.Command(daemon, "tag", paths.JailImage, qualified).Run(); err != nil {
		fmt.Fprintln(out, "Failed to tag the converted image as "+qualified+" in "+daemon+".")
		return false
	}
	ociTar := tarPath + ".oci.tar"
	if err := exec.Command(daemon, "save", "--format", "oci-archive", "-o", ociTar, qualified).Run(); err != nil {
		fmt.Fprintln(out, "Failed to export OCI image from "+daemon+".")
		return false
	}
	loadErr := exec.Command("container", "image", "load", "-i", ociTar).Run()
	_ = os.Remove(ociTar)
	if loadErr != nil {
		fmt.Fprintln(out, "Failed to load OCI image into Apple Container.")
		return false
	}
	return true
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
