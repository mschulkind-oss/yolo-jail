package packload

import "testing"

// TestReleaseDecodeProbeAPIIsStable pins the two functions packs/releasedecode_test.go's probe
// calls when it is compiled inside the LAST RELEASE's tree: TolerateSkew, then LoadDir per pack,
// which is how every release's in-jail boot has read a staged pack. This tree is the next
// release, so these two signatures are a contract with every future run of that test — change
// one and the check breaks at the next tag, in a tree nobody can edit any more. If a change
// really must move them, the probe must learn to call both spellings first.
func TestReleaseDecodeProbeAPIIsStable(t *testing.T) {
	var (
		tolerate func()                                    = TolerateSkew
		load     func(root, name string) (*Pack, []string) = LoadDir
	)
	if tolerate == nil || load == nil {
		t.Fatal("unreachable: a nil function value")
	}
}
