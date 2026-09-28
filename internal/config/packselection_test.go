package config

import (
	"errors"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func selectionNames(packs []*packload.Pack) []string {
	var out []string
	for _, p := range packs {
		out = append(out, p.Name)
	}
	return out
}

// embeddedResolve resolves an embedded entry to the embedded pack of its name.
func embeddedResolve(e PackEntry) (*packload.Pack, error) {
	return embeddedPackNamed(e.Name)
}

// The closure's additions follow the configured packs, each announced before it is joined, and
// each joined through Join.
func TestSelectPacksClosesTheSelectionAndJoinsEachAddition(t *testing.T) {
	var events []string
	sel, err := SelectPacks([]PackEntry{EmbeddedPackEntry("claude")}, PackSelectSpec{
		Resolve:  embeddedResolve,
		Announce: func(cause string) { events = append(events, "announce "+cause) },
		Join: func(p *packload.Pack) (*packload.Pack, error) {
			events = append(events, "join "+p.Name)
			return p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude", "aws-auth", "openai-auth", "wire-bridge"}
	if got := selectionNames(sel.Packs()); !slices.Equal(got, want) {
		t.Fatalf("Packs() = %v, want %v", got, want)
	}
	if len(sel.Causes) != 3 || !sel.Complete() {
		t.Errorf("causes = %v, complete = %v", sel.Causes, sel.Complete())
	}
	wantEvents := []string{
		"announce + aws-auth (needed by claude)", "join aws-auth",
		"announce + openai-auth (needed by claude)", "join openai-auth",
		"announce + wire-bridge (needed by claude)", "join wire-bridge",
	}
	if !slices.Equal(events, wantEvents) {
		t.Errorf("events = %v, want %v", events, wantEvents)
	}
}

// Without FailFast every failure is recorded and the rest still resolves; with it, the first
// failure is the error and nothing past it is resolved.
func TestSelectPacksFailFastIsTheCallersChoice(t *testing.T) {
	boom := errors.New("boom")
	entries := []PackEntry{{Name: "broken"}, EmbeddedPackEntry("codex")}
	resolve := func(e PackEntry) (*packload.Pack, error) {
		if e.Name == "broken" {
			return nil, boom
		}
		return embeddedResolve(e)
	}
	sel, err := SelectPacks(entries, PackSelectSpec{Resolve: resolve})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Unresolved) != 1 || !errors.Is(sel.Unresolved[0].Err, boom) || sel.Complete() {
		t.Errorf("unresolved = %+v", sel.Unresolved)
	}
	if got := selectionNames(sel.Packs()); !slices.Equal(got, []string{"codex", "openai-auth"}) {
		t.Errorf("the resolvable part = %v, want codex and its need", got)
	}

	resolved := 0
	_, err = SelectPacks(entries, PackSelectSpec{FailFast: true, Resolve: func(e PackEntry) (*packload.Pack, error) {
		resolved++
		return resolve(e)
	}})
	if !errors.Is(err, boom) || resolved != 1 {
		t.Errorf("FailFast: err = %v after %d resolutions, want boom after 1", err, resolved)
	}
}

// A nil pack with no error contributes nothing and is not a failure.
func TestSelectPacksSkipsAnEntryThatContributesNothing(t *testing.T) {
	sel, err := SelectPacks([]PackEntry{EmbeddedPackEntry("claude")}, PackSelectSpec{
		Resolve: func(PackEntry) (*packload.Pack, error) { return nil, nil },
	})
	if err != nil || len(sel.Packs()) != 0 || !sel.Complete() {
		t.Errorf("sel = %+v, err = %v", sel, err)
	}
}
