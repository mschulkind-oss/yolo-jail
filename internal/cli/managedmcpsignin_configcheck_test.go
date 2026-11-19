package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestManagedMCPBundleConfigCheckRejectsInvalidSelectedDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		license      bool
		want         string
	}{{"complete", "claude-plugin", true, ""}, {"unknown format", "unknown", true, "format"}, {"missing license", "claude-plugin", false, "license"}} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
			pack := filepath.Join(workspace.Root, "fixture-pack")
			if err := os.MkdirAll(filepath.Join(pack, "bundle/.claude-plugin"), 0700); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`name="fixture"
[[mcp_sign_in_bundle]]
bundle="bundle"
format=%q
origin="https://example.invalid/repo"
revision="0123456789abcdef0123456789abcdef01234567"
license="bundle/LICENSE"
`, tc.format)
			for name, body := range map[string]string{"pack.toml": body, "bundle/.claude-plugin/plugin.json": `{"name":"original"}`} {
				if err := os.WriteFile(filepath.Join(pack, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.license {
				if err := os.WriteFile(filepath.Join(pack, "bundle/LICENSE"), []byte("MIT License"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			user := testconfig.UserFormat(t, configdoc.TOML, fmt.Sprintf("packs=['fixture/fixture-pack']\n[pack_sources]\nfixture=%q\n", filepath.Dir(pack)))
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			withConfigEditCWD(t, workspace.Root)
			var out, errs bytes.Buffer
			rc := configRunW([]string{"check"}, &out, &errs)
			if tc.want == "" {
				if rc != 0 {
					t.Fatalf("accepted selected bundle refused: rc=%d %s %s", rc, out.String(), errs.String())
				}
			} else if rc != 1 || !strings.Contains(out.String()+errs.String(), tc.want) {
				t.Fatalf("invalid selected bundle: rc=%d %s %s", rc, out.String(), errs.String())
			}
		})
	}
}
