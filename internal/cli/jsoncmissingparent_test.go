package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

const jsoncMissingParentText = "{\n  // keep this comment\n  \"packs\": [\"claude\", \"zai\"]\n}\n"

// assertParsesWithComment fails unless the JSONC file still parses and kept its comment.
func assertParsesWithComment(t *testing.T, path string) string {
	t.Helper()
	got := readFile(t, path)
	if !strings.Contains(got, "// keep this comment") {
		t.Fatalf("%s lost its comment:\n%s", path, got)
	}
	if _, err := configdoc.Parse([]byte(got), configdoc.JSONC); err != nil {
		t.Fatalf("%s no longer parses: %v\n%s", path, err, got)
	}
	return got
}

func TestRequestVerbOnJSONCWorkspaceMissingParentsCreatesThem(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	// Replace the TOML workspace with a legacy JSONC one that holds no requests/brokered object.
	if err := os.Remove(fx.project); err != nil {
		t.Fatal(err)
	}
	ws := testconfig.WorkspaceFormat(t, configdoc.JSONC, "{\n  // keep this comment\n}\n")
	withConfigEditCWD(t, ws.Root)

	rc, out, errs := captureStd(t, func() int { return registry["request"]([]string{"request", "github", "runs", "nightly.yml"}) })
	if strings.Contains(errs, "parent does not exist") {
		t.Fatalf("the verb dead-ended on a missing parent: rc=%d stderr=%q", rc, errs)
	}
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := assertParsesWithComment(t, ws.Path)
	if !strings.Contains(got, "nightly.yml") {
		t.Fatalf("request not written:\n%s", got)
	}
	if errs := launchErrors(t, ws.Root); len(errs) != 0 {
		t.Fatalf("launch validation rejects the written file: %v\n%s", errs, got)
	}
}

func TestActiveProviderVerbJSONCInsertionIsIndented(t *testing.T) {
	dir, _ := legacyUserFixture(t, jsoncMissingParentText, false)
	rc, out, errs := runActiveProviderVerb(t, "claude", "zai")
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := assertParsesWithComment(t, filepath.Join(dir, "config.jsonc"))
	want := "  \"packs\": [\"claude\", \"zai\"],\n  \"active_provider\": {\n    \"claude\": \"zai\"\n  }\n}"
	if !strings.Contains(got, want) {
		t.Fatalf("inserted object is mis-indented, want it to contain:\n%s\ngot:\n%s", want, got)
	}
}
