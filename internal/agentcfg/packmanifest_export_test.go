package agentcfg

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// PackManifestForTest is packManifest exported for the EXTERNAL test package
// (agentcfg_test), which holds the probe and verifier suites. Those live outside the
// package on purpose — they exercise the engine the way a real caller does — so they
// cannot reach the unexported helper.
//
// In an _test.go file, so it is not part of the shipped API.
func PackManifestForTest(t *testing.T) *manifest.Manifest { return packManifestFor(t) }
