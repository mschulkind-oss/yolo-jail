package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func managedMCPTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		row := fmt.Sprintf("%v %04o", entry.Type(), info.Mode().Perm())
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			row += " " + target
		} else if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			row += " " + string(body)
		}
		snapshot[relative] = row
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestMCPAuthStatusThroughMainHasNoEffectsOnMissingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("YOLO_VERSION", "")
	writeUserConfig(t, `{"packs":[],"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`)
	before := managedMCPTreeSnapshot(t, home)
	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "status", "slack", "--json"}) })
	if rc != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if !reflect.DeepEqual(before, managedMCPTreeSnapshot(t, home)) {
		t.Fatal("status changed fixture state")
	}
	if strings.Contains(out+errs, "fixture-access") || strings.Contains(out+errs, "fixture-refresh") {
		t.Fatal("status leaked fixture token")
	}
}
