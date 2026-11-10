package approvalfixtures

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// galleryFile is the committed gallery, from this package's directory.
var galleryFile = filepath.Join("..", "..", filepath.FromSlash(GalleryPath))

// The committed gallery is what the committed fixtures generate, so a fixture edit that
// forgets the page fails here. YOLO_UPDATE_APPROVAL_FIXTURES=1 rewrites it.
func TestGalleryIsCurrent(t *testing.T) {
	fs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Gallery(fs)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("YOLO_UPDATE_APPROVAL_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(galleryFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(galleryFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(galleryFile)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s is stale (%v); regenerate it: YOLO_UPDATE_APPROVAL_FIXTURES=1 go test ./internal/approvalfixtures", GalleryPath, err)
	}
	// Every native capture the page shows exists beside it.
	for _, f := range fs {
		for _, e := range f.Evidence {
			if e.Kind == "native-capture" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(galleryFile), filepath.FromSlash(e.File))); err != nil {
					t.Errorf("%s: native capture %s: %v", f.ID, e.File, err)
				}
			}
		}
	}
}

// Request text is untrusted: markup in it reaches the page escaped, never as markup, and a
// missing capture is shown as missing, never as an image.
func TestGalleryEscapesRequestTextAndNeverFakesACapture(t *testing.T) {
	f := valid()
	f.Request = json.RawMessage(`{"schema":1,"id":"r-1","source":"git","workspace":"/home/dev/code/app","state":"pending","headline":"<script>alert(1)</script>","description":"<b>x</b>","command":"<img src=x onerror=alert(1)>"}`)
	page, err := Gallery([]Fixture{f})
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	for _, bad := range []string{"<script>alert", "<b>x</b>", "<img src=x"} {
		if strings.Contains(s, bad) {
			t.Fatalf("unescaped %q in the page", bad)
		}
	}
	if strings.Contains(s, "<img") {
		t.Fatal("a fixture with only missing evidence rendered an image")
	}
	if strings.Count(s, "No capture.") != 2 {
		t.Fatal("each missing platform is shown as missing")
	}
}

// A preview tile says it is a preview, so it cannot be mistaken for a native capture.
func TestGalleryLabelsPreviews(t *testing.T) {
	f := valid()
	f.Evidence = append(f.Evidence, Evidence{Platform: "browser", Kind: "preview", Renderer: "r", File: "index.html", Tested: "visual"})
	page, err := Gallery([]Fixture{f})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "PREVIEW (HTML, not native)") {
		t.Fatal("the preview tile is unlabeled")
	}
}
