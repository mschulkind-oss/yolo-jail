package toolskills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const hk = "packslip:github.com/jdx/hk"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseListRealSample(t *testing.T) {
	got, err := ParseList(fixture(t, "ls-nonempty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "hk-configure" || got[1].Name != "hk-debug" {
		t.Fatalf("got %+v", got)
	}
	for _, s := range got {
		if s.Tool != hk || s.Version != "2.13.0" || !filepath.IsAbs(s.Path) {
			t.Fatalf("bad entry %+v", s)
		}
	}
}

func TestParseListEmptyArray(t *testing.T) {
	got, err := ParseList([]byte("[]\n"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParseListIgnoresUnknownFields(t *testing.T) {
	got, err := ParseList([]byte(`[{"name":"a","tool":"t","version":"1","path":"/x/a","extra":{"k":1},"n":2}]`))
	if err != nil || len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParseListRefusals(t *testing.T) {
	for name, in := range map[string]string{
		"relative path": `[{"name":"a","tool":"t","version":"1","path":"skills/a"}]`,
		"tilde path":    `[{"name":"a","tool":"t","version":"1","path":"~/a"}]`,
		"empty path":    `[{"name":"a","tool":"t","version":"1","path":""}]`,
		"no name":       `[{"tool":"t","version":"1","path":"/a"}]`,
		"no tool":       `[{"name":"a","version":"1","path":"/a"}]`,
		"object":        `{"name":"a"}`,
		"string":        `"x"`,
		"truncated":     `[{"name":"a","tool":"t"`,
		"table text":    "hk-configure  packslip:github.com/jdx/hk  2.13.0\n",
		"empty":         "",
		"wrong type":    `[{"name":1,"tool":"t","version":"1","path":"/a"}]`,
		"array of str":  `["a"]`,
	} {
		if got, err := ParseList([]byte(in)); err == nil {
			t.Errorf("%s: accepted %v", name, got)
		}
	}
}

func TestValidName(t *testing.T) {
	ok := []string{"hk-configure", "a", "Ünï", strings.Repeat("a", 128)}
	bad := []string{"", ".hidden", "..", "a/b", `a\b`, "a\x00b", "a\nb", "a\x1b[31m", "a\x7f", strings.Repeat("a", 129), "bad\xffutf8"}
	for _, n := range ok {
		if !ValidName(n) {
			t.Errorf("%q rejected", n)
		}
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestSanitizeNeverPassesControlCharacters(t *testing.T) {
	got := Sanitize("a\x1b[31mred\nline\x00" + strings.Repeat("z", 300))
	if strings.ContainsAny(got, "\x1b\n\x00") || len(got) > MaxNameBytes+3 {
		t.Fatalf("%q", got)
	}
}

func sk(tool, name string) Skill {
	return Skill{Name: name, Tool: tool, Version: "1", Path: "/m/" + tool + "/" + name}
}

func TestSelectFiltersByExactToolAndOrdersByAllowlistThenName(t *testing.T) {
	all := []Skill{
		sk("b", "z"), sk("a", "y"), sk("c", "unlisted"), sk("a", "x"), sk("b", "a"),
		sk("a-extra", "prefix"), sk("A", "case"),
	}
	sel := Select(all, []string{"a", "b"})
	var got []string
	for _, s := range sel.Skills {
		got = append(got, s.Tool+"/"+s.Name)
	}
	want := []string{"a/x", "a/y", "b/a", "b/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Reversing the allowlist reverses the tools, never mise's order.
	sel = Select(all, []string{"b", "a"})
	if sel.Skills[0].Tool != "b" || sel.Skills[len(sel.Skills)-1].Tool != "a" {
		t.Fatalf("allowlist order ignored: %+v", sel.Skills)
	}
}

func TestSelectIsIndependentOfInputOrder(t *testing.T) {
	a := []Skill{sk("a", "1"), sk("b", "2"), sk("a", "3")}
	b := []Skill{sk("a", "3"), sk("a", "1"), sk("b", "2")}
	if !reflect.DeepEqual(Select(a, []string{"a", "b"}), Select(b, []string{"a", "b"})) {
		t.Fatal("selection depends on mise's order")
	}
}

func TestSelectDuplicateNameFirstToolWinsAndLoserIsNamed(t *testing.T) {
	all := []Skill{sk("second", "dup"), sk("first", "dup"), sk("second", "own")}
	sel := Select(all, []string{"first", "second"})
	if len(sel.Skills) != 2 || sel.Skills[0].Tool != "first" || sel.Skills[1].Name != "own" {
		t.Fatalf("skills %+v", sel.Skills)
	}
	if len(sel.Shadowed) != 1 || sel.Shadowed[0].Skill.Tool != "second" || sel.Shadowed[0].Winner != "first" {
		t.Fatalf("shadowed %+v", sel.Shadowed)
	}
}

func TestSelectSkipsInvalidNamesAndSanitizesTheReport(t *testing.T) {
	all := []Skill{sk(hk, "good"), sk(hk, "../escape"), sk(hk, "evil\x1b[2J"), sk(hk, ".dot"), sk("other", "../x")}
	sel := Select(all, []string{hk})
	if len(sel.Skills) != 1 || sel.Skills[0].Name != "good" {
		t.Fatalf("skills %+v", sel.Skills)
	}
	if len(sel.Invalid) != 3 {
		t.Fatalf("invalid %+v", sel.Invalid)
	}
	for _, in := range sel.Invalid {
		if strings.ContainsAny(in.Name, "\x1b\n") {
			t.Fatalf("unsanitized %q", in.Name)
		}
	}
}

func TestSelectEmptyAllowlistKeepsNothing(t *testing.T) {
	if sel := Select([]Skill{sk("a", "x")}, nil); len(sel.Skills) != 0 {
		t.Fatalf("%+v", sel)
	}
}

func TestParseWarningsRealSample(t *testing.T) {
	w := ParseWarnings(string(fixture(t, "ls-missing.stderr")))
	if len(w) != 1 || w[0].Tool != hk || w[0].Version != "2.13.0" || w[0].Skill != "hk-debug" {
		t.Fatalf("%+v", w)
	}
	if !strings.Contains(w[0].Line, "reinstall the tool") {
		t.Fatalf("line %q", w[0].Line)
	}
}

func TestParseWarningsToleratesUnknownText(t *testing.T) {
	in := "mise WARN  a new version is available\nrandom\n\x1b[33mmise WARN\x1b[0m  t@1 declares a skill s that is not installed: why\n\x00\xff\n"
	w := ParseWarnings(in)
	if len(w) != 1 || w[0].Tool != "t" || w[0].Skill != "s" {
		t.Fatalf("%+v", w)
	}
	if got := ParseWarnings(""); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestMissingSampleListsOnlyTheInstalledSkill(t *testing.T) {
	got, err := ParseList(fixture(t, "ls-missing.json"))
	if err != nil || len(got) != 1 || got[0].Name != "hk-configure" {
		t.Fatalf("%v %v", got, err)
	}
}
