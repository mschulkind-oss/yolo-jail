package packs_test

// releasedecode_test.go decodes the packs this tree SHIPS with the LAST RELEASE's in-jail
// reader, exactly as a jail that release launched would read them at boot, and fails on any
// break it does not already know about.
//
// WHY. A running jail keeps the binaries it launched with, and its boot reads whatever pack
// tree it is handed: packdecl.DecodeTolerant under packload.TolerateSkew, and then
// LoadJailPacks treats ANY problem LoadDir reports as fatal (internal/entrypoint/packsurfaces.go).
// Tolerance covers an unknown kind or field. It does not cover a known field whose TYPE grew —
// claude's `api_key_env_name` became a list (provider-credential-scope.md, OQ-CN1) — or a new
// value in a closed set, such as a hook name. Either one stops an older jail's boot. So a manifest
// change the current tree reads happily can still stop every jail an older yolo launched from
// booting, the moment that jail is handed the new tree
// (docs/design/attach-skew-and-contract-guardrails.md, "Findings since filing"). This test is the
// place such a change is noticed: it compiles the last release's reader from git and runs it
// over the embed a binary built from this tree carries.
//
// A KNOWN BREAK IS LISTED WITH ITS GUARD: what keeps an old jail from ever reading the new
// manifest. An unlisted break fails. So does a listed one that no longer breaks, since its guard
// line is then describing nothing. Entries are keyed by release, so the next release tag
// retires them without an edit: they become inert and are logged as removable.
//
// WHERE IT RUNS. In the short suite, so the pre-commit gate and CI's check-go job both run it;
// both CI checkouts fetch full history and tags (fetch-depth: 0). Outside CI, a clone without
// tags or git skips it, saying so. Under GitHub Actions a missing tag is a failure, because a
// silent skip there would retire the check with every run green.

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/packs"
)

// knownReleaseBreak is one problem the last release's reader reports for a pack this tree
// ships, and why shipping it is safe anyway.
type knownReleaseBreak struct {
	// release is the tag whose reader reports it. An entry for any other release is inert.
	release string
	// pack is the shipped pack's directory name, which is the name an old jail's boot gives it.
	pack string
	// problem is a substring of the reader's problem line: stable across index shifts
	// (contributes[11] moves when an entry is added above it), specific enough to name one
	// break.
	problem string
	// guard says what keeps a jail that release launched from ever reading this manifest.
	guard string
	// pinnedBy names the test that fails if the guard stops holding, when one exists. It is
	// checked to exist, so a pin cannot be cited and then renamed away.
	pinnedBy string
}

// guardNoRestageOnAttach is the guard for every pack-contract break an old jail could meet.
// ⚠ It holds only once OQ-PK2's per-launch pack trees land (docs/reference/pack-system.md#oq-pk2):
// until then an attach re-stages the host's packs into the tree a running podman jail binds,
// and re-runs that jail's boot over them. The change that builds the trees sets pinnedBy on
// these entries to the test that pins an attach writing nothing into a running jail's tree.
const guardNoRestageOnAttach = "an attach never re-stages (one immutable pack tree per launch, " +
	"pack-system.md OQ-PK2), so a jail an older yolo launched never reads this tree's packs; " +
	"only a fresh launch does, and a fresh launch mounts this tree's binaries"

// knownReleaseBreaks is the allowlist. Measured against v0.10.0 on 2026-09-26: claude's bedrock
// provider declares api_key_env_name as a list where v0.10.0's packdecl declares a string, and pi
// declares two hooks v0.10.0 does not know.
var knownReleaseBreaks = []knownReleaseBreak{
	{release: "v0.10.0", pack: "claude", problem: "api_key_env_name", guard: guardNoRestageOnAttach},
	{release: "v0.10.0", pack: "pi", problem: `unknown hook "shared_directory"`, guard: guardNoRestageOnAttach},
	{release: "v0.10.0", pack: "pi", problem: `unknown hook "unshare_directory"`, guard: guardNoRestageOnAttach},
}

// releaseDecodeProbe is the program compiled INSIDE the last release's tree: that release's
// reader, called the way its jail boot calls it. It may use only what every release since
// v0.9.0 exports with these signatures; internal/packload's TestReleaseDecodeProbeAPIIsStable
// keeps this tree's copy of them fixed, since this tree is the next release.
const releaseDecodeProbe = `package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func main() {
	packload.TolerateSkew()
	entries, err := os.ReadDir(os.Args[1])
	if err != nil {
		panic(err)
	}
	out := map[string]map[string][]string{}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p, problems := packload.LoadDir(filepath.Join(os.Args[1], name), name)
		var notes []string
		if p != nil {
			notes = p.SkewNotes
		}
		out[name] = map[string][]string{"problems": problems, "skipped": notes}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`

// TestShippedPacksDecodeUnderTheLastRelease is the check.
func TestShippedPacksDecodeUnderTheLastRelease(t *testing.T) {
	root, release := lastRelease(t)
	for _, b := range knownReleaseBreaks {
		if b.guard == "" {
			t.Errorf("the known break %s/%q names no guard; an allowlist entry is a claim that it is "+
				"safe, and the guard is the claim", b.pack, b.problem)
		}
		if b.pinnedBy != "" && !testFuncExists(t, root, b.pinnedBy) {
			t.Errorf("the known break %s/%q cites %s as its pin, and no test of that name exists",
				b.pack, b.problem, b.pinnedBy)
		}
	}

	old := t.TempDir()
	extractRelease(t, root, release, old)
	probeDir := filepath.Join(old, "cmd", "yolo-release-decode-probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(releaseDecodeProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	shipped := filepath.Join(t.TempDir(), "packs")
	materialize(t, shipped)

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH to build %s's reader with", release)
	}
	cmd := exec.Command(goBin, "run", "./cmd/yolo-release-decode-probe", shipped)
	cmd.Dir = old
	// Hermetic: the release's own vendor tree, this machine's toolchain, no network, and no
	// workspace file or GOFLAGS from the environment steering the build.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=vendor", "GOTOOLCHAIN=local", "GOWORK=off",
		"GOPROXY=off", "CGO_ENABLED=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("building or running %s's pack reader failed: %v\n%s\n"+
			"If %s's packload no longer exports TolerateSkew() and LoadDir(root, name) (*Pack, "+
			"[]string), update releaseDecodeProbe to that release's in-jail entry point.",
			release, err, stderr.String(), release)
	}
	var got map[string]map[string][]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the probe's output is not JSON: %v\n%s", err, raw)
	}
	if len(got) == 0 {
		t.Fatalf("%s's reader decoded no packs from %s, so this test checked nothing", release, shipped)
	}

	used := make([]bool, len(knownReleaseBreaks))
	for pack, res := range got {
		for _, note := range res["skipped"] {
			t.Logf("%s skips, without failing its boot: %s", release, note)
		}
		for _, problem := range res["problems"] {
			matched := false
			for i, b := range knownReleaseBreaks {
				if b.release == release && b.pack == pack && strings.Contains(problem, b.problem) {
					used[i], matched = true, true
				}
			}
			if !matched {
				t.Errorf("a jail %s launched cannot boot on pack %q as this tree ships it:\n\t%s\n"+
					"Its boot treats any decode problem as fatal. Either keep the manifest readable by "+
					"%s's reader, or add a knownReleaseBreaks entry whose guard says what keeps such "+
					"a jail from ever reading it.", release, pack, problem, release)
			}
		}
	}
	for i, b := range knownReleaseBreaks {
		switch {
		case b.release != release:
			t.Logf("inert: the known break %s/%q is for %s, not the last release %s — removable",
				b.pack, b.problem, b.release, release)
		case !used[i]:
			t.Errorf("the known break %s/%q no longer occurs under %s's reader; remove the entry, "+
				"whose guard now describes nothing", b.pack, b.problem, release)
		}
	}
}

// lastRelease finds the repository root and the newest release tag before this commit: the
// release a jail still running on this machine was most plausibly launched by. HEAD^, not HEAD,
// so the release commit itself is compared against the release before it rather than against
// its own reader, which would compare nothing.
func lastRelease(t *testing.T) (root, tag string) {
	t.Helper()
	inCI := os.Getenv("GITHUB_ACTIONS") == "true"
	unavailable := func(format string, args ...any) {
		t.Helper()
		if inCI {
			t.Fatalf(format+" — CI must fetch full history and tags (actions/checkout fetch-depth: 0) "+
				"for this check to run", args...)
		}
		t.Skipf(format, args...)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		unavailable("no git on PATH to read the last release from")
	}
	// The MODULE root, found from this package's directory, which is where `go test` runs it —
	// not `git rev-parse --show-toplevel`. Inside a git hook (the pre-commit gate runs this test)
	// git exports GIT_DIR without GIT_WORK_TREE, and --show-toplevel then answers the current
	// directory, packs/, so every pinnedBy lookup below searched packs/internal and found nothing.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Dir(wd)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("this test runs from packs/, so %s should be the module root, and it holds no go.mod: %v", root, err)
	}
	if err := exec.Command(git, "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		unavailable("not a git checkout, so there is no last release to read")
	}
	out, err := exec.Command(git, "-C", root, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "HEAD^").Output()
	if err != nil {
		unavailable("no release tag is reachable from HEAD^ (a shallow clone, or no tags fetched)")
	}
	return root, strings.TrimSpace(string(out))
}

// extractRelease writes the tree at tag into dest, streaming `git archive` through archive/tar
// so no tar binary is needed. The whole tree, not a package list: a future release's reader may
// import anything in its module.
func extractRelease(t *testing.T, root, tag, dest string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "archive", "--format=tar", tag)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(stdout)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading git archive %s: %v", tag, err)
		}
		name := filepath.Clean(h.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		path := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			_ = os.Symlink(h.Linkname, path)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("git archive %s: %v\n%s", tag, err, stderr.String())
	}
}

// materialize copies the embed a binary built from this tree carries into dest, one directory
// per pack: the shape a host stages into a jail's _official tree.
func materialize(t *testing.T, dest string) {
	t.Helper()
	err := fs.WalkDir(packs.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(packs.FS, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// testFuncExists reports whether any _test.go file under root declares func name(.
func testFuncExists(t *testing.T, root, name string) bool {
	t.Helper()
	re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
	found := false
	for _, dir := range []string{"internal", "cmd", "packs", "integration"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if b, err := os.ReadFile(path); err == nil && re.Match(b) {
				found = true
			}
			return nil
		})
	}
	return found
}
