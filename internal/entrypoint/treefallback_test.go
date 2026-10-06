package entrypoint

// treefallback_test.go is the jail's half of an UNMODIFIED EXTENSION's FALLBACK
// (docs/design/pi-extension-store-builds.md §4.3, XB-D7): the boot that renders the agent's list puts
// the author's raw entry in the tree's place wherever the host handed this jail no tree — no wire at
// all, a wire naming the key with no build, or a wire not naming it — and leaves the tree's entry
// where it handed one. It reads the FILE the agent reads, through the boot's own loop
// (ConfigurePackSurfaces), so deleting the substitution's call there fails it.

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const (
	fallbackKey   = "personal/pi-web-access"
	fallbackTree  = "~/.pi/agent/yolo-ext/pi-web-access/node_modules/pi-web-access"
	fallbackEntry = "npm:pi-web-access"
)

// fallbackPack contributes an npm extension with a fallback, and the list entry naming its tree.
func fallbackPack(t *testing.T) *packload.Pack {
	t.Helper()
	add, err := json.Marshal([]any{listKilo, fallbackTree})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "personal", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Into: ".pi/agent/yolo-ext/pi-web-access", Source: "npm:pi-web-access",
			Fallback: fallbackEntry},
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages", Add: add},
	}}}
}

func TestTheBootTakesAnUnmodifiedExtensionsFallbackWhereNoTreeIsHanded(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire map[string]TreeDelivery
		want string
	}{
		{"no wire: a notch that builds no tree, or an older launcher", nil, fallbackEntry},
		{"a wire naming the key with no build", map[string]TreeDelivery{fallbackKey: {Into: ".pi/agent/yolo-ext/pi-web-access",
			Reason: "its build failed on the host"}}, fallbackEntry},
		{"a wire that does not name the key", map[string]TreeDelivery{"other/x": {Build: "abc"}}, fallbackEntry},
		{"a wire handing a build", map[string]TreeDelivery{fallbackKey: {Into: ".pi/agent/yolo-ext/pi-web-access",
			Build: "0123abcd", Label: "1.4.2"}}, fallbackTree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := overlayRenderEnv(t)
			if tc.wire != nil {
				e.Vars[PatchedTreesEnv] = PatchedTreesWire(tc.wire)
			}
			owner, ext := listOwnerPack(t, "", nil), fallbackPack(t)
			bootJail(t, e, owner, ext)
			wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listKilo, tc.want)
			// The pack the launch loaded is untouched: the substitution works on copies.
			var add []any
			_ = json.Unmarshal(ext.Decl.Contributes[1].Add, &add)
			if add[1] != fallbackTree {
				t.Errorf("the boot rewrote the loaded pack's own list: %v", add)
			}
		})
	}
}
