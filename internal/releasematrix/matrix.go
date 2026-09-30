// Package releasematrix is the RELEASE MATRIX of docs/design/broker-as-a-pack.md §14: what
// yolo's own release does for the binaries its official packs declare. It says which platforms
// each binary is built for (BP-D7), which release file each build is published as (BP-D8), and
// what the tree must satisfy before any release can build them at all (the refusals BP-D9
// names).
//
// It REBUILDS NOTHING. The pin tool (tools/pack-binaries, the one program that builds every
// official build, writes its url and sha256 into the manifest, and checks them) does the
// building, digesting, writing and staging. This package is the part that tool shares with the
// short suite's census test (internal/loopholedecl's releasematrix_test.go), so the check the
// release runs and the check every `just check-ci` runs cannot disagree about what an official
// binary must be.
//
// An official pack that pins a program someone else releases is outside the matrix (§14): it
// declares that upstream's builds. Nothing here can tell the two apart, because no official
// pack does that yet, so every `binaries` entry in the packs embed is read as a program this
// module builds.
package releasematrix

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const (
	// ModulePath is this module's path: an official binary is built from its cmd/<name>.
	ModulePath = "github.com/mschulkind-oss/yolo-jail"
	// EmbedPackage is the packs embed. A program that links it carries every official
	// manifest in its bytes, its own digest included, so no build of it could match the pin.
	EmbedPackage = ModulePath + "/packs"
	// ReleaseDownloadBase is where a tag's release files are served from (BP-D8). An asset
	// URL there redirects over https to GitHub's asset host, which the download's redirect
	// rule requires (BP-D4).
	ReleaseDownloadBase = "https://github.com/mschulkind-oss/yolo-jail/releases/download/"
	// ReleaseBuildID is the goreleaser build whose platforms are the matrix: the `yolo` build,
	// because a host binary runs where the host yolo runs (BP-D7).
	ReleaseBuildID = "yolo"
	// GoreleaserConfig is the release's config file, relative to the checkout.
	GoreleaserConfig = ".goreleaser.yaml"
)

// versionRE is a release version, the same pattern `just release` accepts.
var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// NormalizeVersion returns version without a leading "v", refusing anything `just release`
// would not cut.
func NormalizeVersion(version string) (string, error) {
	v := strings.TrimPrefix(version, "v")
	if !versionRE.MatchString(v) {
		return "", fmt.Errorf("%q is not a release version (want X.Y.Z or X.Y.Z-pre)", version)
	}
	return v, nil
}

// AssetName is the release file a build is published as: `<name>_<version>_<goos>_<goarch>`,
// the shape of the archives' `yolo-jail_<version>_<os>_<arch>` (BP-D8).
func AssetName(name, version, platform string) string {
	goos, goarch, _ := strings.Cut(platform, "/")
	return name + "_" + version + "_" + goos + "_" + goarch
}

// AssetURL is where the tag v<version>'s release serves AssetName.
func AssetURL(name, version, platform string) string {
	return ReleaseDownloadBase + "v" + version + "/" + AssetName(name, version, platform)
}

// ParseAssetURL reads a URL of AssetURL's form back into its parts. ok is false for any URL of
// another form, including one whose tag and file name name different versions.
func ParseAssetURL(u string) (name, version, platform string, ok bool) {
	rest, found := strings.CutPrefix(u, ReleaseDownloadBase+"v")
	if !found {
		return "", "", "", false
	}
	version, file, found := strings.Cut(rest, "/")
	if !found || !versionRE.MatchString(version) || strings.Contains(file, "/") {
		return "", "", "", false
	}
	i := strings.LastIndexByte(file, '_')
	if i < 0 {
		return "", "", "", false
	}
	goarch := file[i+1:]
	j := strings.LastIndexByte(file[:i], '_')
	if j < 0 {
		return "", "", "", false
	}
	goos := file[j+1 : i]
	name, found = strings.CutSuffix(file[:j], "_"+version)
	if !found || name == "" || goos == "" || goarch == "" {
		return "", "", "", false
	}
	return name, version, goos + "/" + goarch, true
}

// ReleasePlatforms reads the platforms the release ships `yolo` to out of the goreleaser config:
// the `yolo` build's goos × goarch, less its `ignore` pairs, sorted as "<goos>/<goarch>".
//
// It reads the config rather than a copy of it, so a platform the release drops fails the census
// instead of leaving an official binary pinned for a platform no release builds (§9's
// "supported, missing"). It refuses what it does not model — a build that leaves goos or goarch
// to goreleaser's defaults, `targets`, an `ignore` keyed by anything but goos and goarch —
// rather than guess at them.
func ReleasePlatforms(goreleaserYAML []byte) ([]string, error) {
	var cfg struct {
		Builds []struct {
			ID      string              `yaml:"id"`
			GOOS    []string            `yaml:"goos"`
			GOARCH  []string            `yaml:"goarch"`
			Targets []string            `yaml:"targets"`
			Ignore  []map[string]string `yaml:"ignore"`
		} `yaml:"builds"`
	}
	if err := yaml.Unmarshal(goreleaserYAML, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", GoreleaserConfig, err)
	}
	for _, b := range cfg.Builds {
		if b.ID != ReleaseBuildID {
			continue
		}
		if len(b.Targets) > 0 {
			return nil, fmt.Errorf("%s: the %q build names `targets`, which the release matrix "+
				"does not read — list goos and goarch instead, or teach "+
				"internal/releasematrix to read targets", GoreleaserConfig, ReleaseBuildID)
		}
		if len(b.GOOS) == 0 || len(b.GOARCH) == 0 {
			return nil, fmt.Errorf("%s: the %q build must list goos and goarch explicitly — "+
				"the release matrix does not model goreleaser's defaults", GoreleaserConfig,
				ReleaseBuildID)
		}
		ignored := map[string]bool{}
		for _, ig := range b.Ignore {
			for k := range ig {
				if k != "goos" && k != "goarch" {
					return nil, fmt.Errorf("%s: the %q build ignores by %q, which the release "+
						"matrix does not read", GoreleaserConfig, ReleaseBuildID, k)
				}
			}
			ignored[ig["goos"]+"/"+ig["goarch"]] = true
		}
		var out []string
		for _, goos := range b.GOOS {
			for _, goarch := range b.GOARCH {
				p := goos + "/" + goarch
				if !ignored[p] && !ignored[goos+"/"] && !ignored["/"+goarch] {
					out = append(out, p)
				}
			}
		}
		sort.Strings(out)
		return out, nil
	}
	return nil, fmt.Errorf("%s has no build with id %q, whose platforms are the release matrix",
		GoreleaserConfig, ReleaseBuildID)
}

// Platforms is BP-D7 for one binary: the platforms it must have a build for. For each release
// platform the loophole's `platforms` admits, each reference to the binary needs the build for
// where it runs there — that platform for a `{binary:}` reference, linux on its architecture
// for a `{jail_binary:}` one (BP-D3) — so no darwin jail build is ever wanted. Sorted.
//
// It is the union of what BinariesNeeded answers on every release platform, which is what makes
// "exactly" hold: a machine the release ships yolo to asks the cache for these builds and no
// others.
func Platforms(m *loopholedecl.Manifest, binary string, release []string) []string {
	set := map[string]bool{}
	for _, p := range release {
		goos, goarch, _ := strings.Cut(p, "/")
		if !m.SupportsPlatform(goos, goarch) {
			continue
		}
		for _, n := range m.BinariesNeeded(goos, goarch) {
			if n.Binary == binary {
				set[n.Platform] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
