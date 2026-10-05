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
// --repin it first re-pins a tree whose program has moved. The integration harness runs it
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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pack-binaries:", err)
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultDeps(root, os.Environ())))
}

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
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		cacheDir: paths.PackBinariesDir,
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
	// goos and goarch are the machine seed builds for: a host reference's build for this pair,
	// a jail reference's for linux on goarch, which is what a launch here asks the cache for
	// (BP-D3).
	goos, goarch string
	// cacheDir is the pack-binary cache seed fills when it is named none: the one every launch
	// on this machine reads (paths.PackBinariesDir, which internal/loopholes' BinaryCacheDir
	// resolves against too).
	cacheDir func() string
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
	all, err := releasematrix.Manifests(os.DirFS(filepath.Join(t.d.root, "packs")))
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

// seed is the seed verb: this machine's builds of every official binary, made with the recipe,
// each admitted to the cache at dir through packbin's verified rename when its digest is the
// pin (BP-D15). Nothing is downloaded but the pinned toolchain, once, into the module cache.
//
// A build whose digest is not the pin is a program this tree has changed since it was pinned.
// Without repin it is not seeded, and the run fails naming the pin command, the integration
// harness's case; with repin, `just install`'s, the tree is re-pinned first — every build's
// sha256, each url kept, exactly what `just pin-pack-binaries` writes — so the manifest the
// install then embeds pins the build it seeds, and the fork that changed a program runs it.
func (t *task) seed(tmp, dir string, repin bool, stdout io.Writer) int {
	needs, problems := t.machineNeeds(stdout)
	if len(needs) == 0 && len(problems) == 0 {
		fmt.Fprintf(stdout, "pack-binaries: no official binary runs on %s/%s — nothing to seed\n",
			t.d.goos, t.d.goarch)
		return 0
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
	if len(stale) > 0 && repin {
		if t.refusePin() {
			fmt.Fprintln(t.stderr, "pack-binaries: nothing was seeded")
			return 1
		}
		if err := t.buildWanted(tmp, stdout); err != nil {
			fmt.Fprintf(t.stderr, "pack-binaries: %v\npack-binaries: nothing was seeded\n", err)
			return 1
		}
		if rc := t.pin(stdout); rc != 0 {
			fmt.Fprintln(t.stderr, "pack-binaries: nothing was seeded")
			return rc
		}
		for _, n := range stale {
			fmt.Fprintf(stdout, "%s: re-pinned binary %s (%s): this tree's program has moved "+
				"since it was pinned — commit the manifest, since `just check-ci` refuses the "+
				"old pin\n", n.e.Path, n.name, n.platform)
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
