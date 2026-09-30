package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// The tool's tests run over a FIXTURE CHECKOUT, because no official pack declares a binary yet
// (docs/design/broker-as-a-pack.md §14.4): a module named like this one, with a packs tree
// holding one loophole whose host daemon runs `toold`, a cmd/toold, and a goreleaser config
// whose `yolo` build ships to two platforms. The go on PATH stands in for the pinned toolchain,
// which a test must not download; fetchToolchain has its own test with a stub.

const fixtureManifestPath = "packs/p/loopholes/tool/manifest.jsonc"

// placeholder is what an author writes for a build before its first pin: the release file's
// url for some version, and a digest of zeros.
func placeholder(platform string) string {
	return `{"url": "` + releasematrix.AssetURL("toold", "0.0.1", platform) + `", "sha256": "` +
		strings.Repeat("0", 64) + `"}`
}

func fixtureManifest(platforms ...string) string {
	var builds []string
	for _, p := range platforms {
		builds = append(builds, `      "`+p+`": `+placeholder(p)+`, // the `+p+` build`)
	}
	return `// A fixture loophole: an official binary before its first pin.
{
  "name": "tool",
  "description": "fixture",
  "version": 1,
  /* the program the host daemon runs */
  "binaries": {
    "toold": {
` + strings.Join(builds, "\n") + `
    },
  },
  "host_daemon": {"cmd": ["{binary:toold}", "--socket", "{socket}"], "publishes": "socket"},
}
`
}

const fixtureMain = `package main

// stamp is empty unless a build stamps it, which the recipe never does.
var stamp string

func main() { println("toold", stamp) }
`

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fixtureCheckout(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "go.mod", "module "+releasematrix.ModulePath+"\n\ngo 1.26.0\n")
	writeFile(t, root, releasematrix.GoreleaserConfig,
		"builds:\n  - id: yolo\n    main: ./cmd/yolo\n    goos: [linux, darwin]\n    goarch: [amd64]\n")
	writeFile(t, root, "packs/embed.go", "package packs\n")
	writeFile(t, root, "cmd/toold/main.go", fixtureMain)
	writeFile(t, root, fixtureManifestPath, fixtureManifest("darwin/amd64", "linux/amd64"))
	return root
}

// hostGo is the go on PATH, standing in for the pinned toolchain.
func hostGo(t *testing.T) string {
	t.Helper()
	g, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH to build the fixture with")
	}
	return g
}

type result struct {
	code           int
	stdout, stderr string
	fetched        bool
}

func runTool(t *testing.T, root string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	var r result
	g := hostGo(t)
	r.code = run(args, &out, &errb, deps{root: root, environ: os.Environ(),
		toolchain: func() (string, error) { r.fetched = true; return g, nil }})
	r.stdout, r.stderr = out.String(), errb.String()
	return r
}

func decodeFixture(t *testing.T, root string) *loopholedecl.Manifest {
	t.Helper()
	m, err := loopholedecl.Decode([]byte(readFile(t, root, fixtureManifestPath)), "packs/p/loopholes/tool")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The release in order: pin writes each build's url and sha256 in place, check agrees, and stage
// writes files whose digests are the pins.
func TestPinThenCheckThenStage(t *testing.T) {
	root := fixtureCheckout(t)
	r := runTool(t, root, "pin", "v0.2.0")
	if r.code != 0 || !strings.Contains(r.stdout, fixtureManifestPath+": pinned for v0.2.0") {
		t.Fatalf("pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	body := readFile(t, root, fixtureManifestPath)
	for _, keep := range []string{"// A fixture loophole", "/* the program the host daemon runs */",
		"// the linux/amd64 build", "// the darwin/amd64 build"} {
		if !strings.Contains(body, keep) {
			t.Errorf("pin lost %q from the manifest:\n%s", keep, body)
		}
	}
	m := decodeFixture(t, root)
	sums := map[string]string{}
	for _, bb := range m.Binaries[0].Builds {
		if want := releasematrix.AssetURL("toold", "0.2.0", bb.Platform); bb.URL != want {
			t.Errorf("%s url = %s, want %s", bb.Platform, bb.URL, want)
		}
		if bb.SHA256 == strings.Repeat("0", 64) {
			t.Errorf("%s sha256 is still the placeholder", bb.Platform)
		}
		sums[bb.Platform] = bb.SHA256
	}
	if sums["linux/amd64"] == sums["darwin/amd64"] {
		t.Errorf("both platforms pinned the same digest %s", sums["linux/amd64"])
	}

	if r := runTool(t, root, "check", "0.2.0"); r.code != 0 ||
		!strings.Contains(r.stdout, "every official build matches v0.2.0 (2 builds)") {
		t.Fatalf("check after pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}

	dir := filepath.Join(root, "bundle", "pack-binaries")
	if r := runTool(t, root, "stage", "0.2.0", "bundle/pack-binaries"); r.code != 0 {
		t.Fatalf("stage: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"toold_0.2.0_darwin_amd64", "toold_0.2.0_linux_amd64"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("staged %v, want %v", names, want)
	}
	for platform, sum := range sums {
		p := filepath.Join(dir, releasematrix.AssetName("toold", "0.2.0", platform))
		got, err := fileSHA256(p)
		if err != nil || got != sum {
			t.Errorf("%s: sha256 %s (%v), and the manifest pins %s", p, got, err, sum)
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable: %v %v", p, fi.Mode(), err)
		}
	}

	if r := runTool(t, root, "pin", "0.2.0"); r.code != 0 || !strings.Contains(r.stdout, "already pinned for v0.2.0") {
		t.Errorf("a second pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// The call site of the recipe's first item (BP-D9): every build a verb makes is made by the go
// the toolchain dependency returned, never by the go on PATH, whose version nothing ties to the
// release runner's. The stand-in toolchain logs each call and then runs the real go; the go on
// PATH refuses, so a build that reached for it fails the run.
func TestEveryBuildIsMadeWithTheToolchain(t *testing.T) {
	realGo := hostGo(t)
	root := fixtureCheckout(t)
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	writeFile(t, dir, "toolchain/bin/go", "#!/bin/sh\nprintf '%s\\n' \"$1\" >>'"+calls+"'\nexec '"+
		realGo+"' \"$@\"\n")
	writeFile(t, dir, "path/go", "#!/bin/sh\necho 'the go on PATH was run' >&2\nexit 97\n")
	for _, p := range []string{"toolchain/bin/go", "path/go"} {
		if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(p)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Join(dir, "path")+string(os.PathListSeparator)+os.Getenv("PATH"))

	toolchain := filepath.Join(dir, "toolchain", "bin", "go")
	for _, verb := range []string{"pin", "check"} {
		var out, errb bytes.Buffer
		code := run([]string{verb, "0.2.0"}, &out, &errb, deps{root: root, environ: os.Environ(),
			toolchain: func() (string, error) { return toolchain, nil }})
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", verb, code, out.String(), errb.String())
		}
	}
	// Two platforms, built once by pin and once by check.
	if got := strings.Fields(readFile(t, dir, "calls")); strings.Join(got, " ") != "build build build build" {
		t.Errorf("the toolchain ran %q, want four builds", got)
	}
}

// What done looks like (§14.4): editing the program after pinning makes the check refuse, naming
// the binary, the platform and both digests — and stage publishes nothing.
func TestCheckRefusesAProgramEditedAfterThePin(t *testing.T) {
	root := fixtureCheckout(t)
	if r := runTool(t, root, "pin", "0.2.0"); r.code != 0 {
		t.Fatalf("pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	old, _ := decodeFixture(t, root).Binaries[0].BuildFor("linux/amd64")
	writeFile(t, root, "cmd/toold/main.go", strings.Replace(fixtureMain, `"toold"`, `"toold, edited"`, 1))

	r := runTool(t, root, "check", "0.2.0")
	if r.code != 1 {
		t.Fatalf("check of an edited program: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{
		fixtureManifestPath + ": binary toold (linux/amd64): this tree builds sha256 ",
		"and the manifest pins " + old.SHA256,
		"binary toold (darwin/amd64)",
		"just pin-pack-binaries 0.2.0",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("check's refusal does not say %q:\n%s", want, r.stderr)
		}
	}

	r = runTool(t, root, "stage", "0.2.0", "staged")
	if r.code != 1 || !strings.Contains(r.stderr, "nothing was staged") {
		t.Errorf("stage of an edited program: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "staged")); len(entries) != 0 {
		t.Errorf("stage refused and still wrote %v", entries)
	}
}

func TestCheckRefusesAURLForAnotherRelease(t *testing.T) {
	root := fixtureCheckout(t)
	if r := runTool(t, root, "pin", "0.2.0"); r.code != 0 {
		t.Fatalf("pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	r := runTool(t, root, "check", "0.3.0")
	if r.code != 1 || !strings.Contains(r.stderr, "url names v0.2.0's release, and this is v0.3.0's") {
		t.Errorf("check for another version: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "this tree builds sha256") {
		t.Errorf("the digests match and check says otherwise:\n%s", r.stderr)
	}
}

// Pin writes values, never builds: a build set that is not BP-D7's is refused before anything
// is built or written, naming what to add or drop (BP-D11) — so the refusal costs no toolchain
// download. check builds what it can, since it reports every digest as well.
func TestPinRefusesAManifestOffTheMatrixAndWritesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		platforms []string
		want      string
	}{
		"a missing platform": {[]string{"linux/amd64"}, "binary toold (darwin/amd64): has no build here"},
		"an extra platform": {[]string{"darwin/amd64", "linux/amd64", "linux/arm64"},
			"binary toold (linux/arm64): declares a build no release produces"},
	} {
		t.Run(name, func(t *testing.T) {
			root := fixtureCheckout(t)
			writeFile(t, root, fixtureManifestPath, fixtureManifest(tc.platforms...))
			before := readFile(t, root, fixtureManifestPath)
			for _, verb := range []string{"pin", "check"} {
				r := runTool(t, root, verb, "0.2.0")
				if r.code != 1 || !strings.Contains(r.stderr, tc.want) {
					t.Errorf("%s: exit %d, want 1 naming %q\n%s%s", verb, r.code, tc.want, r.stdout, r.stderr)
				}
				if verb == "pin" && (r.fetched || !strings.Contains(r.stderr, "nothing was pinned")) {
					t.Errorf("pin fetched the toolchain (%v) before refusing a build set it will not "+
						"pin, or did not say nothing was pinned:\n%s%s", r.fetched, r.stdout, r.stderr)
				}
			}
			if after := readFile(t, root, fixtureManifestPath); after != before {
				t.Errorf("a refused pin rewrote the manifest:\n%s", after)
			}
		})
	}
}

// The two refusals about the program itself, which leave nothing to build, so no toolchain is
// fetched for them.
func TestCheckRefusesAProgramItCannotPin(t *testing.T) {
	root := fixtureCheckout(t)
	writeFile(t, root, "cmd/toold/main.go", "package main\n\nimport _ \""+releasematrix.EmbedPackage+
		"\"\n\nfunc main() {}\n")
	r := runTool(t, root, "check", "0.2.0")
	if r.code != 1 || !strings.Contains(r.stderr, "cmd/toold links the packs embed (cmd/toold → packs)") {
		t.Errorf("a program linking the embed: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r.fetched {
		t.Error("the toolchain was fetched for a program that cannot be pinned")
	}

	if err := os.RemoveAll(filepath.Join(root, "cmd", "toold")); err != nil {
		t.Fatal(err)
	}
	r = runTool(t, root, "pin", "0.2.0")
	if r.code != 1 || !strings.Contains(r.stderr, "there is no cmd/toold") {
		t.Errorf("a binary with no program: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// With no official binary, every verb succeeds and fetches nothing — which is every release
// until the first one lands, so `just release` and both workflows pay nothing for the gate.
func TestNoOfficialBinaryFetchesNoToolchain(t *testing.T) {
	root := fixtureCheckout(t)
	writeFile(t, root, fixtureManifestPath, `{"name": "tool", "description": "no binaries"}`)
	for _, args := range [][]string{{"pin", "0.2.0"}, {"check", "0.2.0"}, {"stage", "0.2.0", "out"}} {
		r := runTool(t, root, args...)
		if r.code != 0 || r.fetched || !strings.Contains(r.stdout, "no official pack declares a binary (read 1 loophole manifests)") {
			t.Errorf("%v: exit %d fetched=%v\n%s%s", args, r.code, r.fetched, r.stdout, r.stderr)
		}
	}
	if fi, err := os.Stat(filepath.Join(root, "out")); err != nil || !fi.IsDir() {
		t.Errorf("stage with nothing to stage left no directory for goreleaser's glob: %v", err)
	}
}

func TestStageRefusesADirectoryThatIsNotEmpty(t *testing.T) {
	root := fixtureCheckout(t)
	writeFile(t, root, "out/left-over", "x")
	r := runTool(t, root, "stage", "0.2.0", "out")
	if r.code != 1 || !strings.Contains(r.stderr, "is not empty, and everything in it would be uploaded") {
		t.Errorf("stage into a non-empty dir: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestUsageAndRefusals(t *testing.T) {
	root := fixtureCheckout(t)
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{nil, 2, "usage:"},
		{[]string{"check"}, 2, "usage:"},
		{[]string{"stage", "0.2.0"}, 2, "usage:"},
		{[]string{"publish", "0.2.0"}, 2, "usage:"},
		{[]string{"check", "latest"}, 2, "is not a release version"},
	} {
		if r := runTool(t, root, tc.args...); r.code != tc.code || !strings.Contains(r.stderr, tc.want) {
			t.Errorf("%v: exit %d, want %d naming %q\n%s", tc.args, r.code, tc.code, tc.want, r.stderr)
		}
	}
	if r := runTool(t, t.TempDir(), "check", "0.2.0"); r.code != 1 || !strings.Contains(r.stderr, "is not the root of") {
		t.Errorf("outside a checkout: exit %d\n%s", r.code, r.stderr)
	}
}

// The tool reads the checkout's packs/, since it writes what it read; the census reads the
// embed, since that is what a release carries. They must be the same manifests.
func TestTheToolReadsTheManifestsTheEmbedCarries(t *testing.T) {
	disk, err := releasematrix.Manifests(os.DirFS(filepath.Join("..", "..", "packs")))
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := releasematrix.Manifests(packs.FS)
	if err != nil {
		t.Fatal(err)
	}
	paths := func(es []releasematrix.Entry) (out []string) {
		for _, e := range es {
			out = append(out, e.Path)
		}
		return out
	}
	if len(disk) == 0 || !reflect.DeepEqual(paths(disk), paths(embedded)) {
		t.Errorf("the checkout carries %v, and the embed %v", paths(disk), paths(embedded))
	}
}
