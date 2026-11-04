package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// The named-setups refusals name `yolo config set setup <name> --user` and
// `yolo config set setups.<name>.<field> <value> --user` as their next steps
// (named-setups.md §6); the generic edit verbs must write both at user scope.
func TestConfigSetWritesTheSetupKeyAtUserScope(t *testing.T) {
	user, ws := genericFixture(t, "# keep user\n", configdoc.TOML, "# keep project\n")
	rc, out, errs := runConfigVerb(t, "set", "setup", "seatbelt-self", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, `setup = "seatbelt-self"`) {
		t.Fatalf("setup not written:\n%s", got)
	}
	rc, out, errs = runConfigVerb(t, "set", "setups.lab.confinement", "seatbelt", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	got = assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, "[setups.lab]") || !strings.Contains(got, `confinement = "seatbelt"`) {
		t.Fatalf("setups.lab.confinement not written:\n%s", got)
	}
}

// A project file cannot hold `setup`: the edit verb refuses to write it there, by the
// schema scope rule every user-only key meets.
func TestConfigSetRefusesTheSetupKeyInAProject(t *testing.T) {
	_, ws := genericFixture(t, "", configdoc.TOML, "# keep project\n")
	rc, out, errs := runConfigVerb(t, "set", "setup", "seatbelt-self", "--project")
	if rc == 0 {
		t.Fatalf("a project `setup` was written: stdout=%q stderr=%q", out, errs)
	}
	if got := readFile(t, ws.Path); strings.Contains(got, "setup") {
		t.Fatalf("the project file holds setup:\n%s", got)
	}
	if !strings.Contains(out+errs, "config.setup is user-scope only") {
		t.Fatalf("the refusal does not say why: stdout=%q stderr=%q", out, errs)
	}
}
