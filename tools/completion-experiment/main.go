// Command pack-binaries is the PIN TOOL of docs/design/broker-as-a-pack.md §14: the one program
// that builds every official build of a pack-shipped binary, writes its url and sha256 into the
// manifest, and checks them.
//
// An official build is a program from this module's cmd/<name>, for a loophole manifest in the
// packs embed that declares `binaries` (internal/loopholedecl's binaries.go). Its platforms are
// BP-D7's, its release file BP-D8's, and its bytes come from BP-D9's recipe (recipe.go) with
// Toolchain (toolchain.go), so the digest committed before the tag is the digest of the file the
// tag's release publishes.
//
// Usage, from the checkout's root:
//
//	go run ./tools/pack-binaries pin [<version>]          write each build's sha256, and url
//	go run ./tools/pack-binaries check [<version>]        build and compare; write nothing
//	go run ./tools/pack-binaries stage <version> <dir>    check, then write each build into <dir>
//	go run ./tools/pack-binaries seed [--repin] [<dir>]   build this machine's builds into the cache
//
// WITH A VERSION, pin and check are the release's: `pin` is `just pin-pack-binaries <version>`,
// run before the release is cut and committed with its changelog section, and `check` is the
// gate `just release` runs before it tags and publish.yml runs before PyPI. `stage` is
// goreleaser's before-hook in the release itself (.goreleaser.yaml), and the files it writes are
// uploaded as the release's own files.
//
// WITHOUT ONE, they are what main pins between two releases (BP-D15, OQ-BP7 ruled 2026-10-05:
// main pins its own build). `check` compares the digests alone, so a url may still name any
// earlier release, and it is a dependency of `just check-ci`; `pin` (`just pin-pack-binaries`)
// writes each sha256 and keeps each url. `seed` is `just install`'s: it builds this machine's
// builds and admits each whose digest is the pin to the pack-binary cache every launch reads,
// so a from-source or forked tree's jail runs that tree's programs with no download, and with
// --repin it first re-pins, on this machine's platforms only, a program the tree has moved. The
// integration harness runs it
// without --repin, into its run's cache.
//
// Every refusal is reported, not just the first: a digest (naming the binary, the platform and
// both digests), a url that does not name <version>, a platform outside BP-D7's set or missing
// from it, a program that links the packs embed, and a `binaries` key with no cmd/<name>. With
// no official binary declared, each verb says so and succeeds without fetching the toolchain.
//
// This lives under tools/, not cmd/: cmd/* binaries are built into every jail's prefix, and this
// one only ever runs from a checkout.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--internal-toolchain-session" {
		os.Exit(runToolchainSession(os.Args[2:], os.Stdin, os.Stdout))
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pack-binaries:", err)
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultDeps(root, os.Environ())))
}

// toolchainSessionTestPacks is set only by the isolated compiled-test child. A nil value
// leaves the production embedded packs unchanged.
var toolchainSessionTestPacks fs.FS

// defaultDeps is the world a real run reads: builds are made with Toolchain, downloaded by the
// go on PATH, and never with that go itself; seed builds for this machine and fills the cache
// every launch on it reads.
func defaultDeps(root string, environ []string) deps {
	return deps{
		root:    root,
		environ: environ,
		toolchain: func() (string, error) {
			hostGo, err := exec.LookPath("go")
			if err != nil {
				return "", fmt.Errorf("the go command, which downloads %s: %w", Toolchain, err)
			}
			return fetchToolchain(hostGo, environ)
		},
		pathGo:   func() (string, error) { return exec.LookPath("go") },
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		cacheDir: paths.PackBinariesDir,
		packs:    packs.FS,
	}
}

// deps is what run reads from the world, so a test can hand it a fixture checkout and a
// toolchain that is not downloaded.
type deps struct {
	root    string
	environ []string
	// toolchain returns the go binary builds are made with. It is called only when there is
	// something to build.
	toolchain func() (string, error)
	// pathGo is the go on PATH, which seed alone may build with when toolchain fails and it
	// reports exactly Toolchain (BP-D26).
	pathGo func() (string, error)
	// goos and goarch are the machine seed builds for: a host reference's build for this pair,
	// a jail reference's for linux on goarch, which is what a launch here asks the cache for
	// (BP-D3).
	goos, goarch string
	cacheDir     func() string
	// packs is the packs tree whose loophole manifests the tool reads: in production the packs
	// embed, which `go run` compiles from the checkout it runs in.
	packs fs.FS
	// observation-only hooks are nil outside the private controlled session.
	beforeBuild  func(string) error
	planObserved func(planObservation) error
}

const usage = `usage: pack-binaries pin [<version>]
       pack-binaries check [<version>]
       pack-binaries stage <version> <dir>
       pack-binaries seed [--repin] [<dir>]
Run from the checkout's root. <version> is the release, X.Y.Z or X.Y.Z-pre, with or without a v.
With no version, pin writes each build's sha256 and keeps its url, and check compares the
digests alone: what main pins between releases. seed builds this machine's builds into the
pack-binary cache (<dir>, or the one yolo reads), re-pinning first with --repin.`

func run(args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	verb, rest := args[0], args[1:]
	version, dir, repin := "", "", false
	switch verb {
	case "pin", "check":
		if len(rest) > 1 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		if len(rest) == 1 {
			version = rest[0]
		}
	case "stage":
		if len(rest) != 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		version, dir = rest[0], rest[1]
	case "seed":
		for _, a := range rest {
			switch {
			case a == "--repin" && !repin:
				repin = true
			case a != "" && !strings.HasPrefix(a, "-") && dir == "":
				dir = a
			default:
				fmt.Fprintln(stderr, usage)
				return 2
			}
		}
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if version != "" || verb == "stage" {
		v, err := releasematrix.NormalizeVersion(version)
		if err != nil {
			fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
			return 2
		}
		version = v
	}
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(d.root, dir)
	}

	t := &task{d: d, verb: verb, version: version, stderr: stderr}
	if err := t.prepare(); err != nil {
		fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
		return 1
	}
	if verb == "stage" {
		if err := emptyDir(dir); err != nil {
			fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
			return 1
		}
	}
	if len(t.official) == 0 {
		if d.planObserved != nil {
			if err := d.planObserved(planObservation{Source: "no-official", WantCount: 0}); err != nil {
				fmt.Fprintln(stderr, "pack-binaries: controlled observation failed")
				return 1
			}
		}
		fmt.Fprintf(stdout, "pack-binaries: no official pack declares a binary (read %d loophole "+
			"manifests) — nothing to %s\n", t.read, verb)
		return 0
	}
	// pin writes values, never keys, so a build set it would refuse to write is refused before
	// the toolchain is fetched or anything is built (BP-D11).
	if verb == "pin" && t.refusePin() {
		return 1
	}

	tmp, err := os.MkdirTemp("", "pack-binaries-")
	if err != nil {
		fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	if verb == "seed" {
		if dir == "" {
			dir = d.cacheDir()
		}
		return t.seed(tmp, dir, repin, stdout)
	}
	if err := t.buildWanted(tmp, stdout); err != nil {
		fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
		return 1
	}

	switch verb {
	case "pin":
		return t.pin(stdout)
	case "check":
		t.compare()
		if t.report() {
			return 1
		}
		if version == "" {
			fmt.Fprintf(stdout, "pack-binaries: this tree builds every official build's pinned "+
				"sha256 (%d builds)\n", len(t.built))
		} else {
			fmt.Fprintf(stdout, "pack-binaries: every official build matches v%s (%d builds)\n",
				version, len(t.built))
		}
		return 0
	default: // stage
		t.compare()
		if t.report() {
			fmt.Fprintf(stderr, "pack-binaries: nothing was staged in %s\n", dir)
			return 1
		}
		return t.stage(dir, stdout, stderr)
	}
}

// task is one run of a verb over the checkout.
type task struct {
	d    deps
	verb string
	// version is the release a url must name, "" for what main pins between releases: the
	// digests alone.
	version string
	stderr  io.Writer

	read     int
	official []releasematrix.Entry
	release  []string
	problems []releasematrix.Problem
	// wants is every declared build BP-D7 wants of every buildable binary, as buildWanted
	// collected them.
	wants []want
	// built is each (binary, platform) built, by key.
	built map[string]build
	// goBin is the toolchain's go, once fetched.
	goBin string
	// unfetched is why the toolchain could not be fetched when seed is building with the go on
	// PATH instead (seedToolchain), and nil otherwise.
	unfetched error
}

// want is one declared build and what the manifest says of it.
type want struct {
	e                        releasematrix.Entry
	name, platform, url, sum string
}

// build is one official build as the recipe made it.
type build struct {
	name, platform, path, sha256 string
}

func buildKey(name, platform string) string { return name + " " + platform }

// pinCommand is the command a stale pin's refusal names: the release's pin for a version, and
// the digest-only pin between releases.
func (t *task) pinCommand() string {
	if t.version == "" {
		return "`just pin-pack-binaries`"
	}
	return "`just pin-pack-binaries " + t.version + "`"
}

// prepare reads the manifests and the release platforms and runs the census.
func (t *task) prepare() error {
	if err := checkRoot(t.d.root); err != nil {
		return err
	}
	// THE EMBED'S MANIFESTS, not every directory under packs/: what `go install` builds into
	// yolo from this same tree, uncommitted edits included, and nothing it would leave out — a
	// pack the embed does not list cannot stop an install (BP-D25). Pins are still written to
	// the tree's files, at the same paths.
	var all []releasematrix.Entry
	var err error
	if t.verb == "seed" {
		// The install's read skips, rather than refuses, a loophole directory it cannot read:
		// yolo loads only what a pack.json names, tolerantly, and the gates refuse it anyway.
		var unread []error
		all, unread, err = releasematrix.ManifestsTolerant(t.d.packs)
		for _, u := range unread {
			fmt.Fprintf(t.stderr, "pack-binaries: skipped %v — nothing in it is seeded; "+
				"`just check-ci` refuses it until it is a manifest or gone\n", u)
		}
	} else {
		all, err = releasematrix.Manifests(t.d.packs)
	}
	if err != nil {
		return err
	}
	t.read, t.official = len(all), releasematrix.WithBinaries(all)
	if len(t.official) == 0 {
		return nil
	}
	cfg, err := os.ReadFile(filepath.Join(t.d.root, releasematrix.GoreleaserConfig))
	if err != nil {
		return err
	}
	if t.release, err = releasematrix.ReleasePlatforms(cfg); err != nil {
		return err
	}
	t.problems = releasematrix.Census(t.d.root, t.official, t.release)
	return nil
}

// checkRoot refuses a directory that is not this module's checkout, where every path the tool
// reads and writes would mean something else.
func checkRoot(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "module "+releasematrix.ModulePath {
				return nil
			}
		}
	}
	return fmt.Errorf("%s is not the root of a %s checkout — run this from there", root,
		releasematrix.ModulePath)
}

// programProblem is the census's refusal of a binary's program, when there is one: the program
// cannot be built at all.
func (t *task) programProblem(e releasematrix.Entry, name string) (releasematrix.Problem, bool) {
	for _, p := range t.problems {
		if p.Kind == releasematrix.KindProgram && p.Manifest == e.Path && p.Binary == name {
			return p, true
		}
	}
	return releasematrix.Problem{}, false
}

// collectWants is every declared build BP-D7 wants of every buildable binary. A declared build
// BP-D7 does not want is the census's to report, and is not built: the program may not build
// there at all.
func (t *task) collectWants() []want {
	var wants []want
	for _, e := range t.official {
		for _, b := range e.Manifest.Binaries {
			if _, bad := t.programProblem(e, b.Name); bad {
				continue
			}
			wanted := map[string]bool{}
			for _, p := range releasematrix.Platforms(e.Manifest, b.Name, t.release) {
				wanted[p] = true
			}
			for _, bb := range b.Builds {
				if wanted[bb.Platform] {
					wants = append(wants, want{e, b.Name, bb.Platform, bb.URL, bb.SHA256})
				}
			}
		}
	}
	return wants
}

// buildWanted builds, once each, every build collectWants names.
func (t *task) buildWanted(tmp string, stdout io.Writer) error {
	t.wants = t.collectWants()
	if t.d.planObserved != nil {
		if err := t.d.planObserved(planObservation{Source: "build-wanted", WantCount: len(t.wants)}); err != nil {
			return fmt.Errorf("controlled acquisition plan: %w", err)
		}
	}
	for _, w := range t.wants {
		if _, err := t.buildOnce(tmp, stdout, w.name, w.platform); err != nil {
			return err
		}
	}
	return nil
}

// buildOnce builds cmd/<name> for platform with the recipe, unless this run already has, and
// fetches the toolchain the first time anything is built.
func (t *task) buildOnce(tmp string, stdout io.Writer, name, platform string) (build, error) {
	if t.built == nil {
		t.built = map[string]build{}
	}
	k := buildKey(name, platform)
	if b, done := t.built[k]; done {
		return b, nil
	}
	if t.goBin == "" {
		goBin, err := t.d.toolchain()
		if err != nil {
			return build{}, fmt.Errorf("the pinned toolchain %s: %w", Toolchain, err)
		}
		fmt.Fprintf(stdout, "pack-binaries: building with %s (%s)\n", Toolchain, goBin)
		t.goBin = goBin
	}
	if t.d.beforeBuild != nil {
		if err := t.d.beforeBuild(t.goBin); err != nil {
			return build{}, fmt.Errorf("controlled build barrier: %w", err)
		}
	}
	path, sum, err := buildOne(t.goBin, t.d.root, t.d.environ, name, platform, tmp)
	if err != nil {
		return build{}, err
	}
	b := build{name: name, platform: platform, path: path, sha256: sum}
	t.built[k] = b
	return b, nil
}

// compare records, for check and stage, each wanted build the manifest disagrees with: a url
// naming another release, when a version was given, and a digest the tree does not build.
func (t *task) compare() {
	for _, w := range t.wants {
		b := t.built[buildKey(w.name, w.platform)]
		if t.version != "" {
			if _, v, _, ok := releasematrix.ParseAssetURL(w.url); ok && v != t.version {
				t.problems = append(t.problems, releasematrix.Problem{Kind: releasematrix.KindURL,
					Manifest: w.e.Path, Binary: w.name, Platform: w.platform,
					Text: "url names v" + v + "'s release, and this is v" + t.version + "'s — run " +
						t.pinCommand() + " and commit the result"})
			}
		}
		if b.sha256 != w.sum {
			t.problems = append(t.problems, t.digestProblem(w, b))
		}
	}
}

// digestProblem is a pin the tree no longer reproduces, naming both digests and the command that
// re-pins it.
func (t *task) digestProblem(w want, b build) releasematrix.Problem {
	return releasematrix.Problem{Kind: releasematrix.KindDigest, Manifest: w.e.Path,
		Binary: w.name, Platform: w.platform,
		Text: "this tree builds sha256 " + b.sha256 + ", and the manifest pins " + w.sum +
			" — run " + t.pinCommand() + " and commit the result"}
}

// report prints every problem and says whether there was one.
func (t *task) report() bool {
	if len(t.problems) == 0 {
		return false
	}
	printProblems(t.stderr, t.problems)
	release := "for v" + t.version
	if t.version == "" {
		release = "against this tree"
	}
	fmt.Fprintf(t.stderr, "pack-binaries: %d problem(s) with the official pack binaries %s\n",
		len(t.problems), release)
	return true
}

func printProblems(w io.Writer, problems []releasematrix.Problem) {
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].String() < problems[j].String() })
	for _, p := range problems {
		fmt.Fprintln(w, "✗", p)
	}
}

// refusePin reports the census problems pin cannot write its way out of — a build set that is
// not BP-D7's, a program that cannot be built, and, for the digest-only pin, which keeps each
// url, a url of the wrong form — and says whether there was one. With a version a url of the
// wrong form is not among them: pin rewrites every url.
func (t *task) refusePin() bool {
	var blocking []releasematrix.Problem
	for _, p := range t.problems {
		if p.Kind != releasematrix.KindURL || t.version == "" {
			blocking = append(blocking, p)
		}
	}
	if len(blocking) == 0 {
		return false
	}
	printProblems(t.stderr, blocking)
	fmt.Fprintf(t.stderr, "pack-binaries: %d problem(s) with the official pack binaries\n", len(blocking))
	fmt.Fprintln(t.stderr, "pack-binaries: nothing was pinned")
	return true
}

// pin writes every build's sha256, and with a version its url. run has already refused a build
// set pin cannot write (refusePin), so every declared build was built.
func (t *task) pin(stdout io.Writer) int {
	what := "for v" + t.version
	if t.version == "" {
		what = "to this tree's builds (each url kept)"
	}
	for _, e := range t.official {
		var pins []buildPin
		for _, b := range e.Manifest.Binaries {
			for _, bb := range b.Builds {
				built := t.built[buildKey(b.Name, bb.Platform)]
				p := buildPin{Binary: b.Name, Platform: bb.Platform, SHA256: built.sha256}
				if t.version != "" {
					p.URL = releasematrix.AssetURL(b.Name, t.version, bb.Platform)
				}
				pins = append(pins, p)
				was := ""
				if bb.SHA256 != built.sha256 {
					was = " (was " + bb.SHA256 + ")"
				}
				fmt.Fprintf(stdout, "%s: %s %s sha256 %s%s\n", e.Path, b.Name, bb.Platform,
					built.sha256, was)
			}
		}
		changed, err := writePins(t.d.root, e.Path, pins)
		if err != nil {
			fmt.Fprintf(t.stderr, "pack-binaries: %v\n", err)
			return 1
		}
		if changed {
			fmt.Fprintf(stdout, "%s: pinned %s\n", e.Path, what)
		} else {
			fmt.Fprintf(stdout, "%s: already pinned %s\n", e.Path, what)
		}
	}
	return 0
}

// stage writes each verified build into dir under its release file name (BP-D8).
func (t *task) stage(dir string, stdout, stderr io.Writer) int {
	keys := make([]string, 0, len(t.built))
	for k := range t.built {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := t.built[k]
		dst := filepath.Join(dir, releasematrix.AssetName(b.name, t.version, b.platform))
		if err := copyExecutable(b.path, dst); err != nil {
			fmt.Fprintf(stderr, "pack-binaries: staging %s: %v\n", dst, err)
			return 1
		}
		fmt.Fprintf(stdout, "staged %s (sha256 %s)\n", dst, b.sha256)
	}
	return 0
}

// machineNeeds is every build a launch on this machine asks the cache for (BP-D3): for each
// official loophole whose `platforms` admits the machine, each reference's declared build for
// where it runs, once per manifest. What it cannot seed and why goes to stdout, and a program the
// census refuses comes back as a problem: it cannot be built at all.
func (t *task) machineNeeds(stdout io.Writer) ([]want, []releasematrix.Problem) {
	goos, goarch := t.d.goos, t.d.goarch
	var out []want
	var problems []releasematrix.Problem
	seen := map[string]bool{}
	for _, e := range t.official {
		needs := e.Manifest.BinariesNeeded(goos, goarch)
		if len(needs) == 0 {
			continue
		}
		if !e.Manifest.SupportsPlatform(goos, goarch) {
			fmt.Fprintf(stdout, "%s: the loophole does not run on %s/%s (its `platforms`), so "+
				"none of its binaries is seeded\n", e.Path, goos, goarch)
			continue
		}
		for _, n := range needs {
			k := e.Path + " " + buildKey(n.Binary, n.Platform)
			if seen[k] {
				continue
			}
			seen[k] = true
			if !n.HasBuild {
				fmt.Fprintf(stdout, "%s: binary %s has no build for %s, where it runs %s, so "+
					"there is nothing to seed for it\n", e.Path, n.Binary, n.Platform, n.Where())
				continue
			}
			if p, bad := t.programProblem(e, n.Binary); bad {
				problems = append(problems, p)
				continue
			}
			out = append(out, want{e, n.Binary, n.Platform, n.Build.URL, n.Build.SHA256})
		}
	}
	return out, problems
}

// repinStale writes, for each stale build, the digest this machine just built for it — and
// NOTHING ELSE (BP-D30): not another platform's digest, which this machine did not build, and
// not a url. A Mac whose native build did not reproduce a Linux-made pin rewrites only its own
// two, and the landing gate refuses the rest until `just pin-pack-binaries` re-pins every
// platform. It returns 0, or 1 having said why.
func (t *task) repinStale(stale []want, stdout io.Writer) int {
	var order []string
	byPath := map[string][]buildPin{}
	for _, n := range stale {
		if _, ok := byPath[n.e.Path]; !ok {
			order = append(order, n.e.Path)
		}
		byPath[n.e.Path] = append(byPath[n.e.Path], buildPin{Binary: n.name, Platform: n.platform,
			SHA256: t.built[buildKey(n.name, n.platform)].sha256})
	}
	for _, path := range order {
		if _, err := writePins(t.d.root, path, byPath[path]); err != nil {
			fmt.Fprintf(t.stderr, "pack-binaries: %v\n", err)
			return 1
		}
	}
	repinned := map[string]bool{}
	for _, n := range stale {
		repinned[n.e.Path+" "+buildKey(n.name, n.platform)] = true
		// The commit moves HEAD through packs/, which version.SourceSkew compares this
		// install's stamp against, so the step after it is another install (BP-D28).
		fmt.Fprintf(stdout, "%s: re-pinned binary %s (%s) to this machine's build, sha256 %s (was "+
			"%s): this tree's program has moved since it was pinned — commit the manifest "+
			"(`just check-ci` refuses the old pin), then re-run `just install`, since a launch "+
			"building from the checkout (YOLO_REPO_ROOT) refuses a yolo stamped before that "+
			"commit\n", n.e.Path, n.name, n.platform, t.built[buildKey(n.name, n.platform)].sha256,
			n.sum)
	}
	told := map[string]bool{}
	for _, n := range stale {
		if told[n.e.Path+" "+n.name] {
			continue
		}
		told[n.e.Path+" "+n.name] = true
		var others []string
		for _, b := range n.e.Manifest.Binaries {
			if b.Name != n.name {
				continue
			}
			for _, p := range b.BuildPlatforms() {
				if !repinned[n.e.Path+" "+buildKey(n.name, p)] {
					others = append(others, p)
				}
			}
		}
		if len(others) > 0 {
			fmt.Fprintf(stdout, "%s: binary %s still pins the build before the change for %s, which "+
				"this machine did not build — run `just pin-pack-binaries` before landing it, since "+
				"`just check-ci` refuses those pins\n", n.e.Path, n.name, strings.Join(others, ", "))
		}
	}
	return 0
}

// seedToolchain is the go seed builds with: the pinned toolchain, fetched or already in the
// module cache, or — when it cannot be had, offline or after a Toolchain bump — the go on PATH,
// if that reports exactly Toolchain (BP-D26). Falling back cannot admit a wrong build, because
// packbin.Seed admits only bytes whose digest is the pin, and §14.2 measured a go1.26.7 not
// fetched as the module building the same bytes; what it does lose is the right to re-pin, which
// seed refuses then (t.unfetched).
func (t *task) seedToolchain(stdout io.Writer) error {
	goBin, err := t.d.toolchain()
	if err == nil {
		fmt.Fprintf(stdout, "pack-binaries: building with %s (%s)\n", Toolchain, goBin)
		t.goBin = goBin
		return nil
	}
	pathGo, perr := t.d.pathGo()
	if perr == nil {
		perr = checkToolchainVersion(pathGo, t.d.environ)
	}
	if perr != nil {
		return fmt.Errorf("the pinned toolchain %s could not be fetched (%v), and the go on PATH "+
			"cannot stand in for it (%v) — reach the module proxy once, after which the toolchain "+
			"is cached, or put %s on PATH", Toolchain, err, perr, Toolchain)
	}
	fmt.Fprintf(stdout, "pack-binaries: the pinned toolchain %s could not be fetched (%v); "+
		"building with the go on PATH (%s), which reports %s, to seed only — a build is admitted "+
		"only where its digest is the pin\n", Toolchain, err, pathGo, Toolchain)
	t.goBin, t.unfetched = pathGo, err
	return nil
}

// seed is the seed verb: this machine's builds of every official binary, made with the recipe,
// each admitted to the cache at dir through packbin's verified rename when its digest is the
// pin (BP-D15). Nothing is downloaded but the pinned toolchain, once, into the module cache.
//
// A build whose digest is not the pin is a program this tree has changed since it was pinned.
// Without repin it is not seeded, and the run fails naming the pin command, the integration
// harness's case; with repin, `just install`'s, the tree is re-pinned first — the sha256 of
// each build this machine made, and nothing else (repinStale, BP-D30) — so the manifest the
// install then embeds pins the build it seeds, and the fork that changed a program runs it.
func (t *task) seed(tmp, dir string, repin bool, stdout io.Writer) int {
	needs, problems := t.machineNeeds(stdout)
	if len(needs) == 0 && len(problems) == 0 {
		fmt.Fprintf(stdout, "pack-binaries: no official binary runs on %s/%s — nothing to seed\n",
			t.d.goos, t.d.goarch)
		return 0
	}
	if len(needs) > 0 {
		if err := t.seedToolchain(stdout); err != nil {
			fmt.Fprintf(t.stderr, "pack-binaries: %v\npack-binaries: nothing was seeded\n", err)
			return 1
		}
	}
	for _, n := range needs {
		if _, err := t.buildOnce(tmp, stdout, n.name, n.platform); err != nil {
			fmt.Fprintf(t.stderr, "pack-binaries: %v\npack-binaries: nothing was seeded\n", err)
			return 1
		}
	}
	var stale []want
	for _, n := range needs {
		if t.built[buildKey(n.name, n.platform)].sha256 != n.sum {
			stale = append(stale, n)
		}
	}
	if len(stale) > 0 && repin && t.unfetched != nil {
		// A re-pin writes the digests these builds have, and the release builds with the fetched
		// toolchain: from the PATH's go, the toolchain may be all that moved.
		fmt.Fprintf(t.stderr, "pack-binaries: not re-pinned: %s could not be fetched (%v), and "+
			"a digest the go on PATH made is not one the release is known to reproduce — re-run "+
			"`just install` where the module proxy can be reached; the toolchain is cached after "+
			"that once\n", Toolchain, t.unfetched)
		repin = false
	}
	if len(stale) > 0 && repin {
		if rc := t.repinStale(stale, stdout); rc != 0 {
			fmt.Fprintln(t.stderr, "pack-binaries: nothing was seeded")
			return rc
		}
		for i := range needs {
			needs[i].sum = t.built[buildKey(needs[i].name, needs[i].platform)].sha256
		}
		stale = nil
	}

	rc := 0
	seeded := 0
	for _, n := range needs {
		b := t.built[buildKey(n.name, n.platform)]
		if b.sha256 != n.sum {
			problems = append(problems, t.digestProblem(n, b))
			continue
		}
		path, outcome, err := packbin.Seed(dir, n.name, n.sum, b.path)
		if err != nil {
			fmt.Fprintf(t.stderr, "pack-binaries: %s: binary %s (%s): %v\n", n.e.Path, n.name,
				n.platform, err)
			rc = 1
			continue
		}
		seeded++
		switch outcome {
		case packbin.Cached:
			fmt.Fprintf(stdout, "%s: binary %s (%s) is already in the cache, sha256 %s\n",
				n.e.Path, n.name, n.platform, n.sum)
		default:
			fmt.Fprintf(stdout, "%s: seeded binary %s (%s), sha256 %s, from this tree: %s\n",
				n.e.Path, n.name, n.platform, n.sum, path)
		}
	}
	if len(problems) > 0 {
		printProblems(t.stderr, problems)
		fmt.Fprintf(t.stderr, "pack-binaries: %d build(s) this machine runs were not seeded, so "+
			"their loopholes stay off here until they are\n", len(problems))
		rc = 1
	}
	fmt.Fprintf(stdout, "pack-binaries: %d build(s) for %s/%s from this tree are in %s\n", seeded,
		t.d.goos, t.d.goarch, dir)
	return rc
}

// emptyDir makes dir, refusing one that already holds anything: every file in it is uploaded
// to the release, so a leftover would be published as if this run had built it.
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s is not empty, and everything in it would be uploaded to the "+
			"release — remove what is there first", dir)
	}
	return nil
}

func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

const (
	toolchainProtocolVersion  = 1
	maxProtocolFrame          = 2 << 20
	maxControlFrame           = 4 << 10
	maxHelloFrame             = 256 << 10
	maxContextEnvironment     = 128 << 10
	maxSessionMillis          = 30 * 60 * 1000
	initialFrameWait          = 30 * time.Second
	producerCleanupWait       = 3 * time.Second
	controlWriterJoinWait     = 250 * time.Millisecond
	effectiveProxyOutputLimit = 64 << 10
	downloadOutputLimit       = 1 << 20
	versionOutputLimit        = 4 << 10
	commandStderrLimit        = 16 << 10
)

var errObservedOutputOverflow = errors.New("controlled command output exceeded its bound")
var errProducerCleanupUnavailable = errors.New("controlled producer cleanup did not complete")
var errControlWriterCleanupUnavailable = errors.New("controlled frame writer did not stop")

type protocolFrame struct {
	Protocol int             `json:"protocol"`
	Nonce    string          `json:"nonce"`
	Sequence uint64          `json:"sequence"`
	Kind     string          `json:"kind"`
	Payload  json.RawMessage `json:"payload"`
}

type initPayload struct {
	DeadlineMillis int64  `json:"deadline_millis"`
	Verb           string `json:"verb"`
}

type emptyPayload struct{}

type donePayload struct {
	ExitStatus int `json:"exit_status"`
}

type errorPayload struct {
	Stage       string                `json:"stage"`
	Status      string                `json:"status"`
	Code        string                `json:"code"`
	Observation *toolchainObservation `json:"observation,omitempty"`
}

type planObservation struct {
	Source    string `json:"source"`
	WantCount int    `json:"want_count"`
}

type selectedValue struct {
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
}

type selectorBinding struct {
	Path       string `json:"path"`
	Resolved   string `json:"resolved,omitempty"`
	Status     string `json:"status"`
	Mode       uint32 `json:"mode,omitempty"`
	Size       int64  `json:"size,omitempty"`
	ModTimeNS  int64  `json:"mod_time_ns,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`
}

type commandObservation struct {
	Stage          string                   `json:"stage"`
	Path           string                   `json:"path"`
	Args           []string                 `json:"args"`
	Selector       string                   `json:"selector"`
	Before         selectorBinding          `json:"before"`
	After          selectorBinding          `json:"after"`
	CWD            string                   `json:"cwd"`
	Environment    map[string]selectedValue `json:"environment"`
	Started        bool                     `json:"started"`
	PID            int                      `json:"pid,omitempty"`
	Status         string                   `json:"status"`
	ExitCode       *int                     `json:"exit_code,omitempty"`
	Stdout         []byte                   `json:"stdout"`
	Stderr         []byte                   `json:"stderr"`
	StdoutOverflow bool                     `json:"stdout_overflow"`
	StderrOverflow bool                     `json:"stderr_overflow"`
}

type readinessObservation struct {
	Directory string `json:"directory"`
	GoBinary  string `json:"go_binary"`
	Status    string `json:"status"`
}

type proxyObservation struct {
	Proxy    string             `json:"proxy,omitempty"`
	SumDB    string             `json:"sumdb,omitempty"`
	Fallback string             `json:"fallback"`
	Command  commandObservation `json:"command"`
}

type toolchainObservation struct {
	Protocol        int                      `json:"protocol"`
	HostOS          string                   `json:"host_os"`
	HostArch        string                   `json:"host_arch"`
	Downloader      string                   `json:"downloader"`
	Toolchain       string                   `json:"toolchain"`
	Module          string                   `json:"module"`
	Proxy           proxyObservation         `json:"proxy"`
	DownloadContext map[string]selectedValue `json:"download_context"`
	VersionContext  map[string]selectedValue `json:"version_context"`
	Commands        []commandObservation     `json:"commands"`
	ChosenDir       string                   `json:"chosen_dir,omitempty"`
	ModuleSum       string                   `json:"module_sum,omitempty"`
	Readiness       readinessObservation     `json:"readiness"`
	VersionOutput   []byte                   `json:"version_output,omitempty"`
	ReturnedGo      string                   `json:"returned_go,omitempty"`
}

type helloPayload struct {
	Source                      string                   `json:"source"`
	HostOS                      string                   `json:"host_os"`
	HostArch                    string                   `json:"host_arch"`
	CWD                         string                   `json:"cwd"`
	Executable                  string                   `json:"executable"`
	ProvisionalDownloader       string                   `json:"provisional_downloader"`
	ProvisionalDownloaderStatus string                   `json:"provisional_downloader_status"`
	DownloaderBinding           selectorBinding          `json:"downloader_binding"`
	Environment                 map[string]selectedValue `json:"environment"`
}

type toolchainSession struct {
	input              io.ReadCloser
	output             io.Writer
	nonce              string
	readSeq            uint64
	writeSeq           uint64
	ctx                context.Context
	cancel             context.CancelFunc
	stateMu            sync.Mutex
	writeMu            sync.Mutex
	planSeen           bool
	wantCount          int
	resultSent         bool
	released           bool
	releasedGo         string
	errorSent          bool
	cleanupUnavailable bool
}

func runToolchainSession(args []string, input io.ReadCloser, output io.Writer) int {
	if len(args) != 1 || args[0] != "check" {
		return 2
	}
	s := &toolchainSession{input: input, output: output}
	var init initPayload
	if err := s.readExpected("INIT", initialFrameWait, &init); err != nil || init.Verb != "check" || init.DeadlineMillis <= 0 || init.DeadlineMillis > maxSessionMillis {
		return 1
	}
	baseCtx, cancel := context.WithTimeout(context.Background(), time.Duration(init.DeadlineMillis)*time.Millisecond)
	s.ctx, s.cancel = baseCtx, cancel
	defer cancel()
	if err := s.sendHello(); err != nil {
		return 1
	}
	if err := s.readExpected("START", 0, &emptyPayload{}); err != nil {
		s.fail("start", toolchainObservation{}, "invalid_control")
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		s.fail("check", toolchainObservation{}, "cwd_unavailable")
		return 1
	}
	environ := os.Environ()
	d := defaultDeps(root, environ)
	if toolchainSessionTestPacks != nil {
		d.packs = toolchainSessionTestPacks
	}
	shared := newSharedToolchain(s.ctx, func(ctx context.Context) (string, error) {
		hostGo, err := exec.LookPath("go")
		if err != nil {
			return "", err
		}
		return fetchToolchainObserved(ctx, hostGo, environ, s)
	})
	shared.cancel = cancel
	shared.session = s
	d.toolchain = func() (string, error) { return shared.get() }
	d.beforeBuild = s.beforeBuild
	d.planObserved = s.planObserved
	status := run(args, io.Discard, io.Discard, d)
	status = s.finishStatus(status)
	if s.isCleanupUnavailable() || s.contextErr() != nil {
		return 1
	}
	if err := s.writeFrame("DONE", donePayload{ExitStatus: status}, maxControlFrame); err != nil {
		return 1
	}
	if err := s.readExpected("FINISH", 0, &emptyPayload{}); err != nil {
		return 1
	}
	if s.contextErr() != nil {
		return 1
	}
	return status
}

func (s *toolchainSession) contextErr() error {
	if s.ctx == nil {
		return nil
	}
	return s.ctx.Err()
}

func (s *toolchainSession) isCleanupUnavailable() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.cleanupUnavailable
}

func (s *toolchainSession) markCleanupUnavailable() {
	s.stateMu.Lock()
	s.cleanupUnavailable = true
	s.stateMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.input != nil {
		_ = s.input.Close()
	}
	if closer, ok := s.output.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (s *toolchainSession) finishStatus(status int) int {
	if s.isCleanupUnavailable() {
		return 1
	}
	if err := s.contextErr(); err != nil {
		s.fail("check", toolchainObservation{}, "session_canceled")
		return 1
	}
	s.stateMu.Lock()
	missing := status == 0 && (!s.planSeen || !s.resultSent || (s.wantCount > 0 && !s.released))
	needsFailure := status != 0 && !s.errorSent
	s.stateMu.Unlock()
	if missing {
		s.fail("check", toolchainObservation{}, "required_observation_missing")
		return 1
	}
	if needsFailure {
		s.fail("check", toolchainObservation{}, "check_failed")
	}
	if s.isCleanupUnavailable() || s.contextErr() != nil {
		return 1
	}
	return status
}

func (s *toolchainSession) sendHello() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	source := ""
	if _, file, _, ok := runtime.Caller(0); ok {
		source = file
	}
	downloader, lookErr := exec.LookPath("go")
	downloaderStatus := "resolved"
	if lookErr != nil {
		downloader, downloaderStatus = "", "unavailable"
	}
	if !utf8.ValidString(cwd) || !utf8.ValidString(executable) || !utf8.ValidString(source) || !utf8.ValidString(downloader) {
		return errors.New("runtime context cannot be encoded as UTF-8")
	}
	env, err := selectedEnvironment(os.Environ())
	if err != nil {
		return err
	}
	return s.writeFrame("HELLO", helloPayload{
		Source: source, HostOS: runtime.GOOS, HostArch: runtime.GOARCH, CWD: cwd,
		Executable: executable, ProvisionalDownloader: downloader, ProvisionalDownloaderStatus: downloaderStatus,
		DownloaderBinding: bindSelector(downloader), Environment: env,
	}, maxHelloFrame)
}

func selectedEnvironment(environ []string) (map[string]selectedValue, error) {
	keys := make(map[string]bool, len(observedEnvironmentKeys))
	budget := 1
	for _, key := range observedEnvironmentKeys {
		keys[key] = true
		cost, ok := selectedEnvEntryCost(key, "", false)
		if !ok || budget+cost+1 > maxContextEnvironment {
			return nil, errors.New("selected environment exceeds controlled session bound")
		}
		budget += cost + 1
	}
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if !dynamicNixKey(key) || keys[key] {
			continue
		}
		if !utf8.ValidString(key) {
			return nil, errors.New("selected environment is not UTF-8")
		}
		cost, ok := selectedEnvEntryCost(key, "", false)
		if !ok || budget+cost+1 > maxContextEnvironment {
			return nil, errors.New("selected environment exceeds controlled session bound")
		}
		keys[key] = true
		budget += cost + 1
	}
	out := make(map[string]selectedValue, len(keys))
	costs := make(map[string]int, len(keys))
	for key := range keys {
		cost, _ := selectedEnvEntryCost(key, "", false)
		out[key] = selectedValue{}
		costs[key] = cost
	}
	for _, kv := range environ {
		key, value, found := strings.Cut(kv, "=")
		if !found || !keys[key] {
			continue
		}
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return nil, errors.New("selected environment is not UTF-8")
		}
		cost, ok := selectedEnvEntryCost(key, value, true)
		if !ok || budget-costs[key]+cost > maxContextEnvironment {
			return nil, errors.New("selected environment exceeds controlled session bound")
		}
		budget += cost - costs[key]
		costs[key] = cost
		out[key] = selectedValue{Present: true, Value: value}
	}
	if err := checkContextEnvironmentSize(out); err != nil {
		return nil, err
	}
	return out, nil
}

func selectedEnvEntryCost(key, value string, present bool) (int, bool) {
	keySize, ok := jsonQuotedSize(key, maxContextEnvironment)
	if !ok {
		return 0, false
	}
	objectSize := len(`{"present":false}`)
	if present {
		objectSize = len(`{"present":true}`)
		if value != "" {
			valueSize, ok := jsonQuotedSize(value, maxContextEnvironment)
			if !ok {
				return 0, false
			}
			objectSize = len(`{"present":true,"value":`) + valueSize + 1
		}
	}
	cost := keySize + 1 + objectSize
	return cost, cost <= maxContextEnvironment
}

func jsonQuotedSize(value string, limit int) (int, bool) {
	size := 2
	for i := 0; i < len(value); {
		b := value[i]
		add := 0
		switch {
		case b == '"' || b == '\\':
			add = 2
		case b < 0x20:
			switch b {
			case '\b', '\f', '\n', '\r', '\t':
				add = 2
			default:
				add = 6
			}
		case b == '<' || b == '>' || b == '&':
			add = 6
		case b < utf8.RuneSelf:
			add = 1
		default:
			r, n := utf8.DecodeRuneInString(value[i:])
			if r == utf8.RuneError && n == 1 {
				add = 3
			} else if r == '\u2028' || r == '\u2029' {
				add = 6
			} else {
				add = n
			}
			i += n
		}
		if size+add > limit {
			return 0, false
		}
		size += add
		if b < utf8.RuneSelf {
			i++
		}
	}
	return size, size <= limit
}

var observedEnvironmentKeys = []string{
	"GOENV", "GOROOT", "GOTOOLDIR", "GOVERSION", "GOTOOLCHAIN", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH",
	"GOMOD", "GOWORK", "GOCACHE", "GOMODCACHE", "GOPATH", "GOFLAGS", "GOEXPERIMENT", "CGO_ENABLED",
	"CC", "CXX", "GOAMD64", "GOTMPDIR", "GOCACHEPROG", "GOPROXY", "GOSUMDB", "GOPRIVATE", "GONOPROXY",
	"GONOSUMDB", "GOINSECURE", "GOAUTH", "GOFIPS140", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS",
	"CGO_FFLAGS", "CGO_LDFLAGS", "HOME", "PATH", "PWD", "XDG_CONFIG_HOME", "LD_LIBRARY_PATH", "LD_PRELOAD",
	"GCC_EXEC_PREFIX", "COMPILER_PATH", "LIBRARY_PATH", "CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH",
	"PKG_CONFIG", "NPM_CONFIG_PREFIX", "YOLO_TEST_PI_PACKAGE", "YOLO_TEST_PI_SUBAGENTS",
}

func dynamicNixKey(key string) bool {
	for _, prefix := range []string{"NIX_CFLAGS", "NIX_LDFLAGS", "NIX_CC_WRAPPER", "NIX_BINTOOLS_WRAPPER", "NIX_DYNAMIC", "NIX_HARDENING"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func checkContextEnvironmentSize(env map[string]selectedValue) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(data) > maxContextEnvironment {
		return fmt.Errorf("selected environment exceeds controlled session bound")
	}
	return nil
}

func (s *toolchainSession) planObserved(plan planObservation) error {
	if plan.WantCount < 0 || (plan.Source != "no-official" && plan.Source != "build-wanted") {
		return errors.New("invalid observed plan")
	}
	s.stateMu.Lock()
	if s.planSeen {
		s.stateMu.Unlock()
		return errors.New("duplicate observed plan")
	}
	s.planSeen, s.wantCount = true, plan.WantCount
	if plan.WantCount != 0 {
		s.stateMu.Unlock()
		return nil
	}
	if s.resultSent {
		s.stateMu.Unlock()
		return errors.New("duplicate result")
	}
	s.resultSent = true
	s.stateMu.Unlock()
	return s.writeFrame("RETURN", struct {
		Outcome string          `json:"outcome"`
		Plan    planObservation `json:"plan"`
	}{Outcome: "no_acquisition", Plan: plan}, maxControlFrame)
}

func (s *toolchainSession) beforeBuild(goBin string) error {
	if err := s.contextErr(); err != nil {
		return err
	}
	s.stateMu.Lock()
	released, releasedGo := s.released, s.releasedGo
	s.stateMu.Unlock()
	if !released || goBin == "" || goBin != releasedGo {
		return errors.New("build attempted without the released chosen toolchain")
	}
	return s.contextErr()
}

func (s *toolchainSession) returnAndWait(ctx context.Context, observation toolchainObservation) error {
	s.stateMu.Lock()
	if s.resultSent || s.wantCount <= 0 || !s.planSeen {
		s.stateMu.Unlock()
		return errors.New("unexpected provider return")
	}
	s.resultSent = true
	s.stateMu.Unlock()
	if err := s.writeFrame("RETURN", struct {
		Outcome     string               `json:"outcome"`
		Observation toolchainObservation `json:"observation"`
	}{Outcome: "provider_return", Observation: observation}, maxProtocolFrame); err != nil {
		return err
	}
	if err := s.readExpected("RELEASE", 0, &emptyPayload{}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.contextErr(); err != nil {
		return err
	}
	s.stateMu.Lock()
	if err := s.contextErr(); err != nil {
		s.stateMu.Unlock()
		return err
	}
	s.released, s.releasedGo = true, observation.ReturnedGo
	s.stateMu.Unlock()
	return nil
}

func (s *toolchainSession) fail(stage string, observation toolchainObservation, code string) {
	s.stateMu.Lock()
	if s.errorSent {
		s.stateMu.Unlock()
		return
	}
	s.errorSent = true
	s.stateMu.Unlock()
	payload := errorPayload{Stage: stage, Status: "failed", Code: code, Observation: &observation}
	_ = s.writeFrame("ERROR", payload, maxProtocolFrame)
}

func (s *toolchainSession) writeFrame(kind string, payload any, limit int) error {
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.stateMu.Lock()
	unavailable := s.cleanupUnavailable
	s.stateMu.Unlock()
	if unavailable {
		return errProducerCleanupUnavailable
	}
	sequence := s.writeSeq + 1
	frame, err := json.Marshal(protocolFrame{Protocol: toolchainProtocolVersion, Nonce: s.nonce, Sequence: sequence, Kind: kind, Payload: p})
	if err != nil {
		return err
	}
	if len(frame) > limit || len(frame) > maxProtocolFrame {
		return fmt.Errorf("frame exceeds bound")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	writeDone := make(chan error, 1)
	go func() {
		if err := writeAll(s.output, header[:]); err != nil {
			writeDone <- err
			return
		}
		writeDone <- writeAll(s.output, frame)
	}()
	writeCtx := s.ctx
	if writeCtx == nil {
		writeCtx = context.Background()
	}
	wait := 30 * time.Second
	if deadline, ok := writeCtx.Deadline(); ok && time.Until(deadline) < wait {
		wait = time.Until(deadline)
	}
	if wait <= 0 {
		wait = time.Nanosecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-writeDone:
		if err != nil {
			s.markCleanupUnavailable()
			return err
		}
		s.writeSeq = sequence
		return nil
	case <-ctxDone(writeCtx):
		s.markCleanupUnavailable()
		return s.joinFrameWriter(writeDone, writeCtx.Err())
	case <-timer.C:
		s.markCleanupUnavailable()
		return s.joinFrameWriter(writeDone, context.DeadlineExceeded)
	}
}

func (s *toolchainSession) joinFrameWriter(done <-chan error, result error) error {
	timer := time.NewTimer(controlWriterJoinWait)
	defer timer.Stop()
	select {
	case <-done:
		return result
	case <-timer.C:
		return errControlWriterCleanupUnavailable
	}
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (s *toolchainSession) readExpected(kind string, timeout time.Duration, payload any) error {
	frame, err := readFrameWithTimeout(s.input, timeout, s.ctx)
	if err != nil {
		if s.cancel != nil {
			s.cancel()
		}
		return err
	}
	if err := rejectDuplicateJSON(frame); err != nil {
		return err
	}
	if !utf8.Valid(frame) {
		return errors.New("protocol frame is not utf-8")
	}
	var f protocolFrame
	dec := json.NewDecoder(bytes.NewReader(frame))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return err
	}
	if f.Protocol != toolchainProtocolVersion || !validSessionNonce(f.Nonce) || f.Sequence != s.readSeq+1 || f.Kind != kind || len(f.Payload) == 0 {
		return errors.New("invalid frame order or header")
	}
	if s.nonce == "" {
		s.nonce = f.Nonce
	} else if f.Nonce != s.nonce {
		return errors.New("wrong session nonce")
	}
	if len(frame) > maxControlFrame {
		return errors.New("control frame exceeds bound")
	}
	if len(f.Payload) == 0 || bytes.TrimSpace(f.Payload)[0] != '{' {
		return errors.New("control payload must be an object")
	}
	if err := rejectDuplicateJSON(f.Payload); err != nil {
		return err
	}
	payloadDecoder := json.NewDecoder(bytes.NewReader(f.Payload))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(payload); err != nil {
		return err
	}
	s.readSeq++
	return nil
}

func validSessionNonce(nonce string) bool {
	if len(nonce) < 16 || len(nonce) > 128 {
		return false
	}
	for _, r := range nonce {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func readFrameWithTimeout(r io.ReadCloser, timeout time.Duration, ctx context.Context) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var header [4]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			ch <- result{err: err}
			return
		}
		n := binary.BigEndian.Uint32(header[:])
		if n == 0 || n > maxProtocolFrame {
			ch <- result{err: errors.New("invalid frame length")}
			return
		}
		data := make([]byte, int(n))
		if _, err := io.ReadFull(r, data); err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{data: data}
	}()
	var timer <-chan time.Time
	var tm *time.Timer
	if timeout > 0 {
		tm = time.NewTimer(timeout)
		timer = tm.C
		defer tm.Stop()
	}
	select {
	case got := <-ch:
		return got.data, got.err
	case <-timer:
		_ = r.Close()
		return nil, context.DeadlineExceeded
	case <-ctxDone(ctx):
		_ = r.Close()
		return nil, ctx.Err()
	}
}

func ctxDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func rejectDuplicateJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil {
		return err
	}
	if err := scanJSONValue(dec, first); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing json value")
		}
		return err
	}
	return nil
}

func scanJSONValue(dec *json.Decoder, first json.Token) error {
	delim, ok := first.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return errors.New("invalid or duplicate json key")
			}
			seen[key] = true
			value, err := dec.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(dec, value); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid json object")
		}
	case '[':
		for dec.More() {
			value, err := dec.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(dec, value); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid json array")
		}
	default:
		return errors.New("invalid json delimiter")
	}
	return nil
}

func bindSelector(path string) selectorBinding {
	binding := selectorBinding{Path: path, Status: "unavailable"}
	if path == "" {
		return binding
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		binding.Resolved = resolved
	}
	if target, err := os.Readlink(path); err == nil {
		binding.LinkTarget = target
	}
	info, err := os.Stat(path)
	if err != nil {
		return binding
	}
	binding.Status = "stat"
	binding.Mode = uint32(info.Mode())
	binding.Size = info.Size()
	binding.ModTimeNS = info.ModTime().UnixNano()
	return binding
}

type boundedCapture struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	remaining := b.limit + 1 - len(b.data)
	if remaining > 0 {
		n := len(p)
		if n > remaining {
			n = remaining
		}
		b.data = append(b.data, p[:n]...)
	}
	if len(p) > remaining || len(b.data) > b.limit {
		b.overflow = true
		b.mu.Unlock()
		b.cancel()
		return len(p), nil
	}
	b.mu.Unlock()
	return len(p), nil
}

func (b *boundedCapture) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), b.overflow
}

func (s *toolchainSession) runCommand(ctx context.Context, stage string, original *exec.Cmd, stdoutLimit int) (commandObservation, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, original.Path, original.Args[1:]...)
	cmd.Args = append([]string(nil), original.Args...)
	cmd.Dir, cmd.Env = original.Dir, append([]string(nil), original.Env...)
	cmd.WaitDelay = 2 * time.Second
	cwd, err := os.Getwd()
	if cmd.Dir != "" {
		cwd = cmd.Dir
	}
	if err != nil {
		cancel()
		return commandObservation{}, err
	}
	if !utf8.ValidString(cmd.Path) || !utf8.ValidString(cwd) {
		cancel()
		return commandObservation{}, errors.New("command context is not UTF-8")
	}
	for _, arg := range cmd.Args {
		if !utf8.ValidString(arg) {
			cancel()
			return commandObservation{}, errors.New("command arguments are not UTF-8")
		}
	}
	env, err := selectedEnvironment(cmd.Environ())
	if err != nil {
		cancel()
		return commandObservation{}, err
	}
	stdout := &boundedCapture{limit: stdoutLimit, cancel: cancel}
	stderr := &boundedCapture{limit: commandStderrLimit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	capture := commandObservation{Stage: stage, Path: cmd.Path, Args: append([]string(nil), cmd.Args...),
		Selector: cmd.Args[0], Before: bindSelector(cmd.Path), CWD: cwd, Environment: env, Status: "start-failed"}
	startErr := cmd.Start()
	if startErr == nil {
		capture.Started = true
		if cmd.Process != nil {
			capture.PID = cmd.Process.Pid
		}
		waitErr := cmd.Wait()
		if state := cmd.ProcessState; state != nil {
			code := state.ExitCode()
			if code >= 0 {
				capture.ExitCode = &code
			}
		}
		switch {
		case cmdCtx.Err() != nil:
			capture.Status = "canceled"
		case waitErr == nil:
			capture.Status = "exited"
		case capture.ExitCode != nil:
			capture.Status = "exit-failed"
		default:
			capture.Status = "signal-failed"
		}
	} else {
		startErr = fmt.Errorf("command start failed")
	}
	cancel()
	capture.After = bindSelector(cmd.Path)
	capture.Stdout, capture.StdoutOverflow = stdout.snapshot()
	capture.Stderr, capture.StderrOverflow = stderr.snapshot()
	if capture.StdoutOverflow || capture.StderrOverflow {
		return capture, errObservedOutputOverflow
	}
	if startErr != nil {
		return capture, startErr
	}
	if capture.Status != "exited" {
		return capture, errors.New("controlled command did not exit successfully")
	}
	return capture, nil
}

type sharedToolchain struct {
	once        sync.Once
	done        chan struct{}
	joinStarted chan struct{}
	joinOnce    sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
	session     *toolchainSession
	cleanupWait time.Duration
	acquire     func(context.Context) (string, error)
	goBin       string
	err         error
}

func newSharedToolchain(ctx context.Context, acquire func(context.Context) (string, error)) *sharedToolchain {
	return &sharedToolchain{done: make(chan struct{}), joinStarted: make(chan struct{}), ctx: ctx,
		cleanupWait: producerCleanupWait, acquire: acquire}
}

func (s *sharedToolchain) get() (string, error) {
	s.once.Do(func() {
		go func() {
			s.goBin, s.err = s.acquire(s.ctx)
			close(s.done)
		}()
	})
	select {
	case <-s.done:
		if err := s.contextErr(); err != nil {
			return "", err
		}
		return s.goBin, s.err
	case <-ctxDone(s.ctx):
		if s.cancel != nil {
			s.cancel()
		}
		s.joinOnce.Do(func() { close(s.joinStarted) })
		timer := time.NewTimer(s.cleanupWait)
		defer timer.Stop()
		select {
		case <-s.done:
			return "", s.contextErr()
		case <-timer.C:
			if s.session != nil {
				s.session.markCleanupUnavailable()
			}
			return "", errProducerCleanupUnavailable
		}
	}
}

func (s *sharedToolchain) contextErr() error {
	if s.ctx == nil {
		return nil
	}
	return s.ctx.Err()
}
