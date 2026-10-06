package packdecl

// skew_test.go pins the USE READ (DecodeForUse, docs/design/patched-forks.md PF-D68 and PF-D69):
// strict about everything this build knows, as Decode is, but a contribution holding a kind, a
// `via` or a field this build does not know is skipped and named rather than refusing the pack —
// unless its kind only restricts, which is kept without the field.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The request's own case: a patched fork's `patches` read by a yolo older than the mode. Here a
// field no build knows stands in for it, on a fork and on a patched extension.
func TestDecodeForUseSkipsAContributionWithAnUnknownField(t *testing.T) {
	manifest := []byte(`{"name":"pi-mine","contributes":[
	  {"kind":"program","bin":"pi","via":"source","fork_of":"pi",
	   "source":"git+https://example.com/pi?ref=main","build":"make",
	   "produces":[".local/bin/pi"],"patches_v9":"patches"},
	  {"kind":"skills","from":"skills","into":".pi/skills"}]}`)

	m, problems, skipped := DecodeForUse(manifest)
	if len(problems) != 0 {
		t.Fatalf("an unknown field is not a problem on the use read: %v", problems)
	}
	if len(skipped) != 1 {
		t.Fatalf("want one line for the one skipped contribution, got %q", skipped)
	}
	for _, want := range []string{`contributes[0]: skipping the program contribution "pi"`,
		`unknown field "patches_v9"`, "a newer yolo may read the field (update yolo)", "yolo pack lint"} {
		if !strings.Contains(skipped[0], want) {
			t.Errorf("the line must name %q: %s", want, skipped[0])
		}
	}
	if cs := m.Contributions(); len(cs) != 1 || cs[0].Kind != KindSkills {
		t.Errorf("only the readable contribution may remain: %+v", cs)
	}

	// THE AUTHORING READ IS UNCHANGED: `yolo pack lint` reads through Decode, and refuses.
	if _, strict := Decode(manifest); len(strict) == 0 || !strings.Contains(strict[0], `unknown field "patches_v9"`) {
		t.Errorf("the strict decoder must still refuse the field by name: %v", strict)
	}
}

// PF-D69: a contribution that only restricts the agent is KEPT without the field, because a
// restriction kept in part holds more than none; the line says which. One of each restricting
// kind, and the control: a non-restricting kind with the same field is skipped.
func TestDecodeForUseKeepsARestrictionWithoutTheFieldItCannotRead(t *testing.T) {
	manifest := []byte(`{"name":"guard","contributes":[
	  {"kind":"blocked-tool","bin":"grep","message":"m","suggestion":"rg","newer":1},
	  {"kind":"intercept","bin":"gh","forward":["yolo","gh","--"],"newer":1},
	  {"kind":"autonomy","guarded":{"launch":[{"bin":"pi","flags":[]}],"newer":1}},
	  {"kind":"env","vars":{"A":"1"},"newer":1}]}`)
	m, problems, skipped := DecodeForUse(manifest)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	var kinds []Kind
	for _, c := range m.Contributions() {
		kinds = append(kinds, c.Kind)
	}
	if want := []Kind{KindBlockedTool, KindIntercept, KindAutonomy}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kept %v, want the three restrictions and not the env", kinds)
	}
	if len(skipped) != 4 {
		t.Fatalf("one line per contribution with the field, got %q", skipped)
	}
	for i, want := range []string{`keeping the blocked-tool contribution "grep" without its unknown field "newer"`,
		`keeping the intercept contribution "gh"`, "keeping the autonomy contribution",
		"skipping the env contribution"} {
		if !strings.Contains(skipped[i], want) {
			t.Errorf("line %d must say %q: %s", i, want, skipped[i])
		}
	}
	if !strings.Contains(skipped[0], "a restriction is never dropped") {
		t.Errorf("a kept restriction must say why it is kept: %s", skipped[0])
	}
}

// A kept restriction whose remainder no longer validates refuses the pack — failing closed — as it
// would without the unknown field: here an autonomy contribution whose one posture is spelled with
// a field this build does not know is left with no posture at all.
func TestDecodeForUseRefusesAKeptRestrictionThatNoLongerValidates(t *testing.T) {
	_, problems, skipped := DecodeForUse([]byte(`{"name":"guard","contributes":[
	  {"kind":"autonomy","guarded_by_a_newer_yolo":{"launch":[{"bin":"pi"}]}}]}`))
	if len(problems) == 0 || len(skipped) != 1 {
		t.Fatalf("an autonomy contribution left with no posture must be refused, not skipped: "+
			"problems=%q skipped=%q", problems, skipped)
	}
}

// PROBLEMS KEEP THEIR pack.json INDEX past a skipped contribution, so an author is never sent to
// the wrong entry.
func TestDecodeForUseLabelsProblemsByTheirIndexInPackJSON(t *testing.T) {
	_, problems, skipped := DecodeForUse([]byte(`{"name":"p","contributes":[
	  {"kind":"skills","from":"skills","into":".p/skills","newer":1},
	  {"kind":"kind-from-a-newer-yolo"},
	  {"kind":"env"}]}`))
	if len(skipped) != 2 {
		t.Fatalf("want two skips, got %q", skipped)
	}
	if len(problems) == 0 || !strings.HasPrefix(problems[0], "contributes[2]") {
		t.Errorf("the third entry's problem must be labeled contributes[2]: %q", problems)
	}
}

// The other classes the use read skips, as the jail's tolerant read does — an unknown kind, an
// unknown via, an unknown wire_api — and the ones it still refuses, which name an edit no newer
// yolo makes unnecessary: a retired kind and a second autonomy.
func TestDecodeForUseSkipsWhatANewerYoloReadsAndRefusesWhatNoneDoes(t *testing.T) {
	_, problems, skipped := DecodeForUse([]byte(`{"name":"p","contributes":[
	  {"kind":"kind-from-a-newer-yolo","from":"x"},
	  {"kind":"program","bin":"t","via":"uv","package":"t"},
	  {"kind":"provider","name":"pv","endpoints":{"openai":{"base_url":"https://x","wire_api":"newer"}}}]}`))
	if len(problems) != 0 {
		t.Errorf("problems: %v", problems)
	}
	if len(skipped) != 3 || !strings.Contains(skipped[0], "unknown kind") ||
		!strings.Contains(skipped[1], "unknown via") || !strings.Contains(skipped[2], "unknown wire_api") {
		t.Errorf("want the kind, the via and the wire_api skipped, in order: %q", skipped)
	}

	for _, manifest := range []string{
		`{"name":"p","contributes":[{"kind":"launch"}]}`,
		`{"name":"p","contributes":[{"kind":"autonomy","guarded":{"launch":[{"bin":"x"}]}},` +
			`{"kind":"autonomy","guarded":{"launch":[{"bin":"y"}]}}]}`,
	} {
		if _, problems, _ := DecodeForUse([]byte(manifest)); len(problems) == 0 {
			t.Errorf("the use read must still refuse %s", manifest)
		}
	}
}

// A pack-wide field is ignored and named, and so is an unknown field inside a known pack-wide one.
func TestDecodeForUseNamesAPackWideFieldItIgnores(t *testing.T) {
	m, problems, skipped := DecodeForUse([]byte(`{"name":"p","requires_yolo":">=1",
	  "needs":[{"pack":"wire-bridge","when_bins":["claude"],"when_os":"linux"}]}`))
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(skipped) != 2 || !strings.Contains(skipped[0], `"needs" is read without its unknown field "when_os"`) ||
		!strings.Contains(skipped[1], `unknown field "requires_yolo" is ignored`) {
		t.Errorf("want both fields named: %q", skipped)
	}
	if len(m.DeclaredNeeds()) != 1 {
		t.Errorf("the known part of needs must still read: %+v", m.DeclaredNeeds())
	}
}

// ON A MANIFEST THIS BUILD READS WHOLE, THE USE READ IS THE AUTHORING READ: the same manifest, the
// same problems, nothing skipped. Every pack yolo ships, plus a manifest with problems.
func TestDecodeForUseMatchesDecodeWhenNothingIsSkipped(t *testing.T) {
	manifests := map[string][]byte{
		"malformed": []byte(`{"name":"p","contributes":[{"kind":"skills"},{"kind":"files","from":"/etc"}]}`),
	}
	shipped, err := filepath.Glob(filepath.Join("..", "..", "packs", "*", ManifestName))
	if err != nil || len(shipped) == 0 {
		t.Fatalf("no shipped manifests found: %v", err)
	}
	for _, path := range shipped {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manifests[path] = data
	}
	for name, data := range manifests {
		strictM, strictP := Decode(data)
		useM, useP, skipped := DecodeForUse(data)
		if len(skipped) != 0 {
			t.Errorf("%s: skipped %q from a manifest this build reads whole", name, skipped)
		}
		if !reflect.DeepEqual(strictP, useP) {
			t.Errorf("%s: problems differ:\n strict %q\n use    %q", name, strictP, useP)
		}
		if !reflect.DeepEqual(strictM, useM) {
			t.Errorf("%s: the two reads decoded different manifests", name)
		}
	}
}

// THE JAIL READS IT THE SAME WAY (DecodeTolerant), so the boot renders what the launch said it
// would: the same lines and the same contributions kept, over a manifest of every class both skip.
func TestTheJailsTolerantReadSkipsWhatTheUseReadSkips(t *testing.T) {
	manifest := []byte(`{"name":"p","newer_top":1,"contributes":[
	  {"kind":"skills","from":"skills","into":".p/skills","newer":1},
	  {"kind":"blocked-tool","bin":"grep","message":"m","suggestion":"rg","newer":1},
	  {"kind":"kind-from-a-newer-yolo"},
	  {"kind":"program","bin":"t","via":"uv","package":"t"},
	  {"kind":"env","vars":{"A":"1"}}]}`)
	useM, _, useSkipped := DecodeForUse(manifest)
	jailM, _, jailSkipped := DecodeTolerant(manifest)
	if !reflect.DeepEqual(useSkipped, jailSkipped) {
		t.Errorf("the two reads say different things:\n use  %q\n jail %q", useSkipped, jailSkipped)
	}
	if !reflect.DeepEqual(useM.Contributions(), jailM.Contributions()) {
		t.Errorf("the two reads keep different contributions:\n use  %+v\n jail %+v",
			useM.Contributions(), jailM.Contributions())
	}
}
