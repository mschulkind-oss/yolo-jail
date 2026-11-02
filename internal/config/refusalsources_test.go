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
	write(t, filepath.Join(ws, WorkspaceConfigName), `{}`)

	msg := refuse(t, ws).Error()
	if !strings.Contains(msg, local) {
		t.Errorf("the refusal does not name %s, the file that WINS the workspace merge:\n%s", local, msg)
	}
	if want := filepath.Join(ws, WorkspaceConfigName); !strings.Contains(msg, want) {
		t.Errorf("the refusal does not name %s:\n%s", want, msg)
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

// A TOML workspace is read from yolo-jail.toml and yolo-jail.local.toml, so the refusal names
// those. ResolveWorkspaceConfigPath knows only the legacy names: on the owner's TOML workspace
// the refusal named a yolo-jail.jsonc that does not exist and left out the local file whose
// values win, which is every file it could have named wrong.
func TestRefusalNamesTheTOMLFilesTheLoaderRead(t *testing.T) {
	ws := approvalWorkspace(t)
	committed := filepath.Join(ws, paths.WorkspaceConfigTOMLName)
	local := filepath.Join(ws, paths.WorkspaceLocalConfigTOMLName)
	write(t, committed, "[install]\nnix = [\"strace\"]\n")
	write(t, local, "[install]\nnix = [\"htop\"]\n")

	e := refuse(t, ws)
	if e.WorkspaceConfig != committed || e.WorkspaceLocalConfig != local {
		t.Errorf("the refusal names %q and %q, want %q and %q", e.WorkspaceConfig,
			e.WorkspaceLocalConfig, committed, local)
	}
	msg := e.Error()
	if strings.Contains(msg, WorkspaceConfigName) || strings.Contains(msg, WorkspaceLocalConfigName) {
		t.Errorf("the refusal names a legacy file this TOML workspace does not have:\n%s", msg)
	}
}

// `yolo config apply` has no launch to re-run, so its refusal does not offer the launch flag:
// it names running the command where it can ask, and says the command, not "this launch", had
// no terminal.
func TestApplyRefusalNamesTheApplysOwnNextStep(t *testing.T) {
	ws := approvalWorkspace(t)
	e := refuse(t, ws)
	if launch := e.Error(); !strings.Contains(launch, AcceptConfigChangesFlag) {
		t.Fatalf("the launch's refusal lost its flag:\n%s", launch)
	}
	e.Apply = true
	msg := e.Error()
	if strings.Contains(msg, AcceptConfigChangesFlag) || strings.Contains(msg, "THIS LAUNCH") {
		t.Errorf("the apply's refusal offers the launch's approval:\n%s", msg)
	}
	for _, want := range []string{"`yolo config apply` has no terminal", ApplyRetryStep(ws)} {
		if !strings.Contains(msg, want) {
			t.Errorf("the apply's refusal does not say %q:\n%s", want, msg)
		}
	}
}
