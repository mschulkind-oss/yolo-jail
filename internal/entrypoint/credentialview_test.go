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

// TestUnderTheBridgeTheSharedCredentialsHookNeverWritesTheFileClaudeOpens pins the hook's
// decision under CL-D22's bridge (linkSharedCredential's doc): with Claude's credential store
// pointed at the shared dir, the file there is the one Claude opens and writes, so the hook may
// only ever fill an ABSENT or EMPTY one, and must leave a populated one, and Claude's own lock
// files beside it, exactly as they were. Driven through runPackHook with claude's real hook and
// the environment a bridged launch hands the jail.
func TestUnderTheBridgeTheSharedCredentialsHookNeverWritesTheFileClaudeOpens(t *testing.T) {
	p, hook := sharedCredsHook(t, "claude")
	const opened = `{"claudeAiOauth":{"accessToken":"AT-shared","refreshToken":"RT-shared"},"mcpOAuth":{"s":{}}}`
	const private = `{"claudeAiOauth":{"accessToken":"AT-private","refreshToken":"RT-private"}}`
	empty := ""
	populated := opened
	for _, tc := range []struct {
		name       string
		shared     *string // nil: absent
		wantShared string
	}{
		{"shared populated: Claude's file is left alone", &populated, opened},
		{"shared empty: the workspace's login is carried into it", &empty, private},
		{"shared absent: the workspace's login is carried into it", nil, private},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			sharedDir := filepath.Join(home, filepath.FromSlash(hook.SharedDir))
			shared := filepath.Join(sharedDir, filepath.Base(hook.File))
			e := &Env{Home: home, Workspace: t.TempDir(), Vars: map[string]string{
				claudeview.SecureStorageEnv: sharedDir,
			}}
			link := filepath.Join(home, filepath.FromSlash(hook.File))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			// Claude's own locks, which the bridge puts beside the file (proper-lockfile takes
			// each by mkdir).
			locks := []string{".oauth_refresh.lock", claudeview.StorageLockDir}
			for _, l := range locks {
				if err := os.MkdirAll(filepath.Join(sharedDir, l), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.shared != nil {
				if err := os.WriteFile(shared, []byte(*tc.shared), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The private file a pre-bridge Claude's first save renamed over the link.
			if err := os.WriteFile(link, []byte(private), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := runPackHook(e, p, hook); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(shared)
			if err != nil {
				t.Fatalf("the shared file Claude opens is gone: %v", err)
			}
			if string(got) != tc.wantShared {
				t.Errorf("shared file = %q, want %q", got, tc.wantShared)
			}
			for _, l := range locks {
				if fi, err := os.Stat(filepath.Join(sharedDir, l)); err != nil || !fi.IsDir() {
					t.Errorf("the hook disturbed Claude's lock %s beside the file: %v", l, err)
				}
			}
			if target, err := os.Readlink(link); err != nil || target != claudeview.LegacyLinkTarget {
				t.Errorf("the old path is %q (%v), want the link %q to the one file",
					target, err, claudeview.LegacyLinkTarget)
			}
		})
	}
}
