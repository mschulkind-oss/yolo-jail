package cli

import (
	"bytes"
	"testing"
)

// --no-embedded-packs is accepted by the front door's unknown-flag refusal AND reaches the
// engine: a flag in one list and not the other either refuses itself or does nothing.
func TestPruneNoEmbeddedPacksFlagIsWired(t *testing.T) {
	args := []string{"prune", "--no-embedded-packs"}
	var errw bytes.Buffer
	if refuseUnknownFlags("prune", args, pruneKnownFlags, &errw) {
		t.Fatalf("prune refused its own --no-embedded-packs: %s", errw.String())
	}
	if !pruneOptions(args).NoEmbeddedPacks {
		t.Error("--no-embedded-packs did not set Options.NoEmbeddedPacks")
	}
	if pruneOptions([]string{"prune"}).NoEmbeddedPacks {
		t.Error("the embedded-pack sweep is off by default")
	}
}
