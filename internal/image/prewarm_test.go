package image

import (
	"bytes"
	"testing"
)

// PREWARM IS THE BUILD'S FIRST HALF AND NOTHING ELSE (prewarm.go): with the runtime holding this
// tree's stock image it builds nothing; without it, it builds the derivation, and in both it prints
// nothing and loads, tags and copies nothing, which the image step's report is the one disclosure
// of. Red with Prewarm's build deleted, or its stock check.
func TestPrewarmBuildsTheDerivationAndNothingElse(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	stockRef := StockImageRef("podman", testIdentity)

	loaded := newFakeRuntime(stockRef)
	o := stockOpts(t, loaded, &out, testIdentity)
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		t.Error("the prewarm built an image the runtime already holds")
		return "", nil
	}
	Prewarm(o)

	missing := newFakeRuntime(JailImage("podman"))
	o = stockOpts(t, missing, &out, testIdentity)
	built := 0
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		built++
		return storeManifest(t, "prewarmed"), nil
	}
	Prewarm(o)
	if built != 1 {
		t.Errorf("the prewarm built %d times, want the derivation once", built)
	}
	if out.Len() != 0 || missing.loads != 0 || len(missing.copiedDests) != 0 {
		t.Errorf("the prewarm printed %q and delivered %d loads, %d copies; it must do neither", out.String(),
			missing.loads, len(missing.copiedDests))
	}
}
