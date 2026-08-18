package config

// refusalsources_test.go pins the file list the OQ-D2 refusal prints.
//
// The refusal's reader is by construction someone who cannot be prompted, so the
// list is the only navigation they get. It named yolo-jail.jsonc and the user
// config — but LoadWorkspaceConfig merges yolo-jail.local.jsonc OVER
// yolo-jail.jsonc, so the file that WINS the merge was the one file the message
// did not name. A reader who diffs only yolo-jail.jsonc against git, finds it
// clean, and concludes nothing changed has been sent to the wrong place by the
// message itself.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// refuse drives CheckConfigChanges to the non-interactive refusal and returns it.
func refuse(t *testing.T, ws string) *ChangedNonInteractiveError {
	t.Helper()
	approve(t, ws, decode(t, `{"packages": ["strace"]}`))
	_, err := CheckConfigChanges(ws, decode(t, `{"packages": ["strace", "htop"]}`), false, false, nil)
	var refusal *ChangedNonInteractiveError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *ChangedNonInteractiveError, got %T: %v", err, err)
	}
	return refusal
}

// When yolo-jail.local.jsonc exists it is part of the merge the snapshot stores,
// so a change can have come from it and the refusal has to say so.
func TestRefusalNamesTheWorkspaceLocalConfigWhenItExists(t *testing.T) {
	ws := approvalWorkspace(t)
	local := filepath.Join(ws, WorkspaceLocalConfigName)
	write(t, local, `{"packages": ["strace"]}`)

	msg := refuse(t, ws).Error()
	if !strings.Contains(msg, local) {
		t.Errorf("the refusal does not name %s, the file that WINS the workspace merge:\n%s", local, msg)
	}
	// The other two sources stay named — this is an addition, not a swap.
	for _, want := range []string{filepath.Join(ws, WorkspaceConfigName), paths.UserConfigPath()} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal stopped naming %s:\n%s", want, msg)
		}
	}
}

// A path that does not exist reads as "look here". Naming an absent
// yolo-jail.local.jsonc would send the same reader to a second wrong place, so
// the entry appears only when the file does.
func TestRefusalOmitsTheWorkspaceLocalConfigWhenAbsent(t *testing.T) {
	ws := approvalWorkspace(t)
	msg := refuse(t, ws).Error()
	if absent := filepath.Join(ws, WorkspaceLocalConfigName); strings.Contains(msg, absent) {
		t.Errorf("the refusal names %s, which does not exist:\n%s", absent, msg)
	}
}
