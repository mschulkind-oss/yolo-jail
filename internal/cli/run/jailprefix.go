package run

import (
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// jailprefix.go decides WHERE the jail's own copy of yolo comes from, and emits
// the two mounts that put it there.
//
// The image used to bake it: `installPrefix` laid down /opt/yolo-jail/bin/<name>
// (real files) plus the share/yolo-jail flake bundle, and /bin/<name> pointed at
// the absolute store path. That made the ~3 % of the image that is our own Go
// build responsible for ~100 % of its rebuilds — every commit under cmd/ or
// internal/ minted a new image store path, re-stored ~2.7 GB of podman layers
// and paid a `podman load` (docs/design/image-staging-vs-baking.md §1.9). The
// image now carries only the NAMES (flake.nix: jailPrefixLinks, /bin/<name> →
// /opt/yolo-jail/bin/<name>) and the launch supplies the content.
//
// THE PREFIX IS TWO DIRECTORIES:
//
//	bin/              the Linux binaries this jail will run (pid1 included)
//	share/yolo-jail/  the flake bundle the IN-JAIL yolo resolves exe-relative
//	                  (internal/reporoot.BundledSourceDirFrom: <exeDir>/../share/yolo-jail)
//
// and BOTH come from one place — a BUNDLE and the binaries it ships. There are
// two kinds of bundle and that is the whole of the branching:
//
//   - The resolved flake source, when it carries prebuilt binaries. Every
//     installed bundle does (`bin/linux-<arch>/`, staged by
//     scripts/stage-source-bundle.sh), and installPrefix bakes one INTO the
//     mounted prefix — so a nested jail inherits prebuilt binaries and never
//     compiles Go for this. Nothing to build.
//   - Otherwise — a LIVE CHECKOUT, which ships none — the bundle
//     `nix build .#installPrefix` produces, whose share/yolo-jail is a
//     self-contained flake.nix + flake.lock + bin/linux-<arch> built from that
//     very checkout.
//
// THE SHARE HALF IS NEVER THE CHECKOUT ITSELF, even though a checkout has a
// flake.nix and would satisfy reporoot. Mounting it would put the whole
// yolo-jail working tree — every file, tracked or not — inside the jail at a
// second path, and buy nothing: the in-jail `yolo` would then build its image
// from a tree whose Go code may already be newer than the binaries it is running
// (which is exactly the skew version.SourceSkew exists to refuse). The built
// bundle matches the binaries beside it by construction. An agent that wants the
// live tree in a nested jail names it the way this launch did: YOLO_REPO_ROOT.
//
// WHY NOT ONE MOUNT. A single `-v <dir>:/opt/yolo-jail:ro` would need the host to
// hold a directory in exactly the prefix shape, which no existing host layout
// has: `just install` stages bin/linux-<arch>/ beside the flake files
// (scripts/stage-source-bundle.sh), not bin/ beside share/yolo-jail/. Reconciling
// by RESTAGING would mean copying ~200 MB of binaries per launch (or a cache with
// its own staleness rule); reconciling by two mounts costs one extra `-v` and
// leaves the host layout alone. The in-jail result is identical, because
// BundledSourceDirFrom only ever looks at <exeDir>/../share/yolo-jail.

const (
	// JailPrefixDir is where the install prefix lands in the jail. The image
	// pre-creates it and its two subdirs (flake.nix fakeRootCommands) because a
	// --read-only rootfs cannot grow a mountpoint, and /bin/<name> targets it.
	JailPrefixDir = "/opt/yolo-jail"
	// JailPrefixBinDir holds the binaries. /bin/<name> symlinks point here.
	JailPrefixBinDir = JailPrefixDir + "/bin"
	// JailPrefixShareDir is the flake bundle the in-jail yolo resolves
	// exe-relative from JailPrefixBinDir. The two paths MUST stay siblings under
	// one parent with `share/yolo-jail` spelled in full, or
	// reporoot.BundledSourceDirFrom stops finding it.
	JailPrefixShareDir = JailPrefixDir + "/share/yolo-jail"
	// JailEntrypointPath is the absolute argv the container command names.
	//
	// ABSOLUTE, not the bare "yolo-entrypoint" it used to be. A bare name is
	// resolved on the IMAGE's PATH (/bin:/usr/bin) and reaches the binary through
	// the /bin/<name> symlink — which now hops through the mountpoint, so a launch
	// that failed to mount would exec a dangling link and brick pid1 with
	// "failed to exec pid1" and no mention of the cause. Naming the real path
	// makes that failure say which path is missing.
	JailEntrypointPath = JailPrefixBinDir + "/yolo-entrypoint"
)

// jailPrefix is the host side of the mount: the two directories that become
// JailPrefixBinDir and JailPrefixShareDir.
type jailPrefix struct {
	// binDir holds the linux/<arch> binaries — the shipped set, by bare name.
	binDir string
	// shareDir holds flake.nix + flake.lock (a flake bundle or a checkout).
	shareDir string
	// built records that binDir came from a nix build rather than from prebuilt
	// bundle artifacts. Reported, not branched on: the launch says where the
	// binaries it is about to run came from.
	built bool
}

// prebuiltBinDir is the per-arch prebuilt directory a flake bundle ships, if the
// resolved source has one. The name matches flake.nix's own prebuilt
// short-circuit (`./bin/linux-${goArch}`) and stage-source-bundle.sh's layout —
// three spellings of one path, and the reason they agree is that they all
// describe the bundle format, not a private convention of this file.
//
// goruntime.GOARCH, not a probe: the jail image is Linux for THIS machine's arch
// (containerJailPlatform says the same thing for the capture manifest), so the
// binaries a launch needs are linux/<GOARCH> whatever the host OS is. A darwin
// host is the case that makes this worth stating — its bundle carries Linux
// binaries precisely because the jail is Linux.
func prebuiltBinDir(root string) string {
	return filepath.Join(root, "bin", "linux-"+goruntime.GOARCH)
}

// resolveJailPrefix decides where the jail's yolo binaries and flake bundle come
// from, given the flake source reporoot.Resolve picked, building them if that
// source ships none. Returns ok=false when
// the binaries could not be produced — the caller must refuse the launch, since
// the container argv names a yolo-entrypoint that would not exist.
//
// The exists check is on yolo-entrypoint specifically, not on the directory: an
// empty or half-staged bin/linux-<arch> is the failure mode that would otherwise
// mount cleanly and die at exec, and yolo-entrypoint is the one member whose
// absence is fatal rather than degrading.
func (o *Options) resolveJailPrefix(root string) (jailPrefix, bool) {
	p, ok := o.jailPrefixSource(root)
	if !ok {
		return jailPrefix{}, false
	}
	// The darwin VM-visibility gate goes HERE, on the RESULT, and not inside
	// either arm: the one mount whose absence means "no pid1" must not be
	// reachable-or-not depending on which arm produced it. Inert off darwin.
	if msg := prefixUnreachableFromVM(p, o.IsMacOS, o.Getenv("YOLO_NIX_HOST_DAEMON")); msg != "" {
		o.pr(o.Stderr).print(msg)
		return jailPrefix{}, false
	}
	return p, true
}

// jailPrefixSource is resolveJailPrefix's two arms — the prebuilt bundle and the
// built checkout — with no policy on top of them.
func (o *Options) jailPrefixSource(root string) (jailPrefix, bool) {
	if prebuilt := prebuiltBinDir(root); o.PathExists(filepath.Join(prebuilt, "yolo-entrypoint")) {
		return jailPrefix{binDir: prebuilt, shareDir: root}, true
	}
	// STDERR, like every other launch notice and like the refusal just below
	// (warnIfNoPacks states the rule): stdout belongs to the jailed command, so
	// a notice printed there is swallowed by the user's redirect or corrupts
	// what they pipe. This sentence used to be printed by image.BuildJailPrefix
	// onto o.Stdout, and it broke CI on every runner that has to build the
	// prefix — TestBuildNoticeGoesToStderr is the regression.
	o.pr(o.Stderr).print("[dim]Building yolo's own binaries (.#installPrefix) — " +
		"they are mounted into the jail, not baked into the image…[/dim]")
	storePath, tail := o.BuildJailPrefix(root)
	if storePath == "" {
		o.pr(o.Stderr).print("[bold red]Cannot start jail: could not build yolo's own " +
			"binaries (`nix build .#installPrefix`).[/bold red]\n" +
			"They are mounted into the jail rather than baked into the image, so there is\n" +
			"nothing to fall back on — a jail without them has no yolo-entrypoint to run.")
		for _, line := range tail {
			o.pr(o.Stderr).printf("[dim]  %s[/dim]", line)
		}
		return jailPrefix{}, false
	}
	prefix := filepath.Join(storePath, image.JailPrefixSubdir)
	return jailPrefix{
		binDir:   filepath.Join(prefix, "bin"),
		shareDir: filepath.Join(prefix, "share", "yolo-jail"),
		built:    true,
	}, true
}

// jailPrefixMountArgs emits the two `-v` pairs. Read-only on purpose: nothing in
// the jail writes to its own install prefix, and Apple Container silently
// ignores `:ro` (apple/container#889) exactly as it does for every other mount
// here — the flag states the intent for the backends that honor it rather than
// pretending the guarantee is universal.
func jailPrefixMountArgs(p jailPrefix) []string {
	return []string{
		"-v", p.binDir + ":" + JailPrefixBinDir + ":ro",
		"-v", p.shareDir + ":" + JailPrefixShareDir + ":ro",
	}
}

// describeJailPrefix is the launch's one line about where the binaries it is
// about to run came from — the companion to "Flake source:", and for the same
// reason: what executes in the jail is now selected at launch time rather than
// fixed by the image's content hash, so it has to be visible without inspecting
// the argv.
func describeJailPrefix(p jailPrefix) string {
	if p.built {
		return p.binDir + " (built from the flake source)"
	}
	return p.binDir + " (prebuilt, from the flake bundle)"
}

// prefixUnreachableFromVM is the darwin pre-flight on the two mount sources: it
// returns the refusal text when this launch would bind a path the runtime VM
// cannot see, and "" when it would not.
//
// THE RULE IT ENFORCES IS NOT NEW. macOS runs containers in a VM, and that VM
// shares the user's home and /private — not /nix. yolo has always known this:
// shouldMountHostNix (hostprobes.go) SKIPS the nix store + daemon socket on
// macOS for exactly this reason, and docs/guides/macos.md states the failure it
// avoids by name — "the bind mount would fail with a statfs error at startup".
//
// C8 then made yolo's own binaries a bind mount too (2026-09-06), and a LIVE
// CHECKOUT has to build them, which puts them in /nix/store — the one tree the
// rule says is not there. Measured on the 2026-09-07 nightly (run 34117863296):
// every launch died as
//
//	Error: statfs /nix/store/…-yolo-jail-install-prefix/opt/yolo-jail/bin: no such file or directory
//
// with rc 125, taking ~30 tests down at ~30s each. The design doc had said this
// arm was unverified on hardware (image-staging-vs-baking.md §9); this is what
// the hardware said.
//
// WHY A REFUSAL AND NOT A COPY. Mirroring the prefix under $HOME would work
// without asking anyone, at ~66 MB per build plus a lifecycle to reap — a second
// delivery mechanism for the binaries, in a file whose whole subject is that
// there is exactly one. The population that hits this is a macOS developer
// pointed at a live checkout (an INSTALLED bundle stages under $HOME and is
// already fine, which is every Homebrew and `just install` user), and for them
// one line of machine setup is cheaper than a mechanism.
//
// IT REUSES YOLO_NIX_HOST_DAEMON RATHER THAN ADDING A DIAL, because that
// variable already means precisely "my runtime VM does share /nix into the
// container" (hostprobes.go, docs/guides/macos.md). A second dial for one fact
// is the shape shouldMountHostNix warns about: two dials that disagree about
// what counts as true turn "I set the variable and nothing happened" into a
// legitimate bug report. Setting it here also turns the nix delegation mounts
// on, which is not a side effect — it is the same claim about the same VM.
func prefixUnreachableFromVM(p jailPrefix, isMacOS bool, nixOptIn string) string {
	if !isMacOS {
		return ""
	}
	if envTruthy(nixOptIn) {
		return ""
	}
	// BOTH sources, not just the one a caller happens to think of first: the
	// share half is what the in-jail yolo resolves its flake bundle from, and a
	// launch that mounted one and not the other would boot and then fail
	// somewhere with no mention of the VM.
	var unreachable []string
	for _, dir := range []string{p.binDir, p.shareDir} {
		if underDir(dir, hostNixStore) {
			unreachable = append(unreachable, dir)
		}
	}
	if len(unreachable) == 0 {
		return ""
	}
	return "[bold red]Cannot start jail: the install prefix is in the nix store, " +
		"which this Mac's runtime VM does not share.[/bold red]\n" +
		"  " + strings.Join(unreachable, "\n  ") + "\n" +
		"They are BIND-MOUNTED into the jail (they are not baked into the image any\n" +
		"more), so podman would fail with `statfs …: no such file or directory` and\n" +
		"rc 125 before pid1 ran. Two ways forward:\n" +
		"  • Share /nix with the VM and say so — `podman machine init -v /nix:/nix`\n" +
		"    (a fresh machine; `-v` is set at init) and export YOLO_NIX_HOST_DAEMON=1,\n" +
		"    which is yolo's one spelling of \"my VM shares /nix\".\n" +
		"  • Or launch from an INSTALLED bundle instead of a live checkout: `just\n" +
		"    install` stages one under $HOME, which the VM does share, and it ships\n" +
		"    prebuilt binaries so nothing is built in the store at all. Unset\n" +
		"    YOLO_REPO_ROOT to select it.\n" +
		"docs/guides/macos.md — Nix daemon and /nix sharing"
}

// underDir reports whether path is dir itself or anything beneath it. Spelled as
// a path-segment test rather than a string prefix so /nix/store-of-my-own is not
// mistaken for a path inside /nix/store.
func underDir(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
