// Package bundledloopholes embeds the bundled loophole manifests into every
// Go binary that links internal/loopholes, so an installed binary (brew,
// goreleaser, go install) carries them without a repo checkout.
package bundledloopholes

import "embed"

// FS holds the bundled loophole directories. The list is EXPLICIT so a stray
// __pycache__/ or editor droppings never get baked into a release binary — the
// cost is that a NEW loophole directory must be added to the directive below.
// TestEmbedMatchesTree (internal/loopholes) fails the build the moment the
// on-disk tree and this embed drift, so the sync is test-enforced, not
// convention-enforced.
//
//go:embed all:audio all:claude-oauth-broker all:host-processes
var FS embed.FS
