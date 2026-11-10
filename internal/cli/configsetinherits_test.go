package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// PI-D6: the opt-out the refusals name, `yolo config set providers.<x>.inherits '""' --user`,
// must be accepted by the edit verb, and a project file must refuse the key (PI-D5).
func TestConfigSetWritesProviderInheritsAtUserScopeOnly(t *testing.T) {
	user, ws := genericFixture(t, "# keep user\n", configdoc.TOML, "# keep project\n")
	rc, out, errs := runConfigVerb(t, "set", "providers.bedrock-key.inherits", `""`, "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, "[providers.bedrock-key]") || !strings.Contains(got, `inherits = ""`) {
		t.Fatalf("inherits not written:\n%s", got)
	}

	rc, out, errs = runConfigVerb(t, "set", "providers.x.inherits", "bedrock", "--project")
	if rc == 0 {
		t.Fatalf("a project inherits was written: stdout=%q stderr=%q", out, errs)
	}
	if got := readFile(t, ws.Path); strings.Contains(got, "inherits") {
		t.Fatalf("the project file holds inherits:\n%s", got)
	}
	if !strings.Contains(out+errs, "user-scope only") {
		t.Fatalf("the refusal does not say why: stdout=%q stderr=%q", out, errs)
	}
}
