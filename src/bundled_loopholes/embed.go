// Package bundledloopholes embeds the bundled loophole manifests into every
// Go binary that links internal/loopholes, so an INSTALLED binary (brew
// formula, pipx/uvx wheel, `go install`, goreleaser archive) carries them
// without a repo checkout. Without this, a bare binary resolved
// BundledLoopholesDir to a nonexistent path and discovered ZERO bundled
// loopholes — no broker CA, no --add-host, no audio/host-processes — the
// documented silent-TLS/auth-failure mode. Python never had the problem
// because the wheel shipped this directory as package data next to
// loopholes.py; the embed is the Go analog of that package data.
//
// This package lives inside the (Python) src/ tree on purpose: go:embed
// cannot reference files above the directory of the .go file, and this
// directory IS the payload. The §G post-cutover relocation moves the whole
// directory — embed.go rides along.
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
