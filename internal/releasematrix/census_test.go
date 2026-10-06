package releasematrix

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// writeFile writes rel under root, making its directories.
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

// fixtureTree is a checkout with a packs embed package and three programs: `toold`, which is
// clean; `linker`, which reaches the embed through an internal package on every platform; and
// `macker`, which reaches it only in its darwin file.
func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "packs/embed.go", "package packs\n")
	writeFile(t, root, "internal/lib/lib.go", "package lib\n\nimport _ \""+EmbedPackage+"\"\n")
	writeFile(t, root, "internal/clean/clean.go", "package clean\n")
	writeFile(t, root, "cmd/toold/main.go", "package main\n\nimport _ \""+ModulePath+"/internal/clean\"\n\nfunc main() {}\n")
	// A test file's imports are never linked, so this one must not count.
	writeFile(t, root, "cmd/toold/main_test.go", "package main\n\nimport _ \""+EmbedPackage+"\"\n")
	writeFile(t, root, "cmd/linker/main.go", "package main\n\nimport _ \""+ModulePath+"/internal/lib\"\n\nfunc main() {}\n")
	writeFile(t, root, "cmd/macker/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, root, "cmd/macker/mac_darwin.go", "package main\n\nimport _ \""+EmbedPackage+"\"\n")
	writeFile(t, root, "cmd/notmain/lib.go", "package notmain\n")
	writeFile(t, root, "cmd/unixonly/main_linux.go", "package main\n\nfunc main() {}\n")
	writeFile(t, root, "cmd/unixonly/main_darwin.go", "package main\n\nfunc main() {}\n")
	return root
}

func TestEmbedChainFollowsTheImportsThatAreLinked(t *testing.T) {
	root := fixtureTree(t)
	chain, err := EmbedChain(root, "toold", "linux", "amd64")
	if err != nil || chain != nil {
		t.Errorf("toold: chain %v, err %v; want neither (its test file's import is not linked)", chain, err)
	}
	chain, err = EmbedChain(root, "linker", "linux", "amd64")
	want := []string{ModulePath + "/cmd/linker", ModulePath + "/internal/lib", EmbedPackage}
	if err != nil || !reflect.DeepEqual(chain, want) {
		t.Errorf("linker: chain %v, err %v; want %v", chain, err, want)
	}
	// The platform's build constraints decide what is linked.
	if chain, err := EmbedChain(root, "macker", "linux", "arm64"); err != nil || chain != nil {
		t.Errorf("macker on linux: chain %v, err %v; want none", chain, err)
	}
	if chain, err := EmbedChain(root, "macker", "darwin", "arm64"); err != nil || len(chain) != 2 {
		t.Errorf("macker on darwin: chain %v, err %v; want cmd/macker → packs", chain, err)
	}
	for name, want := range map[string]string{"nope": "there is no cmd/nope", "notmain": "not a program"} {
		if _, err := EmbedChain(root, name, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", name, err, want)
		}
	}
}

// §14.2 measured which of this tree's programs link the embed; the walk must agree with the
// measurement, or it is not asking the question the toolchain answers.
func TestEmbedChainAgreesWithTheMeasuredTree(t *testing.T) {
	root := filepath.Join("..", "..")
	for name, links := range map[string]bool{
		"yolo": true, "yolo-entrypoint": true, "yolo-jaild": true,
		"yolo-ps": false, "yolo-serial": false, "yolo-journalctl": false, "yolo-cglimit": false,
	} {
		chain, err := EmbedChain(root, name, "linux", "amd64")
		if err != nil {
			t.Errorf("cmd/%s: %v", name, err)
			continue
		}
		if (chain != nil) != links {
			t.Errorf("cmd/%s: chain %v, want linked=%v", name, chain, links)
		}
	}
}

func entryFor(t *testing.T, pack, loophole string, data map[string]any) Entry {
	t.Helper()
	data["name"] = loophole
	return Entry{Pack: pack, Loophole: loophole,
		Path:     path.Join("packs", pack, "loopholes", loophole, "manifest.jsonc"),
		Manifest: decode(t, loophole, data)}
}

// renamed is a fixture manifest whose binary is `name` rather than `toold`.
func renamed(m map[string]any, name string) map[string]any {
	raw, _ := json.Marshal(m)
	s := strings.ReplaceAll(string(raw), "toold", name)
	var out map[string]any
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func TestCensusPassesACleanBinary(t *testing.T) {
	root := fixtureTree(t)
	e := entryFor(t, "p", "tool", fixtureManifest(true, false, nil, allFour...))
	if got := Census(root, []Entry{e}, allFour); len(got) != 0 {
		t.Errorf("Census = %v, want nothing", got)
	}
}

// Each refusal the census exists for, from one manifest per defect.
func TestCensusRefusesEachWayOffTheMatrix(t *testing.T) {
	root := fixtureTree(t)
	badURL := fixtureManifest(false, true, nil, "linux/amd64", "linux/arm64")
	badURL["binaries"].(map[string]any)["toold"].(map[string]any)["linux/arm64"] =
		map[string]any{"url": "https://example.test/toold", "sha256": strings.Repeat("0", 64)}
	crossed := fixtureManifest(false, true, nil, "linux/amd64", "linux/arm64")
	crossed["binaries"].(map[string]any)["toold"].(map[string]any)["linux/arm64"] = buildOf("toold", "1.2.3", "linux/amd64")

	for _, tc := range []struct {
		name     string
		data     map[string]any
		platform string
		want     string
		kind     Kind
	}{
		{"a missing platform", fixtureManifest(true, false, nil, "linux/amd64", "linux/arm64", "darwin/amd64"),
			"darwin/arm64", "has no build here", KindPlatforms},
		{"a darwin jail build", fixtureManifest(false, true, nil, "linux/amd64", "linux/arm64", "darwin/arm64"),
			"darwin/arm64", "declares a build no release produces", KindPlatforms},
		{"a platform the release lacks", fixtureManifest(true, false, nil, append([]string{"windows/amd64"}, allFour...)...),
			"windows/amd64", "declares a build no release produces", KindPlatforms},
		{"a platform `platforms` excludes", fixtureManifest(true, false, []string{"linux"}, allFour...),
			"darwin/amd64", "declares a build no release produces", KindPlatforms},
		{"nothing the release runs it on", fixtureManifest(true, false, []string{"windows"}, "windows/amd64"),
			"", "no platform the release ships yolo to", KindPlatforms},
		{"a url of another form", badURL, "linux/arm64", "is not the release file BP-D8 names", KindURL},
		{"a url naming another platform's file", crossed, "linux/arm64", "is not the release file BP-D8 names", KindURL},
		{"no program", renamed(fixtureManifest(true, false, nil, allFour...), "nope"), "darwin/amd64", "there is no cmd/nope", KindProgram},
		{"a program that links the embed", renamed(fixtureManifest(false, true, nil, "linux/amd64", "linux/arm64"), "linker"),
			"linux/amd64", "links the packs embed (cmd/linker → internal/lib → packs)", KindProgram},
		{"a program that links it on one platform", renamed(fixtureManifest(true, false, nil, allFour...), "macker"),
			"darwin/amd64", "cmd/macker links the packs embed", KindProgram},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := entryFor(t, "p", "tool", tc.data)
			got := Census(root, []Entry{e}, allFour)
			for _, p := range got {
				if p.Platform == tc.platform && strings.Contains(p.Text, tc.want) && p.Kind == tc.kind {
					if !strings.HasPrefix(p.String(), "packs/p/loopholes/tool/manifest.jsonc: binary ") {
						t.Errorf("the problem does not name its manifest and binary: %s", p)
					}
					return
				}
			}
			t.Errorf("Census = %v, want a problem of kind %d on %q naming %q", got, tc.kind,
				tc.platform, tc.want)
		})
	}
}

// The program is judged where the release builds it: a declared build no release produces is a
// platform refusal, and the program not building there is no second one.
func TestCensusJudgesTheProgramOnlyWhereTheReleaseBuildsIt(t *testing.T) {
	root := fixtureTree(t)
	data := renamed(fixtureManifest(true, false, nil, append([]string{"windows/amd64"}, allFour...)...), "unixonly")
	got := Census(root, []Entry{entryFor(t, "p", "tool", data)}, allFour)
	if len(got) != 1 || got[0].Kind != KindPlatforms || got[0].Platform != "windows/amd64" {
		t.Errorf("Census = %v, want only the windows/amd64 build refused", got)
	}
}

func TestManifestsReadsEveryLoopholeOfATree(t *testing.T) {
	withBin, _ := json.Marshal(renamed(fixtureManifest(true, false, nil, allFour...), "toold"))
	var m map[string]any
	_ = json.Unmarshal(withBin, &m)
	m["name"] = "b"
	withBin, _ = json.Marshal(m)
	fsys := fstest.MapFS{
		"agent/pack.json":                            {Data: []byte("{}")},
		"one/loopholes/a/manifest.jsonc":             {Data: []byte(`{"name": "a"} // a comment`)},
		"two/loopholes/b/manifest.jsonc":             {Data: withBin},
		"two/loopholes/README.md":                    {Data: []byte("not a loophole")},
		"two/loopholes/b/bin/something-it-ships.txt": {Data: []byte("x")},
	}
	all, err := Manifests(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range all {
		paths = append(paths, e.Path)
	}
	want := []string{"packs/one/loopholes/a/manifest.jsonc", "packs/two/loopholes/b/manifest.jsonc"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("Manifests read %v, want %v", paths, want)
	}
	if w := WithBinaries(all); len(w) != 1 || w[0].Loophole != "b" || w[0].Pack != "two" {
		t.Errorf("WithBinaries = %+v, want only two/b", w)
	}

	fsys["three/loopholes/c/README.md"] = &fstest.MapFile{Data: []byte("no manifest")}
	if _, err := Manifests(fsys); err == nil || !strings.Contains(err.Error(), "packs/three/loopholes/c/manifest.jsonc") {
		t.Errorf("a loophole with no manifest: err %v, want it named", err)
	}
	delete(fsys, "three/loopholes/c/README.md")
	fsys["one/loopholes/a/manifest.jsonc"] = &fstest.MapFile{Data: []byte(`{"name": "a", "hots_daemon": {}}`)}
	if _, err := Manifests(fsys); err == nil || !strings.Contains(err.Error(), "hots_daemon") {
		t.Errorf("a manifest the strict decoder refuses: err %v, want it reported", err)
	}

	// The tolerant read, the seed's, skips both and reports each, and keeps what it could read.
	fsys["three/loopholes/c/README.md"] = &fstest.MapFile{Data: []byte("no manifest")}
	got, unread, err := ManifestsTolerant(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "packs/two/loopholes/b/manifest.jsonc" {
		t.Errorf("ManifestsTolerant kept %+v, want only two/b", got)
	}
	if len(unread) != 2 || !strings.Contains(unread[0].Error(), "hots_daemon") ||
		!strings.Contains(unread[1].Error(), "packs/three/loopholes/c/manifest.jsonc") {
		t.Errorf("ManifestsTolerant skipped %v, want one/a and three/c, in path order", unread)
	}
}
