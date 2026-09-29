package entrypoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
)

// TestTheSharedCredentialsHookLinksNothingOnAViewLaunch: a launch that delivers the Claude
// credential view hands the jail the resolved switch, and the claude pack's shared_credentials
// hook then leaves ~/.claude/.credentials.json to the host broker. A link there would put the
// machine's refresh token back in front of Claude (docs/design/claude-login-without-interception.md,
// CL-D10). Driven through runPackHook, the hook dispatch, with claude's real hook, so deleting the skip — or the
// switch reaching the hook — fails it.
func TestTheSharedCredentialsHookLinksNothingOnAViewLaunch(t *testing.T) {
	p, hook := sharedCredsHook(t, "claude")
	if hook.File != claudeview.ViewRel {
		t.Fatalf("claude's shared_credentials hook acts on %q, the view is at %q: the skip "+
			"would never match", hook.File, claudeview.ViewRel)
	}

	for _, tc := range []struct {
		name       string
		vars       map[string]string
		legacyLink bool
		wantLink   bool
	}{
		{"interception launch", map[string]string{}, false, true},
		{"view launch", map[string]string{claudeview.SwitchEnv: "1"}, false, false},
		{"view launch after an interception one", map[string]string{claudeview.SwitchEnv: "1"}, true, false},
		{"resolved off", map[string]string{claudeview.SwitchEnv: "0"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			e := &Env{Home: home, Workspace: t.TempDir(), Vars: tc.vars}
			link := filepath.Join(home, filepath.FromSlash(claudeview.ViewRel))
			if tc.legacyLink {
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(claudeview.LegacyLinkTarget, link); err != nil {
					t.Fatal(err)
				}
			}
			if err := runPackHook(e, p, hook); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Lstat(link)
			isLink := err == nil && fi.Mode()&os.ModeSymlink != 0
			if isLink != tc.wantLink {
				t.Errorf("link at %s = %v, want %v", claudeview.ViewRel, isLink, tc.wantLink)
			}
			if tc.wantLink {
				if target, _ := os.Readlink(link); target != claudeview.LegacyLinkTarget {
					t.Errorf("the hook wrote %q; claudeview.LegacyLinkTarget says %q, so a view "+
						"launch would not recognise yolo's own link", target, claudeview.LegacyLinkTarget)
				}
			}
		})
	}
}
