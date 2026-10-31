package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// The step the workspace refusal names, `yolo config set shipped_skill_prefix '"<word>"' --user`, writes the
// key into the user file and the result validates.
func TestConfigSetWritesTheShippedSkillPrefix(t *testing.T) {
	user, ws := genericFixture(t, "# keep user\n", configdoc.TOML, "# keep project\n")
	rc, out, errs := runConfigVerb(t, "set", "shipped_skill_prefix", `"yolo"`, "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, `shipped_skill_prefix = "yolo"`) {
		t.Fatalf("value not written (rc=%d stderr=%q):\n%s", rc, errs, got)
	}
}

// PN-D13 at the host notch: with shipped_skill_prefix set, `yolo host apply` delivers yolo's
// built-in skills as `<prefix>-<skill>`, each naming itself and the others so, and no bare copy.
// Unset is TestHostApplyComposesGeneratedHostSkillsThroughOwnership.
func TestHostApplyDeliversBuiltinSkillsUnderTheShippedSkillPrefix(t *testing.T) {
	home := setupHostMechanicsTest(t)
	selectPacksWith(t, home, `"claude","codex","pi"`, `,"shipped_skill_prefix":"yolo"`)
	rc, report := applyWith(t, true, strings.NewReader("y\ny\ny\n"))
	if rc != 0 {
		t.Fatalf("host apply rc=%d\n%s", rc, report)
	}
	claude := filepath.Join(home, ".claude", "skills")
	for _, bare := range []string{"configuring-the-jail", "diagnosing-the-jail", "jail-mechanics"} {
		name := "yolo-" + bare
		body, err := os.ReadFile(filepath.Join(claude, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("host built-in %s is not delivered as %s: %v\n%s", bare, name, err, report)
		}
		if !strings.Contains(string(body), "\nname: "+name+"\n") {
			t.Errorf("%s/SKILL.md does not name itself %s", name, name)
		}
		if strings.Contains(string(body), "**"+bare+"**") {
			t.Errorf("%s still refers to the bare **%s**", name, bare)
		}
		if _, err := os.Stat(filepath.Join(claude, bare)); !os.IsNotExist(err) {
			t.Errorf("the bare %s is delivered too (err=%v)", bare, err)
		}
	}
}
