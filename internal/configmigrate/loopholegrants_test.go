package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The loophole rows of docs/reference/loophole-grants.md §6, one fixture each, through the
// migration planner: each converts, the equality proof passes, and the standing per loophole per
// kind in this workspace is the same before and after (LG-D11). The packs are the shipped
// grant-declaring ones; the configs are synthetic.

type loopholeRow struct {
	name     string
	user     string // config.jsonc
	project  string // yolo-jail.jsonc, "" for none
	switches map[string]bool
	// approve records the workspace's approval as it stands (the approved-snapshot row).
	approve bool
	// wantUser and wantProject must appear in the converted files; wantNotUser must not.
	wantUser, wantNotUser, wantProject []string
	// standings are this workspace's, "<pack>.<kind>" → the standing text, on both sides.
	standings map[string]string
	// carried are the grants the plan carries into this workspace's record, by pack.
	carried map[string][]string
}

func loopholeRowFixture(t *testing.T, r loopholeRow) (dir, ws string) {
	t.Helper()
	home := env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	ws = realDir(t)
	write(t, filepath.Join(dir, "config.jsonc"), r.user)
	if r.project != "" {
		write(t, filepath.Join(ws, "yolo-jail.jsonc"), r.project)
	}
	for name, on := range r.switches {
		if _, err := config.SetWorkspaceLoophole(ws, name, on); err != nil {
			t.Fatal(err)
		}
	}
	if r.approve {
		part, err := config.ApprovalRecordPart(ws)
		if err != nil {
			t.Fatal(err)
		}
		if err := config.WriteApprovalRecord(ws, part); err != nil {
			t.Fatal(err)
		}
	}
	return dir, ws
}

func TestLegacyLoopholeEnabledMigratesToAllowAndRequests(t *testing.T) {
	const packs = `"packs": ["journal", "host-processes", "audio"]`
	rows := []loopholeRow{{
		name:      "user enabled: true, an unvalued kind",
		user:      `{` + packs + `, "loopholes": {"journal": {"enabled": true}}}`,
		wantUser:  []string{"[allow.journal]", `always = ["read"]`},
		standings: map[string]string{"journal.read": "pending", "journal.read_all": "not_allowed"},
	}, {
		name:      "user enabled: true, a valued kind",
		user:      `{` + packs + `, "loopholes": {"host-processes": {"enabled": true}}}`,
		wantUser:  []string{"[allow.host-processes]", `on_request = ["view"]`},
		standings: map[string]string{"host-processes.view": "requestable"},
	}, {
		name:        "user settings.full: true",
		user:        `{` + packs + `, "loopholes": {"journal": {"enabled": true, "settings": {"full": true}}}}`,
		wantUser:    []string{`always = ["read", "read_all"]`},
		wantNotUser: []string{"full"},
		standings:   map[string]string{"journal.read": "pending", "journal.read_all": "pending"},
	}, {
		name:      "user settings.<user setting>",
		user:      `{` + packs + `, "loopholes": {"host-processes": {"enabled": true, "settings": {"fields": ["pid", "comm"]}}}}`,
		wantUser:  []string{`fields = ["pid", "comm"]`, `on_request = ["view"]`},
		standings: map[string]string{"host-processes.view": "requestable"},
	}, {
		// A user `enabled: false` limited no workspace's switch, so the legacy user config admits
		// a request for audio's use as before (LC-D14), and the converted ceiling says so.
		name:        "user enabled: false",
		user:        `{` + packs + `, "loopholes": {"audio": {"enabled": false}}}`,
		wantUser:    []string{"[allow.audio]", `on_request = ["use"]`},
		wantNotUser: []string{"enabled"},
		standings:   map[string]string{"audio.use": "requestable", "journal.read": "requestable"},
	}, {
		name:        "switch file, enabled: true",
		user:        `{` + packs + `}`,
		switches:    map[string]bool{"journal": true, "audio": true},
		wantUser:    []string{"[allow.audio]", `on_request = ["use"]`},
		wantNotUser: []string{"[allow.journal]"},
		standings:   map[string]string{"journal.read": "granted", "audio.use": "granted"},
		carried:     map[string][]string{"journal": {"read"}, "audio": {"use"}},
	}, {
		name:        "switch file, enabled: true, a valued kind",
		user:        `{` + packs + `}`,
		project:     `{"loopholes": {"host-processes": {"settings": {"visible": ["postgres"]}}}}`,
		switches:    map[string]bool{"host-processes": true},
		wantUser:    []string{`on_request = ["view"]`},
		wantProject: []string{"[requests.host-processes]", `view = ["postgres"]`},
		standings:   map[string]string{"host-processes.view": "granted: postgres"},
		carried:     map[string][]string{"host-processes": {"view=postgres"}},
	}, {
		// The switch file's false is this workspace's withdrawal (LC-D18): it stays in the file,
		// and the workspace holds nothing of journal on either side.
		name:      "switch file, enabled: false",
		user:      `{` + packs + `}`,
		switches:  map[string]bool{"journal": false},
		standings: map[string]string{"journal.read": "not_allowed"},
	}, {
		name:        "enabled: true in an approved workspace snapshot",
		user:        `{` + packs + `}`,
		project:     `{"loopholes": {"audio": {"enabled": true}}}`,
		approve:     true,
		wantUser:    []string{"[allow.audio]", `on_request = ["use"]`},
		wantProject: []string{"[requests.audio]", "use = true"},
		standings:   map[string]string{"audio.use": "granted"},
		carried:     map[string][]string{"audio": {"use"}},
	}, {
		name:    "project enabled: true",
		user:    `{` + packs + `}`,
		project: `{"loopholes": {"journal": {"enabled": true}, "audio": {"enabled": true}}}`,
		// The workspace switch is the request and its admission (LG-D16): audio joins the user
		// ceiling as on_request, and the gate's y grants it, on both sides.
		wantUser:    []string{"[allow.audio]", `on_request = ["use"]`},
		wantProject: []string{"[requests.journal]", "read = true", "[requests.audio]", "use = true"},
		standings:   map[string]string{"journal.read": "pending", "audio.use": "pending"},
	}, {
		name:        "project settings.visible",
		user:        `{` + packs + `, "loopholes": {"host-processes": {"enabled": true}}}`,
		project:     `{"loopholes": {"host-processes": {"settings": {"visible": ["postgres", "redis-server"]}}}}`,
		wantUser:    []string{`on_request = ["view"]`},
		wantProject: []string{`view = ["postgres", "redis-server"]`},
		standings:   map[string]string{"host-processes.view": "pending: postgres, redis-server"},
	}, {
		name:        "project enabled: false, nothing to turn off",
		user:        `{` + packs + `}`,
		project:     `{"loopholes": {"journal": {"enabled": false}}}`,
		wantProject: []string{`journal = "off"`},
		standings:   map[string]string{"journal.read": "not_allowed"},
	}}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			dir, ws := loopholeRowFixture(t, r)
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
			if !p.Equal || len(p.NotConverted) != 0 {
				t.Fatalf("the proof did not pass: equal=%v not converted=%+v differences=%+v", p.Equal, p.NotConverted, p.Differences)
			}
			user := string(target(t, p, filepath.Join(dir, "config.jsonc")).Bytes)
			for _, want := range r.wantUser {
				if !strings.Contains(user, want) {
					t.Errorf("the user config lacks %q:\n%s", want, user)
				}
			}
			for _, unwanted := range append([]string{"loopholes"}, r.wantNotUser...) {
				if strings.Contains(user, unwanted) {
					t.Errorf("the user config still says %q:\n%s", unwanted, user)
				}
			}
			if r.project != "" {
				project := string(target(t, p, filepath.Join(ws, "yolo-jail.jsonc")).Bytes)
				for _, want := range r.wantProject {
					if !strings.Contains(project, want) {
						t.Errorf("the project config lacks %q:\n%s", want, project)
					}
				}
				if strings.Contains(project, "loopholes") {
					t.Errorf("the project config still says loopholes:\n%s", project)
				}
			}
			got := p.GrantStandings[ws]
			for key, want := range r.standings {
				pack, kind, _ := strings.Cut(key, ".")
				if got[pack][kind] != want {
					t.Errorf("standing of %s = %q, want %q (all: %v)", key, got[pack][kind], want, got)
				}
			}
			var carried map[string][]string
			for _, c := range p.Carries {
				if c.Workspace == ws {
					carried = c.Grants
				}
			}
			if len(carried) != len(r.carried) {
				t.Fatalf("carried %v, want %v", carried, r.carried)
			}
			for pack, want := range r.carried {
				if strings.Join(carried[pack], ",") != strings.Join(want, ",") {
					t.Errorf("carried %s = %v, want %v", pack, carried[pack], want)
				}
			}
		})
	}
}

// A project's `enabled: false` under a user's `enabled: true` turned the loophole off in that
// workspace. It converts to the project's withdrawal, `requests.<pack> = "off"` (LC-D18), which
// withholds the user's `always` line there, so the proof passes and the workspace holds nothing
// of journal on either side.
func TestAProjectsEnabledFalseUnderAUsersAlwaysConvertsToItsWithdrawal(t *testing.T) {
	dir, ws := loopholeRowFixture(t, loopholeRow{
		user:    `{"packs": ["journal"], "loopholes": {"journal": {"enabled": true}}}`,
		project: `{"loopholes": {"journal": {"enabled": false}}}`,
	})
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) != 0 {
		t.Fatalf("the proof did not pass: equal=%v not converted=%+v differences=%+v", p.Equal, p.NotConverted, p.Differences)
	}
	project := string(target(t, p, filepath.Join(ws, "yolo-jail.jsonc")).Bytes)
	if !strings.Contains(project, `journal = "off"`) {
		t.Fatalf("the project config does not withdraw journal:\n%s", project)
	}
	user := string(target(t, p, filepath.Join(dir, "config.jsonc")).Bytes)
	if !strings.Contains(user, `always = ["read"]`) {
		t.Fatalf("the user config lost its always line:\n%s", user)
	}
	if got := p.GrantStandings[ws]["journal"]["read"]; got != "not_allowed" {
		t.Fatalf("journal.read = %q here, want not_allowed, as the old files hold it", got)
	}
}

// The switch file's grants reach the approval record, marked carried, and only then is the
// switch removed; after the migration the gate holds the grant as it held it before, and a record
// that cannot be written leaves the switch where it was.
func TestTheMigrationCarriesASwitchFileIntoTheRecord(t *testing.T) {
	r := loopholeRow{user: `{"packs": ["journal", "audio"]}`, switches: map[string]bool{"journal": true, "audio": true}}
	dir, ws := loopholeRowFixture(t, r)
	switchFile := config.WorkspaceFilePath(ws)
	if !exists(switchFile) {
		t.Fatal("the fixture wrote no switch file")
	}

	// A record that cannot be written: the approvals folder is a file.
	approvals := paths.ApprovalsDir()
	write(t, approvals, "not a folder\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if _, err := Apply(p, ApplyOptions{}); err == nil {
		t.Fatal("Apply succeeded with no record to write")
	}
	if !exists(switchFile) {
		t.Fatal("the switch file was removed though its grants were not recorded")
	}
	if err := os.Remove(approvals); err != nil {
		t.Fatal(err)
	}
	journal := Interrupted(Request{UserConfigDir: dir, Workspace: ws})
	if journal == "" {
		t.Fatal("the failed carry left no journal to finish it")
	}
	if _, err := Resume(journal); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if exists(switchFile) {
		t.Error("the switch file outlived the migration that carried it")
	}
	rec := config.RecordedLoopholeGrants(ws)
	if strings.Join(rec["journal"], ",") != "read" || strings.Join(rec["audio"], ",") != "use" {
		t.Errorf("record = %v, want journal [read] and audio [use]", rec)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	var lps []config.GateLoophole
	for _, d := range config.LoopholeGrantDeclarationsForPacks(packload.Embedded()) {
		if d.Pack == "journal" || d.Pack == "audio" {
			lps = append(lps, config.GateLoophole{Pack: d.Pack, Loophole: d.Loophole, Access: d.Access, Grants: d.Grants})
		}
	}
	check := config.WithLoopholeGrants(nil, ws, read, lps)
	standings := check.LoopholeGrantStandings(ws)
	for _, key := range [][2]string{{"journal", "read"}, {"audio", "use"}} {
		if got := standings[key[0]][key[1]].Standing; got != config.LoopholeGrantGranted {
			t.Errorf("%s %s after the migration: %q, want granted", key[0], key[1], got)
		}
	}
	notes := check.UnstatedRecordGrantNotes()
	if len(notes) != 2 || !strings.Contains(strings.Join(notes, "\n"), "yolo request audio use") {
		t.Errorf("the unstated carried grants are not named with their requests: %q", notes)
	}
}
