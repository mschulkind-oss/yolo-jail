package agentcfg

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// packManifest is core's surfaces merged with every EMBEDDED PACK's, which is where the
// agent surfaces now live.
//
// These tests were written against the Go literals and assert the exact posture each
// surface must have — the YOLO permissions block, the force-managed keys, the paths. That
// content moved into packs/*/pack.json, so pointing the SAME assertions at the packs is
// how we know the move was faithful. Rewriting them to assert whatever the packs happen
// to say would have thrown away the check.
func packManifest(t *testing.T) *manifest.Manifest {
	return packManifestFor(t)
}

func packManifestFor(t *testing.T) *manifest.Manifest {
	t.Helper()
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var extra []manifest.Surface
	for _, p := range packs {
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			t.Fatalf("pack %s: %v", p.Name, probs)
		}
		extra = append(extra, surfaces...)
	}
	m, err := ManifestWith(extra...)
	if err != nil {
		t.Fatalf("merging pack surfaces: %v", err)
	}
	return m
}
