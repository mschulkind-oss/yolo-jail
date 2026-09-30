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
//	go run ./tools/pack-binaries pin <version>          write each build's url and sha256
//	go run ./tools/pack-binaries check <version>        build and compare; write nothing
//	go run ./tools/pack-binaries stage <version> <dir>  check, then write each build into <dir>
//
// `pin` is `just pin-pack-binaries <version>`, run before the release is cut and committed with
// its changelog section. `check` is the gate `just release` runs before it tags, and the one
// publish.yml runs before PyPI. `stage` is goreleaser's before-hook in the release itself
// (.goreleaser.yaml), and the files it writes are uploaded as the release's own files.
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
	"sort"
	"strings"

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
// go on PATH, and never with that go itself.
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
}

const usage = `usage: pack-binaries pin <version>
       pack-binaries check <version>
       pack-binaries stage <version> <dir>
Run from the checkout's root. <version> is the release, X.Y.Z or X.Y.Z-pre, with or without a v.`

func run(args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	verb, stageDir := args[0], ""
	switch {
	case (verb == "pin" || verb == "check") && len(args) == 2:
	case verb == "stage" && len(args) == 3:
		stageDir = args[2]
		if !filepath.IsAbs(stageDir) {
			stageDir = filepath.Join(d.root, stageDir)
		}
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
	version, err := releasematrix.NormalizeVersion(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
		return 2
	}
	t := &task{d: d, verb: verb, version: version, stderr: stderr}
	if err := t.prepare(); err != nil {
		fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
		return 1
	}
	if stageDir != "" {
		if err := emptyDir(stageDir); err != nil {
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
	if err := t.build(tmp, stdout); err != nil {
		fmt.Fprintf(stderr, "pack-binaries: %v\n", err)
		return 1
	}

	switch verb {
	case "pin":
		return t.pin(stdout)
	case "check":
		if t.report() {
			return 1
		}
		fmt.Fprintf(stdout, "pack-binaries: every official build matches v%s (%d builds)\n",
			version, len(t.built))
		return 0
	default: // stage
		if t.report() {
			fmt.Fprintf(stderr, "pack-binaries: nothing was staged in %s\n", stageDir)
			return 1
		}
		return t.stage(stageDir, stdout, stderr)
	}
}

// task is one run of a verb over the checkout.
type task struct {
	d       deps
	verb    string
	version string
	stderr  io.Writer

	read     int
	official []releasematrix.Entry
	release  []string
	problems []releasematrix.Problem
	// built is each (binary, platform) built, by key.
	built map[string]build
}

// build is one official build as the recipe made it.
type build struct {
	name, platform, path, sha256 string
}

func buildKey(name, platform string) string { return name + " " + platform }

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

// buildable reports whether a binary's program can be built at all: the census found nothing
// wrong with it as a program.
func (t *task) buildable(e releasematrix.Entry, name string) bool {
	for _, p := range t.problems {
		if p.Kind == releasematrix.KindProgram && p.Manifest == e.Path && p.Binary == name {
			return false
		}
	}
	return true
}

// build builds, once each, every declared build BP-D7 wants of every buildable binary, and for
// check and stage records each disagreement with the manifest. A declared build BP-D7 does not
// want is the census's to report, and is not built: the program may not build there at all.
func (t *task) build(tmp string, stdout io.Writer) error {
	type want struct {
		e        releasematrix.Entry
		name     string
		platform string
		url, sum string
	}
	var wants []want
	for _, e := range t.official {
		for _, b := range e.Manifest.Binaries {
			if !t.buildable(e, b.Name) {
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
	t.built = map[string]build{}
	if len(wants) == 0 {
		return nil
	}
	goBin, err := t.d.toolchain()
	if err != nil {
		return fmt.Errorf("the pinned toolchain %s: %w", Toolchain, err)
	}
	fmt.Fprintf(stdout, "pack-binaries: building with %s (%s)\n", Toolchain, goBin)
	for _, w := range wants {
		k := buildKey(w.name, w.platform)
		b, done := t.built[k]
		if !done {
			path, sum, err := buildOne(goBin, t.d.root, t.d.environ, w.name, w.platform, tmp)
			if err != nil {
				return err
			}
			b = build{name: w.name, platform: w.platform, path: path, sha256: sum}
			t.built[k] = b
		}
		if t.verb == "pin" {
			continue
		}
		if _, v, _, ok := releasematrix.ParseAssetURL(w.url); ok && v != t.version {
			t.problems = append(t.problems, releasematrix.Problem{Kind: releasematrix.KindURL,
				Manifest: w.e.Path, Binary: w.name, Platform: w.platform,
				Text: "url names v" + v + "'s release, and this is v" + t.version + "'s — run `just " +
					"pin-pack-binaries " + t.version + "` and commit the result"})
		}
		if b.sha256 != w.sum {
			t.problems = append(t.problems, releasematrix.Problem{Kind: releasematrix.KindDigest,
				Manifest: w.e.Path, Binary: w.name, Platform: w.platform,
				Text: "this tree builds sha256 " + b.sha256 + ", and the manifest pins " + w.sum +
					" — run `just pin-pack-binaries " + t.version + "` and commit the result"})
		}
	}
	return nil
}

// report prints every problem and says whether there was one.
func (t *task) report() bool {
	sort.SliceStable(t.problems, func(i, j int) bool { return t.problems[i].String() < t.problems[j].String() })
	for _, p := range t.problems {
		fmt.Fprintln(t.stderr, "✗", p)
	}
	if len(t.problems) > 0 {
		fmt.Fprintf(t.stderr, "pack-binaries: %d problem(s) with the official pack binaries for v%s\n",
			len(t.problems), t.version)
	}
	return len(t.problems) > 0
}

// refusePin reports the census problems pin cannot write its way out of — a build set that is
// not BP-D7's, or a program that cannot be built — and says whether there was one. A url of
// the wrong form is not among them: pin rewrites every url.
func (t *task) refusePin() bool {
	var blocking []releasematrix.Problem
	for _, p := range t.problems {
		if p.Kind != releasematrix.KindURL {
			blocking = append(blocking, p)
		}
	}
	if len(blocking) == 0 {
		return false
	}
	t.problems = blocking
	t.report()
	fmt.Fprintln(t.stderr, "pack-binaries: nothing was pinned")
	return true
}

// pin writes every build's url and sha256. run has already refused a build set pin cannot
// write (refusePin), so every declared build was built.
func (t *task) pin(stdout io.Writer) int {
	for _, e := range t.official {
		var pins []buildPin
		for _, b := range e.Manifest.Binaries {
			for _, bb := range b.Builds {
				built := t.built[buildKey(b.Name, bb.Platform)]
				pins = append(pins, buildPin{Binary: b.Name, Platform: bb.Platform,
					URL: releasematrix.AssetURL(b.Name, t.version, bb.Platform), SHA256: built.sha256})
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
			fmt.Fprintf(stdout, "%s: pinned for v%s\n", e.Path, t.version)
		} else {
			fmt.Fprintf(stdout, "%s: already pinned for v%s\n", e.Path, t.version)
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
