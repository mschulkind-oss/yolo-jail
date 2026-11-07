package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

// TestDecodeTOMLLaunchRowCarriesItsSubcommands pins the decode half of an autonomy launch
// row's `subcommands`: the list reaches the posture row a launch folds (PostureFor), in the
// table spelling packs/muse-code uses, for both postures.
func TestDecodeTOMLLaunchRowCarriesItsSubcommands(t *testing.T) {
	const data = `[[autonomy]]
replace = true

[[autonomy.autonomous.launch]]
bin = "muse"
flags = ["--yolo"]
subcommands = ["exec", "login"]

[[autonomy.guarded.launch]]
bin = "muse"
flags = ["--approval-mode", "on-request"]
subcommands = ["exec"]
`
	m, problems := DecodeTOML([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	for autonomy, want := range map[bool][]string{true: {"exec", "login"}, false: {"exec"}} {
		posture := m.PostureFor(autonomy)
		if posture == nil || len(posture.Launch) != 1 {
			t.Fatalf("PostureFor(%t) launch rows = %#v", autonomy, posture)
		}
		if got := posture.Launch[0].Subcommands; !reflect.DeepEqual(got, want) {
			t.Errorf("PostureFor(%t).Launch[0].Subcommands = %v, want %v", autonomy, got, want)
		}
	}
}

// TestLaunchSubcommandsAreRefusedWhenNoArgvWordCouldMatch is the authoring half: a word no
// argv[1] comparison can use, or a list on a row with nothing to exempt, is a manifest
// problem naming the row.
func TestLaunchSubcommandsAreRefusedWhenNoArgvWordCouldMatch(t *testing.T) {
	for _, c := range []struct {
		row  string
		want string
	}{
		{`flags = ["--yolo"]` + "\n" + `subcommands = [""]`, "is empty"},
		{`flags = ["--yolo"]` + "\n" + `subcommands = ["two words"]`, "holds whitespace"},
		{`flags = ["--yolo"]` + "\n" + `subcommands = ["--help"]`, "is a flag, not a subcommand word"},
		{`flags = ["--yolo"]` + "\n" + `subcommands = ["exec", "exec"]`, "named twice"},
		{`subcommands = ["exec"]`, "declares none"},
	} {
		data := "[[autonomy]]\n\n[[autonomy.autonomous.launch]]\nbin = \"muse\"\n" + c.row + "\n"
		_, problems := DecodeTOML([]byte(data))
		if !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("row %q: problems %q, want one containing %q", c.row, problems, c.want)
		}
	}
}
