package integration

// parentpointerenv_test.go keeps an in-jail run of this suite from borrowing the jail's own
// credential pointers. A podman launch made from inside a jail takes a declaring loophole's pointer
// from the launching environment instead of starting the loophole's daemons
// (docs/design/sso-backed-bedrock.md SSO-D2), and every launch here inherits this process's
// environment. Run from an agent in a Bedrock jail, the aws-auth tests would then be served by
// that jail's real service rather than by the fake `aws` they install, and would pass or fail for
// a reason CI never sees. So the suite unsets every variable a shipped loophole's
// `inherit_from_parent_jail` names, before the first launch (applySuiteEnv), and each launch builds
// its own service as it does on CI.

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// parentJailPointerVars is every variable a shipped loophole's `inherit_from_parent_jail` block
// names, read from the embedded packs, so a loophole that adopts the block is covered with no edit
// here. A manifest that does not decode names nothing; the packs' own tests refuse it.
func parentJailPointerVars() []string {
	manifests, _ := fs.Glob(packs.FS, "*/loopholes/*/"+loopholedecl.ManifestName)
	var out []string
	for _, path := range manifests {
		data, err := fs.ReadFile(packs.FS, path)
		if err != nil {
			continue
		}
		m, err := loopholedecl.Decode(data, "/"+strings.TrimSuffix(path, "/"+loopholedecl.ManifestName))
		if err != nil || m.InheritFromParentJail == nil {
			continue
		}
		out = append(out, m.InheritFromParentJail.Vars...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// unsetParentJailPointers removes them from this process's environment.
func unsetParentJailPointers() {
	for _, v := range parentJailPointerVars() {
		os.Unsetenv(v)
	}
}

// The scrub reads the shipped declarations, so it must find aws-auth's: an empty list would
// leave an in-jail run inheriting the jail's pointer with nothing saying so.
func TestTheSuiteUnsetsTheShippedParentJailPointers(t *testing.T) {
	got := parentJailPointerVars()
	for _, want := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"} {
		if !slices.Contains(got, want) {
			t.Errorf("the suite would not unset %s (it unsets %v)", want, got)
		}
	}
	for _, name := range got {
		t.Setenv(name, "integration-parent-pointer-sentinel")
	}
	unsetParentJailPointers()
	for _, name := range got {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("unsetParentJailPointers left %s in the suite environment", name)
		}
	}
}
