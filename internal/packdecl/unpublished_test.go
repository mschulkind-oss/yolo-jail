package packdecl

import (
	"strings"
	"testing"
)

// ONE PREDICATE, TWO PROJECTIONS: a `program` contribution's Install (read by a jail's launcher
// generation) and its DepRequirement (read by the host's dep probe) answer "can this be installed
// here" identically, sentence included, for every platform shape; a `requires` is never
// unpublished, having no vendor to ask (docs/plans/notch-convergence.md item 7).
func TestInstallAndDepRequirementAgreeAboutWhereAProgramIsPublished(t *testing.T) {
	m, problems := Decode([]byte(`{"name":"p","contributes":[
  {"kind":"program","bin":"only-darwin","via":"npm","package":"x","platforms":["darwin"]},
  {"kind":"program","bin":"one-arch","via":"npm","package":"y","platforms":["linux/arm64","darwin"]},
  {"kind":"program","bin":"everywhere","via":"npm","package":"z"},
  {"kind":"requires","bin":"asserted"}]}`))
	if len(problems) > 0 {
		t.Fatalf("fixture: %v", problems)
	}
	installs := map[string]Install{}
	for _, in := range m.InstallContributions() {
		installs[in.Bin] = in
	}
	for _, d := range m.DepRequirements() {
		for _, plat := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}} {
			got := d.UnpublishedReason(plat[0], plat[1])
			if d.Bin == "asserted" {
				if got != "" {
					t.Errorf("a requires is never unpublished: %q", got)
				}
				continue
			}
			if want := installs[d.Bin].UnpublishedReason(plat[0], plat[1]); got != want {
				t.Errorf("%s on %s/%s: dep says %q, install says %q", d.Bin, plat[0], plat[1], got, want)
			}
			if (got == "") != installs[d.Bin].SupportsPlatform(plat[0], plat[1]) {
				t.Errorf("%s on %s/%s: the reason disagrees with SupportsPlatform", d.Bin, plat[0], plat[1])
			}
		}
	}
	why := installs["one-arch"].UnpublishedReason("linux", "amd64")
	for _, want := range []string{"darwin, linux/arm64", "this machine is linux/amd64",
		"nothing can be installed to fix it"} {
		if !strings.Contains(why, want) {
			t.Errorf("the reason must name the declared set beside this machine; want %q in %q", want, why)
		}
	}
}
