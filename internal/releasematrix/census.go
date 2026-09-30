package releasematrix

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// Kind is which rule a Problem breaks.
type Kind int

const (
	// KindPlatforms is a build set that is not BP-D7's.
	KindPlatforms Kind = iota
	// KindURL is a url that is not the release file BP-D8 names.
	KindURL
	// KindProgram is a program that is not a main package at cmd/<name>, or that links the
	// packs embed.
	KindProgram
	// KindDigest is a sha256 the tree does not build. The census never reports one, since it
	// builds nothing; the pin tool does.
	KindDigest
)

// Problem is one way an official binary fails the release matrix.
type Problem struct {
	Kind Kind
	// Manifest is the manifest's path in the checkout.
	Manifest string
	// Binary is the `binaries` key.
	Binary string
	// Platform is the build the problem is about, "" when it is about the binary as a whole.
	Platform string
	// Text says what is wrong and what fixes it.
	Text string
}

func (p Problem) String() string {
	s := p.Manifest + ": binary " + p.Binary
	if p.Platform != "" {
		s += " (" + p.Platform + ")"
	}
	return s + ": " + p.Text
}

// Census is every way the official binaries in entries fail the release matrix, REBUILDING
// NOTHING (docs/design/broker-as-a-pack.md §14.4, step 2): each binary's builds are exactly
// BP-D7's platforms for the release platforms given, each build's url has BP-D8's form, and
// each program is a main package at cmd/<name> that does not link the packs embed on any
// platform it is built for. root is the checkout.
//
// It is the static half of the pin tool's `check`, and the short suite runs it over the embed,
// so a platform outside the matrix fails `just check-ci` rather than the next release. What it
// cannot see without building — that each sha256 is the digest the tree builds, and that each
// url names the version being released — is the tool's.
func Census(root string, entries []Entry, release []string) []Problem {
	var out []Problem
	for _, e := range entries {
		for _, b := range e.Manifest.Binaries {
			out = append(out, censusBinary(root, e, b, release)...)
		}
	}
	return out
}

func censusBinary(root string, e Entry, b loopholedecl.Binary, release []string) []Problem {
	var out []Problem
	add := func(kind Kind, platform, text string) {
		out = append(out, Problem{Kind: kind, Manifest: e.Path, Binary: b.Name, Platform: platform,
			Text: text})
	}

	want := Platforms(e.Manifest, b.Name, release)
	have := b.BuildPlatforms()
	if len(want) == 0 {
		add(KindPlatforms, "", "no platform the release ships yolo to ("+strings.Join(release, ", ")+
			") runs it under the loophole's `platforms`, so no release would build it — "+
			"widen `platforms`, or drop the binary")
	}
	for _, p := range minus(want, have) {
		add(KindPlatforms, p, "has no build here, and BP-D7 wants one: "+whyWanted(e.Manifest, b.Name, p)+
			" — add the build, and let `just pin-pack-binaries <version>` fill it in")
	}
	for _, p := range minus(have, want) {
		add(KindPlatforms, p, "declares a build no release produces — the platforms are exactly those of "+
			"goreleaser's `"+ReleaseBuildID+"` build ("+strings.Join(release, ", ")+
			") that the loophole's `platforms` admits, where it runs; drop the build")
	}

	for _, bb := range b.Builds {
		name, version, platform, ok := ParseAssetURL(bb.URL)
		if !ok || name != b.Name || platform != bb.Platform {
			v := version
			if !ok {
				v = "<version>"
			}
			add(KindURL, bb.Platform, "url "+bb.URL+" is not the release file BP-D8 names: want "+
				AssetURL(b.Name, v, bb.Platform)+" — `just pin-pack-binaries <version>` writes it")
		}
	}

	platforms := union(want, have)
	for _, p := range platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		chain, err := EmbedChain(root, b.Name, goos, goarch)
		if err != nil {
			add(KindProgram, p, err.Error())
			break
		}
		if chain != nil {
			add(KindProgram, p, ProgramDir(b.Name)+" links the packs embed ("+shortChain(chain)+"), so every "+
				"build of it carries the manifest that pins it and no digest could match — "+
				"move what it needs out of the import graph that reaches "+EmbedPackage)
			break
		}
	}
	return out
}

// whyWanted says why BP-D7 wants a build of the binary for platform p, for a message: which
// machines the release ships yolo to would run it there, and on which side.
func whyWanted(m *loopholedecl.Manifest, binary, p string) string {
	host, jail := false, false
	for _, name := range m.BinaryRefs.Host {
		host = host || name == binary
	}
	for _, name := range m.BinaryRefs.Jail {
		jail = jail || name == binary
	}
	goos, goarch, _ := strings.Cut(p, "/")
	onHost := "the release ships yolo to " + p + ", where the loophole runs it on the host"
	inJail := "the release ships yolo to " + goarch + " machines, whose jails run it as " + p
	switch {
	case host && jail && goos == "linux":
		return onHost + ", and to " + goarch + " machines, whose jails run it as " + p
	case jail && goos == "linux":
		return inJail
	default:
		return onHost
	}
}

func shortChain(chain []string) string {
	parts := make([]string, len(chain))
	for i, c := range chain {
		parts[i] = strings.TrimPrefix(strings.TrimPrefix(c, ModulePath), "/")
	}
	return strings.Join(parts, " → ")
}

func minus(a, b []string) []string {
	inB := map[string]bool{}
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}

func union(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range append(append([]string{}, a...), b...) {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
