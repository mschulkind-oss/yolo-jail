package darwinpkg

// opensslfloor_test.go pins that `openssl` stays on the core floor, and names who needs it.
//
// # Why this file exists at all
//
// `internal/oauthbroker/opensslbake_test.go` used to pin it, as the broker's dependency. That
// dependency is GONE — `EnsureCAAndLeaf` mints with `crypto/x509` in-process since `d5bb1e5d`
// — so that test was deleted with the thing it was about. Correct, and it left a hole: the
// only remaining guard, `floor_drift_test.go`, pins that the Go list and `flake.nix`'s
// `coreFloorNames` AGREE. Dropping a name from both keeps it green.
//
// # What that hole costs, and why it is not the image that pays
//
// `macos-user` bakes NOTHING — there is no image on that backend — so it resolves `openssl`
// from the darwin nix profile this floor list builds. `macosuser.real.go` execs
// `openssl rand -base64 32` to mint the sandbox identity's password, which is on the path
// that CREATES the account a macos-user launch runs as. Losing the name there is not a
// degraded feature; it is a backend that cannot provision, on the one backend with no image
// to fall back to and the one this repo can least easily test.
//
// So the pin belongs HERE, beside the floor it protects, and it names its consumers — a bare
// "openssl must be present" invites the next reader to delete it as cargo.

import "testing"

func TestOpensslStaysOnTheCoreFloor(t *testing.T) {
	var found bool
	for _, name := range ImageCoreNames {
		if name == "openssl" {
			found = true
			break
		}
	}
	if !found {
		t.Error(`"openssl" left ImageCoreNames.

Two live consumers still need it, and NEITHER is internal/oauthbroker, which stopped
shelling out to it in 4ceab956:

  * internal/macosuser/real.go — "openssl rand -base64 32" mints the sandbox identity's
    password. macos-user bakes no image, so this floor list IS where that binary comes
    from; without it the backend cannot provision an account at all.
  * internal/entrypoint/shims.go — the generated sha256sum shim's last fallback.

If a consumer really has gone away, delete this test in the same commit as the consumer
and say so. Deleting the name alone is silent on the backend that cannot afford it.`)
	}
}
