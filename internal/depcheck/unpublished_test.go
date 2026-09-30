package depcheck

import (
	"errors"
	"strings"
	"testing"
)

// AN UNPUBLISHED BINARY IS PROBED, NEVER REMEDIED: absent, it gets no remedy (the pack's own
// installer included), no bundle line and no place in Missing; present — a build the user made
// themselves — it is simply present. The reason rides onto the result for the report.
func TestCheckOffersNoRemedyForAnUnpublishedBinary(t *testing.T) {
	orig, origM := LookPath, DetectManager
	t.Cleanup(func() { LookPath, DetectManager = orig, origM })
	DetectManager = func(Lookup) string { return "brew" }
	LookPath = func(bin string) (string, error) {
		if bin == "built" {
			return "/usr/local/bin/built", nil
		}
		return "", errors.New("not found")
	}
	const why = "no build here"
	res := Check([]Requirement{
		{Bin: "gone", SelfInstall: "curl -fsSL https://x/i.sh | sh",
			Hints: map[string]string{"brew": "gone"}, Unpublished: why},
		{Bin: "built", SelfInstall: "curl -fsSL https://x/i.sh | sh", Unpublished: why},
		{Bin: "control", Hints: map[string]string{"brew": "control"}},
	}, nil)
	by := map[string]Result{}
	for _, r := range res {
		by[r.Bin] = r
	}
	if g := by["gone"]; g.Present || g.Remedy != "" || g.Fallback != "" || g.Unpublished != why {
		t.Errorf("an absent unpublished binary = %+v, want no remedy and the reason", g)
	}
	if b := by["built"]; !b.Present || b.Unpublished != "" {
		t.Errorf("a present binary is present whatever the vendor publishes: %+v", b)
	}
	missing := Missing(res)
	if len(missing) != 1 || missing[0].Bin != "control" {
		t.Errorf("Missing = %+v, want the control alone", missing)
	}
	if _, body := Manifest(res); body == "" || containsLine(body, "gone") {
		t.Errorf("the bundle must carry the control and not the unpublished binary:\n%s", body)
	}
}

func containsLine(body, token string) bool {
	for _, f := range strings.Fields(body) {
		if f == token || f == `"`+token+`"` {
			return true
		}
	}
	return false
}
