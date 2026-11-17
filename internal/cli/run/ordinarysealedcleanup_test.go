package run

import (
	"os"
	"testing"
)

// The production remover also owns ordinary trees; unlinkable entries need
// neither read access to their bytes nor permission to chmod their owners.
func TestRemoveSealedSkillTreeOrdinaryCompatibilityAsUnprivilegedOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("foreign-owner fixture needs a root parent; child runs as uid65534")
	}
	t.Setenv("YOLO_TEST_ORDINARY_CLEANUP_CONTROL", "1")
	TestDiscardRemoverDeletesSealedPayloadAsUnprivilegedOwner(t)
}
