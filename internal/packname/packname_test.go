package packname

import (
	"strings"
	"testing"
)

func TestNormalizeIsTheAgentFacingSpelling(t *testing.T) {
	for in, want := range map[string]string{
		"review":        "review",
		"Review_Code":   "review-code",
		"my__pack..x":   "my-pack-x",
		"-lead-trail-":  "lead-trail",
		"yolo-packs":    "yolo-packs",
		"日本":            "",
		"a  B  c":       "a-b-c",
		"matt-fzf":      "matt-fzf",
		"pi.fork/2":     "pi-fork-2",
		"UPPER--double": "upper-double",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWordRefusesAndNamesTheLegalSpelling(t *testing.T) {
	if err := Word("pack_sources key", "yolo-packs"); err != nil {
		t.Errorf("a legal key refused: %v", err)
	}
	err := Word("pack_sources key", "Yolo_Packs")
	if err == nil || !strings.Contains(err.Error(), `write "yolo-packs"`) {
		t.Errorf("Word(Yolo_Packs) = %v, want a refusal naming yolo-packs", err)
	}
	for _, bad := range []string{"", "-a", "a-", "a--b", "a.b"} {
		if Word("namespace", bad) == nil {
			t.Errorf("Word(%q) accepted", bad)
		}
	}
	if Word("namespace", strings.Repeat("a", MaxLen+1)) == nil {
		t.Error("a word past MaxLen accepted")
	}
}

func TestDeliveredJoinsWithOneHyphen(t *testing.T) {
	if got := Delivered("acme", "Review"); got != "acme-review" {
		t.Errorf("Delivered = %q", got)
	}
	if got := Delivered("", "fzf_pick"); got != "fzf_pick" {
		t.Errorf("bare Delivered = %q", got)
	}
	if TooLong(strings.Repeat("a", MaxLen), "p", "skill") != nil || TooLong(strings.Repeat("a", MaxLen+1), "p", "skill") == nil {
		t.Error("TooLong boundary wrong")
	}
}
