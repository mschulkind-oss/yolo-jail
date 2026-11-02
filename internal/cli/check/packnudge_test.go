package check

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func nudgeUser(tables map[string]map[string]any) *jsonx.OrderedMap {
	allow := jsonx.NewOrderedMap()
	for name, body := range tables {
		t := jsonx.NewOrderedMap()
		for k, v := range body {
			t.Set(k, v)
		}
		allow.Set(name, t)
	}
	user := jsonx.NewOrderedMap()
	user.Set("allow", allow)
	return user
}

func nudgeOptions(listed []string, ttyAnswer string, add func(string) error) (*Options, *reporter, *bytes.Buffer) {
	var buf bytes.Buffer
	o := &Options{
		ListedPacks: func() ([]string, error) { return listed, nil },
		AllowFacts: func(l []string) config.AllowPackFacts {
			return config.AllowPackFacts{
				Listed:       l,
				Names:        map[string]string{"aws-auth": "aws-auth", "claude": "claude"},
				LoginProfile: map[string]string{"aws-auth": "profile"},
			}
		},
		IsTTYStdout: func() bool { return ttyAnswer != "" },
	}
	if ttyAnswer != "" {
		o.Stdin = strings.NewReader(ttyAnswer)
		o.PackAdd = add
	}
	return o, newReporter(&buf, false), &buf
}

func TestCheckNudgesUnselectedPackAndFailsWhenDeclined(t *testing.T) {
	user := nudgeUser(map[string]map[string]any{"aws-auth": {"profile": "dev"}})

	// Declined on a terminal: asked, not added, failed, command named.
	added := ""
	o, r, buf := nudgeOptions(nil, "n\n", func(h string) error { added = h; return nil })
	o.sectionPackNudge(r, user, nil)
	if added != "" {
		t.Errorf("a declined offer ran pack add(%q)", added)
	}
	out := buf.String()
	for _, want := range []string{"Add it to packs? [y/N]", "yolo pack add aws-auth", "delete the table"} {
		if !strings.Contains(out, want) {
			t.Errorf("declined output lacks %q:\n%s", want, out)
		}
	}
	if r.failed != 1 {
		t.Errorf("failed = %d, want 1", r.failed)
	}

	// No terminal: never asked, still fails with the command.
	o, r, buf = nudgeOptions(nil, "", nil)
	o.sectionPackNudge(r, user, nil)
	if strings.Contains(buf.String(), "[y/N]") || r.failed != 1 || !strings.Contains(buf.String(), "yolo pack add aws-auth") {
		t.Errorf("no-TTY run: failed=%d\n%s", r.failed, buf.String())
	}

	// Accepted: pack add runs in-process for the named pack and the finding clears.
	o, r, _ = nudgeOptions(nil, "y\n", func(h string) error { added = h; return nil })
	o.sectionPackNudge(r, user, nil)
	if added != "aws-auth" || r.failed != 0 {
		t.Errorf("accepted: added=%q failed=%d", added, r.failed)
	}

	// Accepted but the add itself refuses: the refusal's words are the failure.
	o, r, buf = nudgeOptions(nil, "y\n", func(string) error { return errors.New("host only: run yolo host-run") })
	o.sectionPackNudge(r, user, nil)
	if r.failed != 1 || !strings.Contains(buf.String(), "host-run") {
		t.Errorf("add refusal: failed=%d\n%s", r.failed, buf.String())
	}
}

func TestCheckDidYouMeanForUnknownPack(t *testing.T) {
	user := nudgeUser(map[string]map[string]any{"aws-aut": {"profile": "dev"}})
	o, r, buf := nudgeOptions(nil, "", nil)
	o.sectionPackNudge(r, user, nil)
	out := buf.String()
	if !strings.Contains(out, "Did you mean aws-auth?") || !strings.Contains(out, "no such pack") || r.failed != 1 {
		t.Errorf("failed=%d\n%s", r.failed, out)
	}
}

func TestCheckWarnsLoginPackWithoutProfile(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	user := nudgeUser(map[string]map[string]any{})
	o, r, buf := nudgeOptions([]string{"aws-auth"}, "", nil)
	o.sectionPackNudge(r, user, nil)
	if r.failed != 0 || r.warned != 1 || !strings.Contains(buf.String(), "yolo config set allow.aws-auth.profile") {
		t.Errorf("failed=%d warned=%d\n%s", r.failed, r.warned, buf.String())
	}

	// A profile named: silent.
	user = nudgeUser(map[string]map[string]any{"aws-auth": {"profile": "dev"}})
	o, r, buf = nudgeOptions([]string{"aws-auth"}, "", nil)
	o.sectionPackNudge(r, user, nil)
	if r.failed+r.warned != 0 || buf.Len() != 0 {
		t.Errorf("a configured login pack drew output:\n%s", buf.String())
	}
}

// TestCheckRunsThePackNudge pins the call site in Check: a check over a real user config that
// carries an unselected pack's table must fail and name the command.
func TestCheckRunsThePackNudgeSectionHeader(t *testing.T) {
	user := nudgeUser(map[string]map[string]any{"aws-auth": {"profile": "dev"}})
	o, r, buf := nudgeOptions(nil, "", nil)
	o.sectionPackNudge(r, user, nil)
	if !strings.Contains(buf.String(), "Pack switch") {
		t.Errorf("no section header:\n%s", buf.String())
	}
}

// In a jail the user scope never carries [allow] (the host withholds it), so a listed login
// pack cannot be judged profile-less: no warning, whatever the host file says.
func TestCheckDoesNotWarnLoginPackWithoutProfileInAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	o, r, buf := nudgeOptions([]string{"aws-auth"}, "", nil)
	o.sectionPackNudge(r, nudgeUser(map[string]map[string]any{}), nil)
	if r.failed+r.warned != 0 || buf.Len() != 0 {
		t.Errorf("in-jail check warned about a profile it cannot see: failed=%d warned=%d\n%s", r.failed, r.warned, buf.String())
	}
}
