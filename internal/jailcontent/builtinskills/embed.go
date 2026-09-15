// Package builtinskills embeds the built-in agent Skills that yolo-jail stages
// into every selected agent's read-only skills mount. Baking them into the
// binary (rather than a nix-baked image path) means they iterate with
// `just build-go` alone via the yolo dev-override wrapper, and an installed
// binary carries them without a repo checkout.
//
// The list is EXPLICIT (not a glob) so stray editor droppings never get baked
// into a release binary — mirroring packs/embed.go. The cost is
// that a NEW skill directory must be added to the directive below;
// TestBuiltinSkillsEmbedMatchesTree fails the build the moment the on-disk tree
// and this embed drift, so the sync is test-enforced, not convention-enforced.
package builtinskills

import "embed"

//go:embed all:configuring-the-jail all:diagnosing-the-jail
var FS embed.FS
