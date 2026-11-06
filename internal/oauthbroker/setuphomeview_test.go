package oauthbroker

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
)

// Two setups of one workspace, each with its own home (named-setups.md §9), register two views:
// a setup's Home is part of its registration's name, and the legacy home keeps the name every
// registration before setups had homes was written under.
func TestEachSetupsHomeRegistersItsOwnView(t *testing.T) {
	legacy := claudeview.Location{Workspace: "/code/ws", Subdir: "claude"}
	podman := claudeview.Location{Workspace: "/code/ws", Home: "setups/podman-self/home", Subdir: "claude"}
	seatbelt := claudeview.Location{Workspace: "/code/ws", Home: "setups/seatbelt-self/home", Subdir: "claude"}
	names := map[string]bool{registrationFile(legacy): true, registrationFile(podman): true, registrationFile(seatbelt): true}
	if len(names) != 3 {
		t.Fatalf("two homes of one workspace share a registration: %v", names)
	}
	sum := sha256.Sum256([]byte(legacy.Workspace + "\x00" + legacy.Subdir))
	if want := filepath.Join(ViewRegistryDir, hex.EncodeToString(sum[:8])+".json"); registrationFile(legacy) != want {
		t.Errorf("the legacy home's registration moved: %s, want %s", registrationFile(legacy), want)
	}
}
