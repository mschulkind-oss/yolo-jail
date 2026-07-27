package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
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
		for _, h := range p.Decl.Hooks {
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
	for _, h := range p.Decl.Hooks {
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
