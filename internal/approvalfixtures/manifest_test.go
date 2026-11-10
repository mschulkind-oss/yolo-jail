package approvalfixtures

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// capturesDir holds one directory per platform, each with the manifest its capture runner
// wrote (scripts/approval-gallery/capture-linux.sh; mac-capture-runner.md for macOS).
var capturesDir = filepath.Join(filepath.Dir(galleryFile), "captures")

// loadManifests reads every captures/<platform>/manifest.json, in platform order.
func loadManifests(t *testing.T) []Manifest {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(capturesDir, "*", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var ms []Manifest
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseManifest(b)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if dir := filepath.Base(filepath.Dir(p)); dir != m.Platform {
			t.Fatalf("%s says platform %q but sits in %s/", p, m.Platform, dir)
		}
		ms = append(ms, m)
	}
	return ms
}

// foldedFixtures is the committed fixtures with every manifest folded in. It fails on a
// manifest naming a fixture that does not exist or one that files no request, and on a fold
// that breaks a rule.
func foldedFixtures(t *testing.T) []Fixture {
	t.Helper()
	fs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, f := range fs {
		byID[f.ID] = i
	}
	for _, m := range loadManifests(t) {
		for id := range m.Captures {
			i, ok := byID[id]
			if !ok {
				t.Fatalf("the %s manifest names fixture %q, which does not exist", m.Platform, id)
			}
			if fs[i].Prompt == "none" {
				t.Fatalf("the %s manifest captures %q, which files no request and so shows no notification", m.Platform, id)
			}
			fs[i] = Fold(fs[i], m)
			if err := fs[i].Validate(); err != nil {
				t.Fatalf("%s after folding the %s manifest: %v", id, m.Platform, err)
			}
		}
	}
	return fs
}

// Every capture a manifest lists is in its fixture's evidence. The capture runners write
// only manifests; YOLO_UPDATE_APPROVAL_FIXTURES=1 folds them into the fixture files.
func TestManifestsAreFolded(t *testing.T) {
	committed, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// The other direction: every native capture a fixture claims is one a committed manifest
	// lists, so a hand-added entry, or one left over from an earlier run, cannot pass as native.
	listed := map[string]bool{}
	for _, m := range loadManifests(t) {
		for id, caps := range m.Captures {
			for _, c := range caps {
				listed[m.Platform+"\x00"+id+"\x00"+c.Renderer+"\x00"+c.File] = true
			}
		}
	}
	for _, f := range committed {
		for _, e := range f.Evidence {
			if e.Kind == "native-capture" && !listed[e.Platform+"\x00"+f.ID+"\x00"+e.Renderer+"\x00"+e.File] {
				t.Errorf("fixtures/%s.json claims a native %s capture %s that no committed manifest lists; list it as missing, or re-run the capture", f.ID, e.Platform, e.File)
			}
		}
	}
	folded := foldedFixtures(t)
	update := os.Getenv("YOLO_UPDATE_APPROVAL_FIXTURES") == "1"
	for i, f := range folded {
		if reflect.DeepEqual(f, committed[i]) {
			continue
		}
		if !update {
			t.Errorf("fixtures/%s.json lacks captures its manifest lists; fold them: YOLO_UPDATE_APPROVAL_FIXTURES=1 go test ./internal/approvalfixtures", f.ID)
			continue
		}
		b, err := Encode(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("fixtures", f.ID+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Folding replaces the platform's earlier entry for the same renderer, keeps every other
// entry, and is idempotent; a provenance number becomes a string.
func TestFoldReplacesOnlyItsPlatformAndRenderer(t *testing.T) {
	f := valid()
	m, err := ParseManifest([]byte(`{"schema":1,"platform":"linux","captures":{"` + f.ID + `":[{"file":"captures/linux/x.png","renderer":"r","tested":"visual","provenance":{"os":"o","daemon":"d","daemon_version":"1","theme":"t","scale":1,"captured_at":"c","commit":"abc"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	other := Evidence{Platform: "linux", Kind: "missing", Renderer: "another daemon", Note: "not yet"}
	f.Evidence = append(f.Evidence, other)
	before := len(f.Evidence)
	g := Fold(f, m)
	if len(g.Evidence) != before {
		t.Fatalf("fold changed the entry count: %d -> %d", before, len(g.Evidence))
	}
	if !reflect.DeepEqual(g.Evidence[len(g.Evidence)-1], other) {
		t.Fatalf("another renderer's entry was not kept: %+v", g.Evidence)
	}
	native := 0
	for _, e := range g.Evidence {
		if e.Platform == "linux" && e.Renderer == "r" {
			if e.Kind != "native-capture" || e.Provenance["scale"] != "1" {
				t.Fatalf("unfolded entry left behind: %+v", e)
			}
			native++
		}
	}
	if native != 1 {
		t.Fatalf("want one linux capture for renderer r, got %d", native)
	}
	if !reflect.DeepEqual(Fold(g, m), g) {
		t.Fatal("folding twice changed the fixture")
	}
}

func TestParseManifestRefuses(t *testing.T) {
	for name, b := range map[string]string{
		"unknown field": `{"schema":1,"platform":"linux","captures":{},"extra":1}`,
		"schema":        `{"schema":2,"platform":"linux","captures":{}}`,
		"browser":       `{"schema":1,"platform":"browser","captures":{}}`,
		"capture field": `{"schema":1,"platform":"linux","captures":{"a":[{"file":"f","bogus":1}]}}`,
	} {
		if _, err := ParseManifest([]byte(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Encode is the canonical form every writer uses: no HTML escaping, a trailing newline.
func TestEncodeIsCanonical(t *testing.T) {
	f := valid()
	f.Story = "a <b> & c"
	b, err := Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(b, []byte("}\n")) || !strings.Contains(string(b), "a <b> & c") {
		t.Fatalf("not canonical: %s", b)
	}
	g, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Encode(g)
	if err != nil || !bytes.Equal(b, b2) {
		t.Fatalf("encoding is not stable across a round trip: %v", err)
	}
}
