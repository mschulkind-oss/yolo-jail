package packload

import (
	"io/fs"
	"strings"
	"testing"
)

// Check the catalog callers consume and every original byte that binaries ship,
// including hidden plugin manifests, rather than just the embed directive.
func TestEmbeddedPacksContainNoSlackIntegration(t *testing.T) {
	shipped := Embedded()
	if len(shipped) == 0 {
		t.Fatal("embedded catalog is empty")
	}
	for _, pack := range shipped {
		if strings.Contains(strings.ToLower(pack.Name), "slack") {
			t.Errorf("unauthorized shipped integration: %s", pack.Name)
		}
	}
	forbidden := []string{
		"slack-mcp", "slack-skills-plugin", "slack.com", "docs.slack.dev",
		"1601185624273.8899143856786", "5b8802105740b5c045c97247fded2e73b205e95e",
	}
	err := fs.WalkDir(EmbeddedFS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(EmbeddedFS(), path)
		if err != nil {
			return err
		}
		for _, marker := range forbidden {
			if strings.Contains(path, marker) || strings.Contains(string(body), marker) {
				t.Errorf("embedded %s contains unauthorized Slack integration marker %q", path, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
