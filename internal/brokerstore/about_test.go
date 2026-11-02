package brokerstore

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every action is yolo's own word, only a-z and '-', so a notifier shows it as is.
func TestEveryActionIsLowercaseAndDashes(t *testing.T) {
	word := regexp.MustCompile(`^[a-z-]+$`)
	for _, a := range Actions {
		if !word.MatchString(a) || !SafeLabel(a) {
			t.Errorf("action %q is not [a-z-]", a)
		}
	}
}

// A broker's mistake never puts free text in a safe field: filing drops a safe field that is
// not SafeLabel, and an action not on the list.
func TestFilingDropsAnUnsafeSafeField(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "github"), Source: "github", Revalidate: unchangedPolicy}
	in := spendOrFileRequest()
	in.About = About{Action: "pr-<b>", Target: "PR o/r#1 <b>", Headline: "merge\u200b PR", Title: "<ok>", Refs: []string{"<ok>"}}
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if q.About.Action != "" || q.About.Target != "" || q.About.Headline != "" || q.About.Title != "<ok>" {
		t.Fatalf("about %+v", q.About)
	}
	if h := s.Headline(q); h != "a github request" {
		t.Fatalf("headline %q", h)
	}
	in.About = About{Action: "frobnicate"}
	in.Argv = []string{"pr", "comment", "9"}
	if q, err = s.File(in); err != nil || q.About.Action != "" {
		t.Fatalf("action %q, %v", q.About.Action, err)
	}
}

// The description is the headline, then the title and each ref on a line of its own; the
// agent's text is terminal-safe, so a newline in it cannot add a line.
func TestTheDescriptionPutsEachFreePartOnItsOwnLine(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "github"), Source: "github", Revalidate: unchangedPolicy}
	q := Request{About: About{Action: ActionPush, Target: "github.com/o/r", Headline: "push 2 refs to github.com/o/r",
		Title: "x\ny", Refs: []string{"refs/heads/a<b>", "refs/heads/ma\u200bin"}}}
	want := "push 2 refs to github.com/o/r\ntitle: x\\ny\nref: refs/heads/a<b>\nref: refs/heads/ma\\u200bin"
	if got := s.Description(q); got != want {
		t.Fatalf("description %q, want %q", got, want)
	}
	if got := Refs(q); got != "refs/heads/a<b>\nrefs/heads/ma\\u200bin" {
		t.Fatalf("refs %q", got)
	}
	if got := MarkupEscape(`<a href="x">&'`); got != "&lt;a href=&quot;x&quot;&gt;&amp;&#39;" {
		t.Fatalf("markup %q", got)
	}
	v := s.RequestView(q)
	if v.Headline != q.About.Headline || v.Description != want || v.Action != ActionPush || len(v.Refs) != 2 ||
		strings.ContainsRune(strings.Join(v.Refs, ""), '\u200b') {
		t.Fatalf("view %+v", v)
	}
}
