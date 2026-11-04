package cli

// packbuild.go is `yolo pack build` (docs/design/pack-buildable-programs.md §8, BG-D11): the build
// a launch would run for each PACK-TREE PROGRAM (a `program` delivered `via: "pack"`), run outside
// a launch — for CI, and for a first install that should not wait inside a launch.
//
// IT IS THE LAUNCH'S BUILD, NOT A SECOND ONE. Each program goes through the fork route's one build
// act (buildFork), with the same identity a launch computes (forkBuild: the pack-tree source, the
// tree digest as the revision, the pack-tree recipe and the jail's platform), so a build made here
// is the store entry the next launch finds as a hit, and a launch building the same key at the same
// moment is waited on through the act's own lock. The only thing this verb adds is the act's
// afterLock hook, so it can tell "the store already held it" (up to date) from "built now".
//
// On the host only: builds never run in a jail (the design's §9), and a fork's explicit rebuild is
// `yolo capture <bin>`, which this verb names rather than repeats.

import (
	"errors"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// packBuildUsage is the verb's own usage line, printed with a usage refusal.
const packBuildUsage = "usage: yolo pack build [<pack>...] [--force] [--at jail|host]"

// packBuildHostGOOS is the host's GOOS for `--at host`, a variable so a test on Linux can ask
// what a Mac would do.
var packBuildHostGOOS = goruntime.GOOS

// packBuildOptions are the verb's parsed arguments.
type packBuildOptions struct {
	packs []string
	force bool
	at    string // "jail" or "host"
	// prebuiltOptIn is the user's `programs.overrides.<bin>: {"prebuilt": true}` (BG-D18), read once
	// per run (config.ProgramPrebuiltOptIns).
	prebuiltOptIn map[string]bool
	// overrides are the user's dev overrides (config.ProgramOverrides), read once per run: a program
	// one names is built from that checkout in place of its pack's tree (BG-D19).
	overrides map[string]config.ProgramOverride
}

// parsePackBuildArgs reads `[<pack>...] [--force] [--at jail|host]`; the error is a usage refusal.
func parsePackBuildArgs(args []string) (packBuildOptions, error) {
	o := packBuildOptions{at: "jail"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--force":
			o.force = true
		case a == "--at" || strings.HasPrefix(a, "--at="):
			v, ok := strings.CutPrefix(a, "--at=")
			if !ok {
				if i+1 >= len(args) {
					return o, errors.New("--at needs a value: jail or host")
				}
				i++
				v = args[i]
			}
			if v != "jail" && v != "host" {
				return o, fmt.Errorf("--at %q: it is jail (the default) or host", v)
			}
			o.at = v
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown flag %q", a)
		default:
			o.packs = append(o.packs, a)
		}
	}
	return o, nil
}

// packBuildRuntime is the runtime a build for at runs under, handed to the act as captureFork
// hands it: "macos-user" when the build is the Mac's darwin act, and "" otherwise, so the run
// pipeline resolves a container backend as any capture does. `--at jail` builds for the
// workspace runtime's jail (macos-user when that is the runtime), `--at host` for the host notch:
// the darwin act on a Mac, a container jail's Linux build on Linux (the same build a jail gets).
func packBuildRuntime(at string) string {
	if at == "host" {
		if packBuildHostGOOS == "darwin" {
			return "macos-user"
		}
		return ""
	}
	if captureRuntime() == "macos-user" {
		return "macos-user"
	}
	return ""
}

// packBuild is `yolo pack build`. Exit 0 when nothing failed, 1 when a program failed or could not
// be read, 2 for a usage refusal, an unknown pack, or a jail.
func packBuild(args []string, out, errw io.Writer, color bool) int {
	opts, err := parsePackBuildArgs(args)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack build: %v\n  %s (see `yolo pack --help`)\n", err, packBuildUsage)
		return 2
	}
	if config.InJail() {
		cmd := strings.Join(append([]string{"yolo pack build"}, args...), " ")
		fmt.Fprintf(errw, "yolo pack build: builds never run inside a jail — run `%s` in a terminal on the host\n", cmd)
		return 2
	}
	sel := selectHostPacks(resolveStagedPack, config.UserScopeSelection())
	if sel.loadErr != nil {
		fmt.Fprintf(errw, "yolo pack build: %v\n  fix what it names in %s, then run `yolo pack build` again\n",
			sel.loadErr, paths.UserConfigDisplayPath())
		return 1
	}
	packs, rc := packBuildScope(sel, opts.packs, errw)
	if rc != 0 {
		return rc
	}
	pr := richtext.Printer{W: out, Color: color}
	failed := false
	if len(opts.packs) == 0 {
		// The whole selection: a pack that did not resolve may carry a program, so it is a failure
		// of this act, named with its reason.
		for _, u := range sel.problems() {
			pr.Printf("[red]failed[/red]     %s  [dim]%s — fix it, then run `yolo pack build` again[/dim]",
				richtext.Escape(u.Name), richtext.Escape(u.Reason))
			failed = true
		}
	}
	rt := packBuildRuntime(opts.at)
	opts.overrides = config.ProgramOverrides(func(msg string) { fmt.Fprintf(errw, "Warning: %s\n", msg) })
	opts.prebuiltOptIn = config.ProgramPrebuiltOptIns(nil)
	programs := packload.PackTreePrograms(packs)
	forks := append(packload.Forks(packs), packload.PatchedTrees(packs)...)
	for _, p := range packs {
		said := false
		for _, f := range forks {
			if f.Pack == p.Name {
				said = true
				pr.Printf("[dim]skipped[/dim]    %s  [dim]a fork's explicit rebuild is `yolo capture %s`[/dim]",
					richtext.Escape(f.Label()), richtext.Escape(f.CaptureArg()))
			}
		}
		for _, f := range programs {
			if f.Pack != p.Name {
				continue
			}
			said = true
			if !packBuildOne(pr, f, rt, opts, errw, color) {
				failed = true
			}
		}
		if !said && len(opts.packs) > 0 {
			pr.Printf("[dim]nothing to build in pack %s: it declares no program built from its own tree[/dim]",
				richtext.Escape(p.Name))
		}
	}
	if len(programs) == 0 && len(forks) == 0 && len(opts.packs) == 0 && !failed {
		pr.Printf("[dim]nothing to build: no selected pack declares a program built from its own tree (\"via\": \"pack\")[/dim]")
	}
	if failed {
		return 1
	}
	return 0
}

// resolveStagedPack is the verb's pack resolver: every entry STAGED, as a launch stages each pack
// into its own tree (config.ResolvePack with a Dest, run.Options.stagePackEntry's one call), into a
// directory of this process's pack tree (packload.ProcessPackDir). A launch digests its staged copy,
// and staging changes a tree (a local pack's symlinks become the files they name, among others), so
// a digest of the pack read in place would key a build no launch ever looks up — and the no-build
// copy would read the in-place tree, through any link it holds.
func resolveStagedPack(e config.PackEntry) (*packload.Pack, error) {
	dir, err := packload.ProcessPackDir(e.Slug())
	if err != nil {
		return nil, err
	}
	res, err := config.ResolvePack(e, config.ResolvePackSpec{Dest: dir})
	if err != nil {
		if errors.Is(err, packsrc.ErrNotFetched) && !e.IsLocal() && !e.Embedded() {
			return nil, storeMissError{err}
		}
		return nil, err
	}
	return resolvedOrProblems(e, res)
}

// packBuildScope is the packs the verb covers: every selected pack with no names, or the named
// ones, in the selection's order. A name that is not selected is refused (2), naming `yolo pack
// ls`; a named pack that is configured but did not resolve is refused (1) with its reason.
func packBuildScope(sel hostPackSet, names []string, errw io.Writer) ([]*packload.Pack, int) {
	if len(names) == 0 {
		return sel.packs, 0
	}
	byName := map[string]*packload.Pack{}
	for _, p := range sel.packs {
		byName[p.Name] = p
	}
	want := map[string]bool{}
	for _, n := range names {
		if byName[n] != nil {
			want[n] = true
			continue
		}
		for _, u := range sel.unresolved {
			if u.Name == n {
				fmt.Fprintf(errw, "yolo pack build: the pack %s is configured but did not resolve: %s — fix that, "+
					"then run `yolo pack build %s` again\n", n, u.Reason, n)
				return nil, 1
			}
		}
		fmt.Fprintf(errw, "yolo pack build: no selected pack is named %q — `yolo pack ls` lists the packs your "+
			"config selects\n", n)
		return nil, 2
	}
	var out []*packload.Pack
	for _, p := range sel.packs {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out, 0
}

// packBuildOne builds one pack-tree program through the launch's build act and prints its one
// result line: up to date, built, skipped (not published for this platform) or failed. It reports
// whether nothing failed. The act's own progress — its lock wait, the build jail's output and
// where that output is kept — goes to errw, so out carries the result lines alone.
func packBuildOne(pr richtext.Printer, f packload.Fork, rt string, opts packBuildOptions, errw io.Writer, color bool) bool {
	label := richtext.Escape(f.Label())
	retry := "yolo pack build " + f.Pack
	if opts.at == "host" {
		retry += " --at host"
	}
	fail := func(why string) bool {
		pr.Printf("[red]failed[/red]     %s  [dim]%s — then run `%s`[/dim]", label, richtext.Escape(why), richtext.Escape(retry))
		return false
	}
	platform := forkBuildPlatform(rt)
	goos, arch, _ := strings.Cut(platform, "/")
	if why := (packdecl.Install{Platforms: f.Platforms}).UnpublishedReason(goos, arch); why != "" {
		pr.Printf("[dim]skipped[/dim]    %s  [dim]%s[/dim]", label, richtext.Escape(why))
		return true
	}
	// WHICH BYTES, as a launch resolves them (packload.PackTreeResolve): the build of the pack's tree,
	// or its prebuilt artifact where the tree is absent or the user opted into it (BG-D18). Never a
	// prebuilt in place of a build that cannot run here (OQ-BG2).
	//
	// A DEV OVERRIDE builds the user's checkout in place of the pack's tree, as a launch does
	// (run.ResolvePackTreeProgram, BG-D19), and the result line names the checkout.
	pin := run.ResolvePackTreeProgram(f, platform, opts.prebuiltOptIn[f.Bin], opts.overrides)
	label = richtext.Escape(pin.Fork.Label())
	if pin.Reason != "" {
		return fail(pin.Reason)
	}
	f, digest := pin.Fork, pin.Commit
	if !f.IsPrebuilt() && rt == "macos-user" && !darwinPackTreeBuildable(hostFloorMacProbes()) {
		// A BUILD ENVIRONMENT NEVER SET UP is a fatal refusal naming the setup step and, when the pack
		// declares one for this platform, the explicit opt-in to its prebuilt — never the prebuilt.
		return fail(darwinPackTreeUnbuildableReason(f, platform))
	}
	b := forkBuild{Fork: f, Commit: digest, Platform: platform}
	store := &capture.Store{Dir: paths.CapturesDir()}
	hit := false
	mode := buildMode{
		force:          opts.force,
		lock:           pidlock.Mode{Wait: true, Bound: forkBuildWaitBound},
		runtime:        rt,
		keepJailOutput: true,
		jailStdout:     errw,
		// THE HIT CHECK, under the lock as the act's own is, recorded so the line can say "up to date":
		// a launch building the same key is waited for, and its build is then the hit (§10's row for
		// `yolo pack build` while a launch builds). --force asks for no hit, so the act builds.
		afterLock: func() (*capture.Entry, error, bool) {
			if opts.force {
				return nil, nil, false
			}
			entry, _, err := resolveForkBuild(store, f.Bin, b.Platform, b.selectSource(), b.Commit, b.recipe())
			if err != nil {
				return nil, nil, false
			}
			hit = true
			return entry, nil, true
		},
	}
	start := time.Now()
	entry, err := buildFork(b, mode, errw, errw, color)
	if err != nil {
		// Every refusal of a pack-tree build names the prebuilt opt-in where one is declared (BG-D21):
		// no runtime, a build jail that did not start, a build that failed.
		why := err.Error()
		if log := buildJailLogPath(store.StagingDir("fork-" + b.id())); fileExists(log) {
			why += withBuildLog(log)
		}
		why += f.PrebuiltOptInClause(platform)
		ok := fail(why)
		for _, l := range notStartedLines(err, sealPacks(f), "  ", "`"+retry+"`") {
			fmt.Fprintln(errw, l)
		}
		return ok
	}
	if f.IsPrebuilt() {
		// A PREBUILT is fetched, never built: its line names the sha256 that was verified and the URL.
		what := "fetched"
		if hit {
			what = "up to date"
		}
		pr.Printf("[green]%-10s[/green] %s  [dim]prebuilt artifact (%s), sha256 %s verified, %s, %s[/dim]  [cyan]%s[/cyan]",
			what, label, richtext.Escape(f.PrebuiltWhy), shortSHA(digest), richtext.Escape(f.Prebuilt.URL), platform, entry.Key)
		return true
	}
	if hit {
		pr.Printf("[green]up to date[/green] %s  [dim]tree digest %s, %s[/dim]  [cyan]%s[/cyan]",
			label, shortSHA(digest), platform, entry.Key)
		return true
	}
	pr.Printf("[green]built[/green]      %s  [dim]tree digest %s, %s, in %s[/dim]  [cyan]%s[/cyan]",
		label, shortSHA(digest), platform, time.Since(start).Round(time.Second), entry.Key)
	return true
}

// darwinPackTreeBuildable reports whether the darwin sealed act (macosuser.RunForkBuildAct, FP-D24)
// can EVER build a pack's tree on this Mac: always in the run-as-you mode, which needs no account,
// and in the account mode only once the yolo account exists. A Mac with no sandbox account has
// never set up its build environment (pack-buildable-programs.md §9, the `yolo host` macOS row): by
// OQ-BG2's ruling a build there is a fatal refusal naming `yolo yolo-account create` and, when the pack
// declares a darwin prebuilt, the explicit opt-in to it (darwinPackTreeUnbuildableReason) — never
// the prebuilt on yolo's own.
func darwinPackTreeBuildable(p macCaptureProbes) bool {
	return p.Mode == macosuser.ModeSelf || (p.SandboxUserExists != nil && p.SandboxUserExists())
}

// darwinPackTreeUnbuildableReason is the refusal of f's build on a Mac whose darwin act can never run
// (darwinPackTreeBuildable false): the setup step, and the prebuilt opt-in when f declares an
// artifact for platform (OQ-BG2).
func darwinPackTreeUnbuildableReason(f packload.Fork, platform string) string {
	src := "pack " + f.Pack + "'s " + f.From
	if f.Checkout != "" {
		src = f.SourceSaid()
	}
	why := "the yolo account " + macosuser.SandboxUser + ", which a pack's build on a Mac runs as, does not " +
		"exist, so " + f.Bin + " cannot be built from " + src + " — run the one-time " +
		"setup, `yolo yolo-account create`"
	return why + f.PrebuiltOptInClause(platform)
}

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
