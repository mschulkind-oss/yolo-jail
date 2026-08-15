package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The ship set is spelled in THREE places and each one is silent when it is
// wrong, which is why they are pinned together here rather than trusted:
//
//   - cmd/*/ — every main package that exists. goBinaries compiles all of them.
//   - flake.nix's shippedBinaries — the filter that decides which ones are
//     copied into /opt/yolo-jail/bin, /bin-linked and therefore ON PATH. A
//     binary missing here VANISHES from the image while `go build ./...` stays
//     green (the goSrc fileset trap, one layer down).
//   - scripts/stage-source-bundle.sh's SHIPPED_BINARIES — what a SHIPPED bundle
//     carries as prebuilt artifacts. flake.nix's prebuilt short-circuit does
//     `[ -e "$src" ] || continue`, so a binary missing there is silently skipped
//     when an image is built from a bundle, and present when it is built from a
//     source checkout. That divergence is invisible from inside a dev jail,
//     which only ever takes the source path.
//
// goprobe is the one deliberate omission — a dev-only deployment tripwire that
// must never reach the runtime PATH. It is listed explicitly so that "absent
// from the ship set" has to be a decision rather than an oversight; an
// accidental omission is otherwise indistinguishable from the intended one.
var shipSetExemptCmds = map[string]bool{"goprobe": true}

// repoRoot locates the checkout from this test file's compile-time path.
//
// It FAILS rather than skips when the tree is not readable. A skip here would
// turn the exact silent-vanish failure this test exists to catch into silent
// non-coverage, which is the same shape of bug one level up.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	if _, err := os.Stat(filepath.Join(root, "flake.nix")); err != nil {
		t.Fatalf("no flake.nix at the resolved repo root %s: %v", root, err)
	}
	return root
}

// nixListRe captures the contents of `shippedBinaries = [ ... ];`.
var nixListRe = regexp.MustCompile(`(?m)^\s*shippedBinaries\s*=\s*\[([^\]]*)\]\s*;`)

// bashArrayRe captures the contents of `SHIPPED_BINARIES=( ... )`.
var bashArrayRe = regexp.MustCompile(`(?m)^SHIPPED_BINARIES=\(([^)]*)\)`)

func parseShipList(t *testing.T, path string, re *regexp.Regexp) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := re.FindSubmatch(body)
	if m == nil {
		t.Fatalf("%s no longer declares a ship set matching %s — has the list been "+
			"renamed or restructured? This test is the only thing keeping the three "+
			"copies in agreement.", path, re)
	}
	set := map[string]bool{}
	for _, f := range strings.Fields(string(m[1])) {
		set[strings.Trim(f, `"`)] = true
	}
	if len(set) == 0 {
		t.Fatalf("%s declares an EMPTY ship set", path)
	}
	return set
}

func cmdBinaries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatalf("read cmd/: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("cmd/ has no binaries — the resolved repo root is wrong")
	}
	return names
}

// TestEveryCmdBinaryIsShippedOrExempt is the tripwire for "a new cmd/ binary
// silently vanishes from the image". Adding cmd/foo and forgetting flake.nix
// leaves `go build ./...` green, every unit test green, and `foo` simply absent
// from every jail — reported by users as "the tool isn't installed".
func TestEveryCmdBinaryIsShippedOrExempt(t *testing.T) {
	root := repoRoot(t)
	shipped := parseShipList(t, filepath.Join(root, "flake.nix"), nixListRe)

	for _, name := range cmdBinaries(t, root) {
		switch {
		case shipEnumerated(shipped, name):
			if shipSetExemptCmds[name] {
				t.Errorf("cmd/%s is BOTH exempt and in flake.nix's shippedBinaries — "+
					"decide which", name)
			}
		case shipSetExemptCmds[name]:
			// Deliberately not shipped.
		default:
			t.Errorf("cmd/%s is not in flake.nix's shippedBinaries and is not exempt, "+
				"so it compiles but never reaches the image. Add it to shippedBinaries "+
				"(and to scripts/stage-source-bundle.sh), or add it to "+
				"shipSetExemptCmds with a reason.", name)
		}
	}
}

// TestShipSetsAgreeAcrossFlakeAndBundle pins the source-checkout image and the
// shipped-bundle image to the same set. A binary in one and not the other means
// two builds of "the same" release differ in what they install, and only one of
// them is ever exercised in development.
func TestShipSetsAgreeAcrossFlakeAndBundle(t *testing.T) {
	root := repoRoot(t)
	flakeSet := parseShipList(t, filepath.Join(root, "flake.nix"), nixListRe)
	bundleSet := parseShipList(t, filepath.Join(root, "scripts", "stage-source-bundle.sh"), bashArrayRe)

	for name := range flakeSet {
		if !bundleSet[name] {
			t.Errorf("%q is in flake.nix's shippedBinaries but not in "+
				"stage-source-bundle.sh's SHIPPED_BINARIES — an image built from a "+
				"shipped bundle would silently omit it (the prebuilt loop does "+
				"`[ -e \"$src\" ] || continue`)", name)
		}
	}
	for name := range bundleSet {
		if !flakeSet[name] {
			t.Errorf("%q is staged into the bundle but is not in flake.nix's "+
				"shippedBinaries, so nothing ever copies it out of the bundle", name)
		}
	}
}

// TestLoopholeClientsAreBaked names the two in-jail loophole clients
// explicitly. They were generated Python in ~/.local/bin until the transport
// retirement (docs/design/loophole-transport.md §8.4), and the whole point of
// the port is that the image provides them — a regression that dropped either
// from the ship set would leave the jail with NO client at all, since the
// generator that used to write one is gone.
func TestLoopholeClientsAreBaked(t *testing.T) {
	root := repoRoot(t)
	shipped := parseShipList(t, filepath.Join(root, "flake.nix"), nixListRe)
	for _, name := range []string{"yolo-cglimit", "yolo-journalctl"} {
		if !shipped[name] {
			t.Errorf("%s is not baked into the image, and nothing generates it any "+
				"more — the jail would have no client for that loophole", name)
		}
	}
}

func shipEnumerated(set map[string]bool, name string) bool { return set[name] }
