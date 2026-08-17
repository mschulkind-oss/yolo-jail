package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestHookSetsAgree pins the two hook-name lists together.
//
// The names are duplicated on purpose: packdecl.KnownHooks validates a manifest on the
// HOST (`yolo check`, pack staging), and having internal/packdecl import the entrypoint to
// learn the names would invert the dependency — packdecl is deliberately the leaf that both
// the host CLI and the in-jail entrypoint read.
//
// The failure this prevents is silent in the worst direction. Add a hook to packdecl's list
// without implementing it, and a pack declaring it VALIDATES on the host and then fails at
// boot — the config looked fine and the jail broke. Implement one without listing it and the
// reverse: the manifest is rejected for a hook that works.
func TestHookSetsAgree(t *testing.T) {
	implemented := map[string]bool{
		HookSharedCredentials: true,
		HookPerJailHistory:    true,
		HookClaudePlugins:     true,
	}
	declared := map[string]bool{}
	for _, name := range packdecl.KnownHooks {
		declared[name] = true
		if !implemented[name] {
			t.Errorf("packdecl.KnownHooks lists %q but no hook implements it — a pack "+
				"declaring it would validate on the host and fail at boot", name)
		}
	}
	for name := range implemented {
		if !declared[name] {
			t.Errorf("hook %q is implemented but not in packdecl.KnownHooks — a pack "+
				"declaring it would be rejected as unknown", name)
		}
	}
}

// TestEveryEmbeddedPackHookIsHonored: a hook a shipped pack requests must actually run.
// A typo here would be a claude jail that silently stops sharing credentials across
// workspaces, which surfaces as "I have to log in again in every jail" — a symptom with no
// obvious connection to a manifest key.
func TestEveryEmbeddedPackHookIsHonored(t *testing.T) {
	for _, name := range EmbeddedPackNames() {
		p, err := embeddedPack(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range p.Decl.HookContributions() {
			switch h.Name {
			case HookSharedCredentials, HookPerJailHistory, HookClaudePlugins:
			default:
				t.Errorf("pack %s requests unimplemented hook %q", name, h.Name)
			}
		}
	}
}

// TestSharedCredentialsHookRefusesAnUndeclaredSharedDir: the hook may only link into a
// directory the pack DECLARED in sharedDirs, because that declaration is the only thing
// making the machine-global tier visible to a user reading the pack. A hook that could name
// any path would reach cross-workspace state silently.
func TestSharedCredentialsHookRefusesAnUndeclaredSharedDir(t *testing.T) {
	p, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	err = e.linkSharedCredential(p, packdecl.Hook{
		Name: HookSharedCredentials, File: ".claude/.credentials.json",
		SharedDir: ".somewhere-else",
	})
	if err == nil {
		t.Fatal("want a refusal for a sharedDir the pack never declared")
	}
}

// TestSharedCredentialsHookLinksADeclaredDir is the positive case, using claude's real
// declaration — so the test breaks if the pack's sharedDirs and its hook stop agreeing.
func TestSharedCredentialsHookLinksADeclaredDir(t *testing.T) {
	p, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	var hook packdecl.Hook
	for _, h := range p.Decl.HookContributions() {
		if h.Name == HookSharedCredentials {
			hook = h
		}
	}
	if hook.Name == "" {
		t.Skip("claude pack no longer requests shared_credentials")
	}
	if err := e.linkSharedCredential(p, hook); err != nil {
		t.Fatalf("linking a declared shared dir: %v", err)
	}
}

// sharedCredsHook returns the named embedded pack's shared_credentials hook, failing the
// test if the pack stopped requesting one — a silent drop there is exactly the "I have to
// log in again in every jail" regression, so it must never degrade to a skip.
func sharedCredsHook(t *testing.T, pack string) (*packload.Pack, packdecl.Hook) {
	t.Helper()
	p, err := embeddedPack(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Decl.HookContributions() {
		if h.Name == HookSharedCredentials {
			return p, h
		}
	}
	t.Fatalf("%s pack no longer requests shared_credentials", pack)
	return nil, packdecl.Hook{}
}

// TestSharedCredentialsHookCopiesAFirstLoginIntoEmptyShared pins the COPY-IF-EMPTY branch of
// linkThroughShared, which is the only thing left preserving a first login.
//
// It needs its own test because the harvest is gone (eb12125). Until then there were two
// mechanisms carrying a locally-obtained credential out to the shared tier — the
// newest-expiresAt-wins merge and this copy — and the merge was the one that fired for claude.
// The merge's deletion collapsed the property onto this branch alone, and nothing pinned it:
// removing the copy entirely left `go test -short ./...` fully green (measured 2026-08-17),
// while the user-visible result is a fresh install where the very first `/login` is deleted at
// the next boot and every jail is logged out with no shared credential to fall back to.
//
// Both pre-login shapes of the shared file are exercised, because they reach the branch by
// different tests inside one condition: ABSENT (os.Stat errors) is a machine that has never run
// a jail, and EMPTY (Size()==0) is the placeholder `yolo run` touches so the bind mount has
// something to bind — the far more common one, and the one a `Size()`-blind rewrite would miss.
//
// The counterpart is TestSharedCredentialsHookLogsDiscardDecision below: shared POPULATED must
// still win. The two together are the whole rule; either alone permits a one-line inversion.
func TestSharedCredentialsHookCopiesAFirstLoginIntoEmptyShared(t *testing.T) {
	for _, tc := range []struct {
		name       string
		seedShared bool // write a zero-byte placeholder before the hook runs
	}{
		{"shared absent", false},
		{"shared empty placeholder", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, hook := sharedCredsHook(t, "claude")
			e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
			link := filepath.Join(e.Home, filepath.FromSlash(hook.File))
			sharedDir := filepath.Join(e.Home, filepath.FromSlash(hook.SharedDir))
			shared := filepath.Join(sharedDir, filepath.Base(hook.File))

			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.seedShared {
				if err := os.MkdirAll(sharedDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(shared, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The credential this jail's first `/login` just wrote. It is a REAL file rather
			// than the symlink because the tool rewrote the path (tmp+rename replaces a
			// symlink with a regular file); that is the only way this branch is reached.
			const firstLogin = `{"claudeAiOauth":{"accessToken":"AT","subscriptionType":"max"}}`
			if err := os.WriteFile(link, []byte(firstLogin), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := e.linkSharedCredential(p, hook); err != nil {
				t.Fatalf("linkSharedCredential: %v", err)
			}

			// THE ASSERTION: the login reached the shared tier. Reading through the link
			// rather than the shared path would pass even if the copy never happened and the
			// local file were simply left alone, so this reads the shared file directly.
			got, err := os.ReadFile(shared)
			if err != nil {
				t.Fatalf("shared credential file: %v — the first login was discarded", err)
			}
			if string(got) != firstLogin {
				t.Errorf("shared file = %q, want the first login %q", got, firstLogin)
			}
			// And the link is a symlink into the shared dir, so the NEXT boot takes the
			// already-symlinked fast path instead of copying again.
			target, err := os.Readlink(link)
			if err != nil {
				t.Fatalf("link is not a symlink: %v", err)
			}
			if resolved := filepath.Join(filepath.Dir(link), target); resolved != shared {
				t.Errorf("link -> %q resolves to %q, want the shared file %q", target, resolved, shared)
			}
			// The decision is logged, for the same reason the discard is: a credential that
			// moved tiers must be diagnosable after the fact.
			logData, err := os.ReadFile(filepath.Join(e.Home, sharedCredsLog))
			if err != nil {
				t.Fatalf("no shared-creds log written: %v", err)
			}
			if !strings.Contains(string(logData), "copied local credential into shared") {
				t.Errorf("log does not record the copy: %q", logData)
			}
		})
	}
}

// TestSharedCredentialsHookLogsDiscardDecision pins the agy defect's observable behavior:
// when the shared file is already populated (a prior login in another workspace) and this
// workspace holds a fresh real credential file, the hook must DISCARD the local file (shared
// always wins) and record that decision in the persistent ~/.yolo-shared-creds.log. Without
// the log, this is the "I logged in and it silently reverted next boot" case with no trace.
func TestSharedCredentialsHookLogsDiscardDecision(t *testing.T) {
	p, hook := sharedCredsHook(t, "agy")

	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	link := filepath.Join(e.Home, filepath.FromSlash(hook.File))
	sharedDir := filepath.Join(e.Home, filepath.FromSlash(hook.SharedDir))
	shared := filepath.Join(sharedDir, filepath.Base(hook.File))

	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("shared-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("fresh-local-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := e.linkSharedCredential(p, hook); err != nil {
		t.Fatalf("linkSharedCredential: %v", err)
	}

	// The shared file is untouched — the fresh local login was discarded.
	if got, err := os.ReadFile(shared); err != nil {
		t.Fatal(err)
	} else if string(got) != "shared-token" {
		t.Errorf("shared file = %q, want untouched %q", got, "shared-token")
	}

	// link is now a symlink into the shared dir.
	if target, err := os.Readlink(link); err != nil {
		t.Errorf("link is not a symlink: %v", err)
	} else if !strings.Contains(target, filepath.Base(hook.File)) {
		t.Errorf("link target = %q, want it to point at the shared file", target)
	}

	// The decision must be recorded in the persistent log.
	logData, err := os.ReadFile(filepath.Join(e.Home, ".yolo-shared-creds.log"))
	if err != nil {
		t.Fatalf("no shared-creds log written: %v", err)
	}
	if !strings.Contains(string(logData), "discarded local credential") {
		t.Errorf("log does not record the discard: %q", logData)
	}
}
