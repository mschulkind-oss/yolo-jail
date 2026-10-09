package macosuser

// guestbundle_test.go pins OQ-DP8's two delivery facts across the files that carry them, none of
// which can see the others:
//
//   - the GUEST SET is one list in three spellings — GuestBinaries here, flake.nix's
//     guestBinaries, stage-source-bundle.sh's GUEST_BINARIES — and it is built for darwin into
//     the bundle's SHARE dir (bin/darwin-<arch> beside flake.nix), for both release arches;
//   - the HOST SHIP SET stays {yolo}: every channel that installs onto a host PATH (goreleaser's
//     builds, the Homebrew formula's bin, `just install`) installs `yolo` alone, so a darwin
//     yolo-jaild reaches no host PATH — only the sandbox's root-owned GuestBinDir.

import (
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"gopkg.in/yaml.v3"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

func listIn(t *testing.T, body string, re *regexp.Regexp, what string) []string {
	t.Helper()
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s no longer matches %s", what, re)
	}
	var out []string
	for _, f := range strings.Fields(m[1]) {
		out = append(out, strings.Trim(f, `"`))
	}
	sort.Strings(out)
	return out
}

// trustedHomebrewFormula pins both production workflow callers as well as the
// helper containing the formula. Moving the formula out of workflow YAML must
// not make deleting either real caller invisible to the bundle/host-set checks.
func trustedHomebrewFormula(t *testing.T) string {
	t.Helper()
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(repoFile(t, ".github/workflows/release.yml")), &workflow); err != nil {
		t.Fatalf("decode release workflow: %v", err)
	}
	caller := regexp.MustCompile(`(?m)^\s*VERSION="\$RELEASE_VERSION" RELEASE_TAG="v\$\{RELEASE_VERSION\}" tools/release-wiring/update-homebrew\.sh\s*$`)
	for _, job := range []string{"publish-release", "homebrew-only"} {
		found := false
		for _, step := range workflow.Jobs[job].Steps {
			found = found || caller.MatchString(step.Run)
		}
		if !found {
			t.Errorf("release workflow job %s no longer invokes the trusted formula helper with the frozen version/tag", job)
		}
	}
	return repoFile(t, "tools/release-wiring/update-homebrew.sh")
}

// THE GO SPELLING AGREES WITH THE TWO THAT BUILD THE BINARIES. GuestBinaries decides what a
// launch stages (StageGuestBinaryCommands); a name the bundle lacks would stage a missing file,
// and one the bundle has that this lacks would never reach the guest.
func TestGuestBinariesMatchTheFlakeAndTheBundleScript(t *testing.T) {
	want := append([]string{}, GuestBinaries...)
	sort.Strings(want)
	flake := listIn(t, repoFile(t, "flake.nix"),
		regexp.MustCompile(`(?m)^\s*guestBinaries\s*=\s*\[([^\]]*)\]\s*;`), "flake.nix guestBinaries")
	script := listIn(t, repoFile(t, "scripts/stage-source-bundle.sh"),
		regexp.MustCompile(`(?m)^GUEST_BINARIES=\(([^)]*)\)`), "stage-source-bundle.sh GUEST_BINARIES")
	if strings.Join(flake, " ") != strings.Join(want, " ") || strings.Join(script, " ") != strings.Join(want, " ") {
		t.Errorf("guest set disagrees: macosuser %v, flake.nix %v, stage-source-bundle.sh %v", want, flake, script)
	}
}

// THE BUNDLE BUILDS THE GUEST DIRS FOR DARWIN, BOTH ARCHES, INTO THE SHARE DIR. The script's
// default GUEST_OSES is darwin and its default ARCHES both release arches (the release ships
// Intel and Apple Silicon: .goreleaser.yaml's goarch), and each guest dir is $DEST/bin/<os>-<arch>
// — the bundle root beside flake.nix, which a release archive and the formula put under
// share/yolo-jail, never beside the host binary.
func TestTheBundleStagesDarwinGuestDirsForBothArchesUnderItsShareDir(t *testing.T) {
	script := repoFile(t, "scripts/stage-source-bundle.sh")
	for _, want := range []string{
		"GUEST_OSES=(darwin)",
		"ARCHES=(amd64 arm64)",
		`dst_dir="$DEST/bin/${gos}-${arch}"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("stage-source-bundle.sh no longer says %q", want)
		}
	}
	gr := repoFile(t, ".goreleaser.yaml")
	for _, want := range []string{
		"scripts/stage-source-bundle.sh bundle/share/yolo-jail",
		`- src: "bundle/share/yolo-jail"`,
		`dst: "share/yolo-jail"`,
		"- darwin",
		"- amd64",
		"- arm64",
	} {
		if !strings.Contains(gr, want) {
			t.Errorf(".goreleaser.yaml no longer says %q", want)
		}
	}
	rel := trustedHomebrewFormula(t)
	if !strings.Contains(rel, `system "scripts/stage-source-bundle.sh", pkgshare.to_s`) {
		t.Error("the Homebrew formula no longer stages the bundle into pkgshare")
	}
}

// THE HOST SHIP SET IS {yolo}: nothing a host install puts on a PATH is an in-jail binary.
func TestTheHostShipSetStaysYoloAlone(t *testing.T) {
	gr := repoFile(t, ".goreleaser.yaml")
	mains := regexp.MustCompile(`(?m)^\s*main:\s*(\S+)`).FindAllStringSubmatch(gr, -1)
	if len(mains) != 1 || mains[0][1] != "./cmd/yolo" {
		t.Errorf("goreleaser builds %v; the host ship set is ./cmd/yolo alone", mains)
	}
	rel := trustedHomebrewFormula(t)
	bins := regexp.MustCompile(`bin/"([^"]+)"`).FindAllStringSubmatch(rel, -1)
	for _, b := range bins {
		if b[1] != "yolo" {
			t.Errorf("the Homebrew formula installs bin/%q; the host ship set is yolo alone", b[1])
		}
	}
	just := repoFile(t, "Justfile")
	installs := regexp.MustCompile(`(?m)^\s*go install [^\n]*`).FindAllString(just, -1)
	for _, line := range installs {
		if !strings.HasSuffix(strings.TrimSpace(line), "./cmd/yolo") {
			t.Errorf("`just install` runs %q; only ./cmd/yolo reaches $GOBIN", strings.TrimSpace(line))
		}
	}
	// And the guest prefix is the SANDBOX's, under the root-owned state dir.
	if !strings.HasPrefix(GuestBinDir(""), stateDir+"/") {
		t.Errorf("GuestBinDir %s is outside the root-owned state dir", GuestBinDir(""))
	}
}

// EACH GUEST CLIENT READS THE VARIABLE THE LAUNCH STAGES IT ON. The stager keys on
// GuestClient.EndpointEnv (GuestClientsIn); the client resolves its endpoint from its own
// os.Getenv. Those are different binaries, so this reads each client's source for a Getenv of
// that variable, spelled through its paths constant or as the literal. A client changed to read
// another variable fails here, rather than being staged on a launch whose endpoint it no longer
// dials and skipped on the one it does.
func TestEachGuestClientReadsTheEndpointItIsStagedOn(t *testing.T) {
	consts := map[string]string{
		paths.SerialEndpointEnv:        "SerialEndpointEnv",
		paths.HostProcessesEndpointEnv: "HostProcessesEndpointEnv",
		paths.MacosLogEndpointEnv:      "MacosLogEndpointEnv",
	}
	for _, c := range GuestClients {
		ident, ok := consts[c.EndpointEnv]
		if !ok {
			t.Errorf("%s keys on %s, which has no paths constant this test knows", c.Binary, c.EndpointEnv)
			continue
		}
		src := repoFile(t, filepath.Join("cmd", c.Binary, "main.go"))
		if !strings.Contains(src, "os.Getenv(paths."+ident+")") &&
			!strings.Contains(src, `os.Getenv("`+c.EndpointEnv+`")`) {
			t.Errorf("cmd/%s/main.go reads neither os.Getenv(paths.%s) nor os.Getenv(%q); the "+
				"macos-user launch stages it on that variable", c.Binary, ident, c.EndpointEnv)
		}
	}
}

// THE CLIENTS ARE IN THE SHIPPED SET TOO: a guest member flake.nix's shippedBinaries lacks would
// be a binary the container jail never gets, and the guest set is a subset by rule.
func TestEveryGuestBinaryIsAShippedBinary(t *testing.T) {
	shipped := listIn(t, repoFile(t, "flake.nix"),
		regexp.MustCompile(`(?m)^\s*shippedBinaries\s*=\s*\[([^\]]*)\]\s*;`), "flake.nix shippedBinaries")
	for _, name := range GuestBinaries {
		if !slices.Contains(shipped, name) {
			t.Errorf("guest binary %s is not in flake.nix's shippedBinaries %v", name, shipped)
		}
	}
}
