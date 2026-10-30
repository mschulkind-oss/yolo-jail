package config

// requestsvalidate_test.go pins that the launch's validation (ValidateConfig, what the launch
// preflight and `yolo check` both run) accepts a TOML project's `[requests.<source>]` table, the
// key docs/design/readable-config.md RC-D12 gives a project for asking past the user's `always`.
// The edit path already accepted it (reportUnknownKeysWithReadableFG); the launch path refused it
// as `config.requests: unknown key`, so every `yolo request` was followed by a failing check.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// UngrantedBrokerNotes, `yolo check`'s note for a selected broker the user grants nothing, reads
// the readable grant (the source's read capability under `allow.<source>`) as it reads the legacy
// one, and names the readable next step, `yolo allow <source> <capability>`, to a TOML user.
func TestUngrantedBrokerNotesReadTheReadableGrant(t *testing.T) {
	gitBroker := fakeResolver{"git-broker": {Name: "git-broker",
		Brokered: &loopholedecl.Brokered{Source: "git", ReadCapability: "fetch"}}}

	t.Run("a TOML user whose allow grants the read gets no note", func(t *testing.T) {
		for _, list := range []string{"always", "on_request"} {
			h := newMountsHost(t)
			h.userTOML(t, "packs = []\n\n[allow.git]\n"+list+" = [\"fetch\"]\n")
			if notes := UngrantedBrokerNotes(gitBroker); len(notes) != 0 {
				t.Fatalf("%s = [\"fetch\"] drew a grants-nothing note: %q", list, notes)
			}
		}
	})
	t.Run("a TOML user with no grant is told yolo allow", func(t *testing.T) {
		h := newMountsHost(t)
		userPath := h.userTOML(t, "packs = []\n")
		got := strings.Join(UngrantedBrokerNotes(gitBroker), "\n")
		for _, want := range []string{"grants nothing", "`yolo allow git fetch`", userPath} {
			if !strings.Contains(got, want) {
				t.Fatalf("note lacks %q: %q", want, got)
			}
		}
		if strings.Contains(got, "brokered.git.grants") {
			t.Fatalf("note names the legacy grant to a TOML user: %q", got)
		}
	})
	t.Run("a JSONC user is told to convert, then yolo allow", func(t *testing.T) {
		h := newMountsHost(t)
		if err := os.WriteFile(filepath.Join(h.home, ".config", "yolo-jail", "config.jsonc"), []byte(`{"packs": []}`), 0o644); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(UngrantedBrokerNotes(gitBroker), "\n")
		migrate, allow := strings.Index(got, "`yolo config migrate`"), strings.Index(got, "`yolo allow git fetch`")
		if migrate < 0 || allow < migrate || strings.Contains(got, "brokered.git.grants") {
			t.Fatalf("a JSONC user's note does not name `yolo config migrate` then `yolo allow git fetch`: %q", got)
		}
	})
}

func TestValidateConfigAcceptsATOMLProjectsRequests(t *testing.T) {
	h := newMountsHost(t)
	h.userTOML(t, "packs = []\n\n[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	h.workspaceFile(t, paths.WorkspaceConfigTOMLName, "[requests.git]\npush = [\"main\"]\n")
	errs, _ := h.validate(t)
	for _, e := range errs {
		if strings.Contains(e, "requests") {
			t.Fatalf("the launch's validation refused a TOML project's requests: %q", errs)
		}
	}
}

// A key the readable schema does not know is still refused: accepting `requests` must not
// widen the top-level set past the readable schema's roots.
func TestValidateConfigStillRefusesAnUnknownTopLevelKey(t *testing.T) {
	h := newMountsHost(t)
	h.userTOML(t, "packs = []\n")
	h.workspaceFile(t, WorkspaceConfigName, `{"requestz": {}}`)
	errs, _ := h.validate(t)
	if !strings.Contains(strings.Join(errs, "\n"), "config.requestz: unknown key") {
		t.Fatalf("an unknown top-level key was not refused: %q", errs)
	}
}
