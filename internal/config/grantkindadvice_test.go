package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// grantkindadvice_test.go pins the refusal of a workspace grant whose kind the user config does
// not allow (AGENTS.md: "Advice names a verb, not a file to hand-edit"): it names the
// `yolo allow <source> on_request <capability>` verb, with the capability the source's manifest
// maps the kind to, never the legacy `brokered.<source>.grant_kinds` key. internal/cli's
// TestBrokerGrantHintsNameCommandsTheCLIRuns pins that the named command runs.

// gitReadableGrantResolver is gitGrantResolver with the git manifest's readable capabilities,
// as packs/git declares them.
var gitReadableGrantResolver = fenceResolver{known: map[string]LoopholeInfo{
	"git-broker": {Name: "git-broker", HasHostDaemon: true, Brokered: &loopholedecl.Brokered{
		Source: "git", RemoteHost: "*", Grants: []string{"create-branch", "branch", "tag"},
		ReadCapability: "fetch", KindCapabilities: map[string]string{
			"create-branch": "new_branches", "branch": "push", "tag": "push_tags"}}},
}}

func writeBrokeredUserTOML(t *testing.T, h brokeredHost, body string) {
	t.Helper()
	if err := os.Remove(h.userCfg); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.userCfg), paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The owner's case: a TOML user config and a legacy JSONC workspace holding a branch grant.
func TestGrantKindRefusalNamesTheAllowVerbToATOMLUser(t *testing.T) {
	for _, c := range []struct{ kind, grant, capability string }{
		{"branch", `{"kind": "branch", "repo": "github.com/o/r", "branch": "main"}`, "push"},
		{"create-branch", `{"kind": "create-branch", "repo": "github.com/o/r"}`, "new_branches"},
		{"tag", `{"kind": "tag", "repo": "github.com/o/r", "tag": "v*"}`, "push_tags"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			h := newBrokeredHost(t)
			writeBrokeredUserTOML(t, h, "[allow.git]\nalways = [\"fetch\"]\n")
			write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [`+c.grant+`]}}}`)
			errs, _ := h.validateWith(t, gitReadableGrantResolver)
			want := "config.brokered.git.grants[0].kind: your user config does not let a workspace hold a '" + c.kind +
				"' grant. Allow it with `yolo allow git on_request " + c.capability + "` on the host, then run " +
				"`yolo config apply` again; or remove this grant."
			if !strings.Contains(errs, want) {
				t.Fatalf("the refusal is not\n  %s\ngot:\n%s", want, errs)
			}
			if strings.Contains(errs, "grant_kinds") {
				t.Fatalf("the refusal names the legacy grant_kinds key:\n%s", errs)
			}
		})
	}
}

// Running the named verb clears the refusal: the capability it adds is the one the reader
// honors for the kind.
func TestGrantKindAllowedThroughTheAllowVerbPasses(t *testing.T) {
	h := newBrokeredHost(t)
	writeBrokeredUserTOML(t, h, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneBranchGrant)
	if errs, _ := h.validateWith(t, gitReadableGrantResolver); errs != "" {
		t.Fatalf("a branch grant allowed by `yolo allow git on_request push` was refused:\n%s", errs)
	}
}

// A user config still in legacy JSONC gets the same verb behind `yolo config migrate`, which
// the verb needs first (docs/reference/config/editing.md#the-commands).
func TestGrantKindRefusalNamesMigrateThenAllowToAJSONCUser(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneBranchGrant)
	errs, _ := h.validateWith(t, gitReadableGrantResolver)
	want := "your user config does not let a workspace hold a 'branch' grant. Convert your user config to TOML " +
		"with `yolo config migrate` on the host, then run `yolo allow git on_request push` there and " +
		"`yolo config apply` again; or remove this grant."
	if !strings.Contains(errs, want) {
		t.Fatalf("the refusal is not\n  %s\ngot:\n%s", want, errs)
	}
}

// A workspace read grant maps to no on_request capability: the readable spelling of read is the
// source's read capability for every workspace, `yolo allow git fetch`.
func TestReadGrantKindRefusalNamesTheReadAllowVerb(t *testing.T) {
	h := newBrokeredHost(t)
	writeBrokeredUserTOML(t, h, "packs = []\n")
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "github.com/o/r"}]}}}`)
	errs, _ := h.validateWith(t, gitReadableGrantResolver)
	want := "your user config does not let a workspace hold a 'read' grant. Let every workspace read its own " +
		"approved remotes with `yolo allow git fetch` on the host, then run `yolo config apply` again; or remove this grant."
	if !strings.Contains(errs, want) {
		t.Fatalf("the refusal is not\n  %s\ngot:\n%s", want, errs)
	}
}
