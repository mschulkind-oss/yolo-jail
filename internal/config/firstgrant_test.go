package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A first read grant over a workspace whose only approval record is a legacy one (repositories
// recorded, no grants key) is shown as added at the gate, never as "unchanged": the record was
// made while the broker could do nothing (BB-D110).
func TestAFirstGrantOverALegacyRecordIsShownAsAdded(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{}`)
	write(t, h.userCfg, `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`)
	if err := os.MkdirAll(filepath.Dir(ApprovalScopePath(h.ws)), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, ApprovalScopePath(h.ws), `{"git": ["git@github.com:o/r.git"]}`)
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(h.ws, []ScopeBroker{{Source: "git", Label: "git-broker", RemoteHost: "*"}}, read)
	r, err := ScopeChanges(h.ws, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(r.ScopeBlock, "\n")
	t.Logf("%s", block)
	if !r.ScopeChanged || strings.Contains(block, "unchanged") || !strings.Contains(block, "+ grant read") {
		t.Fatalf("a first grant is not shown as added:\n%s", block)
	}
}

// The same holds when the record names the read grant but no repository (a grant over nothing
// approved nothing): the grant is first held now, so it is added, not unchanged.
func TestAFirstGrantOverARecordOfAGrantWithNoScopeIsShownAsAdded(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{}`)
	write(t, h.userCfg, `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`)
	if err := os.MkdirAll(filepath.Dir(ApprovalScopePath(h.ws)), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, ApprovalScopePath(h.ws), `{"git.grants": ["read *"]}`)
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(h.ws, []ScopeBroker{{Source: "git", Label: "git-broker", RemoteHost: "*"}}, read)
	r, err := ScopeChanges(h.ws, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(r.ScopeBlock, "\n")
	t.Logf("%s", block)
	if !r.ScopeChanged || !strings.Contains(block, "+ grant read") || strings.Contains(block, "grant read *  unchanged") {
		t.Fatalf("a first grant is not shown as added:\n%s", block)
	}
	if !strings.Contains(strings.Join(r.ScopeCounts, ""), "grants: 1 added") {
		t.Fatalf("counts %q do not count the grant as added", r.ScopeCounts)
	}
}

// A legacy workspace entry is read from `brokered.<source>.repos` (WorkspaceRead.Entry), so a
// change to one names that authored key; a source whose workspace lists no entry has no authored
// key, and its next step stays the canonical repos table.
func TestALegacyEntryChangeNamesTheKeyItWasReadFrom(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	write(t, h.userCfg, `{"brokered": {"forge": {"grants": [{"kind": "read", "repo": "*"}]}}}`)
	broker := []ScopeBroker{{Source: "forge", Label: "forge-broker", RemoteHost: "github.com"}}
	scope := func(workspaceConfig string) (*ScopeCheck, ChangeReport) {
		t.Helper()
		write(t, filepath.Join(h.ws, WorkspaceConfigName), workspaceConfig)
		read, err := ReadWorkspaceForGate(h.ws)
		if err != nil {
			t.Fatal(err)
		}
		check := NewScopeCheck(h.ws, broker, read)
		r, err := ScopeChanges(h.ws, check)
		if err != nil {
			t.Fatal(err)
		}
		return check, r
	}
	_, r := scope(`{"brokered": {"forge": {"repos": ["org/lib"]}}}`)
	if len(r.EntryEdits) != 1 || r.EntryEdits[0].Key != "brokered.forge.repos" ||
		!slices.Equal(r.EntryEdits[0].Files, []string{WorkspaceConfigName}) {
		t.Fatalf("the added legacy entry does not name its authored key and file: %#v", r.EntryEdits)
	}
	check, r := scope(`{}`)
	if len(check.Sources) != 1 || check.Sources[0].EntryKey != "" {
		t.Fatalf("a source with no authored entry carries a key: %#v", check.Sources)
	}
	for _, edit := range r.EntryEdits {
		if strings.Contains(edit.Key, "brokered.") {
			t.Errorf("a workspace with no entry names the retired key: %q", edit.Key)
		}
	}
}
