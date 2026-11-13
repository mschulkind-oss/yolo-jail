package config

// mountsoptional_test.go pins the object form's `optional` field
// (docs/reference/context-mounts.md#optional-mounts) at the call sites a launch and `yolo check`
// run: ValidateConfig and LoadRWMounts. "Optional" means "this backend may not be able to
// deliver it"; it never means "unsafe is fine", so the refusal set still refuses an optional
// element.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

func TestAnOptionalObjectElementValidatesAndParsesAsOptional(t *testing.T) {
	h := newMountsHost(t)
	src := mountSourceDir(t, "logs")
	h.user(t, `{"mounts": ["`+src+`", {"host": "`+src+`", "at": "/ctx/l2", "mode": "ro", "optional": true},
	  {"host": "`+src+`", "at": "/ctx/l3", "mode": "ro", "optional": false}]}`)
	if errs, _ := h.validate(t); len(errs) != 0 {
		t.Fatalf("a well-formed optional element failed validation: %q", errs)
	}
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	got := ParseMounts(cfg)
	if len(got) != 3 || got[0].Optional || !got[1].Optional || got[2].Optional {
		t.Fatalf("ParseMounts optional flags = %+v, want [false true false]", got)
	}
}

func TestAMalformedOptionalIsRefused(t *testing.T) {
	src := mountSourceDir(t, "logs")
	for _, tc := range []struct{ name, element, want string }{
		{"a string", `{"host": "` + src + `", "mode": "ro", "optional": "yes"}`, `"optional" must be true or false`},
		{"a typo beside it", `{"host": "` + src + `", "mode": "ro", "optionl": true}`, `(known: at, host, mode, optional, write)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			h.user(t, `{"mounts": [`+tc.element+`]}`)
			errs, _ := h.validate(t)
			if !strings.Contains(joined(errs), tc.want) {
				t.Fatalf("errors = %q, want one containing %q", errs, tc.want)
			}
		})
	}
}

// A MISSING SOURCE: a required element keeps its warning (CX-D24, unchanged); an optional one
// is not a validation finding at all, because the launch prints its one skip line and a
// warning beside it would say the same thing twice.
func TestAMissingOptionalSourceIsNotAValidationWarning(t *testing.T) {
	h := newMountsHost(t)
	gone := filepath.Join(h.home, "no", "such")
	h.user(t, `{"mounts": ["`+gone+`", {"host": "`+gone+`2", "mode": "ro", "optional": true}]}`)
	errs, warns := h.validate(t)
	if len(errs) != 0 {
		t.Fatalf("errors: %q", errs)
	}
	got := joined(warns)
	if !strings.Contains(got, "config.mounts[0]: host path does not exist") {
		t.Errorf("the required element lost its warning: %q", warns)
	}
	if strings.Contains(got, "config.mounts[1]") {
		t.Errorf("the optional element was warned about: %q", warns)
	}
}

// OPTIONAL NEVER SOFTENS THE REFUSAL SET: a read-write optional element whose source is the
// home still refuses, as a required one does.
func TestAnOptionalReadWriteMountOfTheHomeIsStillRefused(t *testing.T) {
	h := newMountsHost(t)
	h.user(t, `{"mounts": [{"host": "`+h.home+`", "mode": "rw", "optional": true}]}`)
	errs, _ := h.validate(t)
	if got := joined(errs); !strings.Contains(got, "is refused") || !strings.Contains(got, "IS your home directory") {
		t.Fatalf("an optional rw mount of the home: errors %q, want the credential-boundary refusal", errs)
	}
}

func TestLoadRWMountsCarriesOptional(t *testing.T) {
	h := newMountsHost(t)
	src := filepath.Join(h.home, "scratch")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	h.user(t, `{"mounts": [{"host": "`+src+`", "mode": "rw", "optional": true}]}`)
	rw, err := LoadRWMounts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rw) != 1 || !rw[0].Optional {
		t.Fatalf("LoadRWMounts = %+v, want one optional element", rw)
	}
}

// A `:optional` STRING SUFFIX is refused as `:rw` is, naming the object form, and the suggestion
// pasted back in validates.
func TestAnOptionalStringSuffixIsRefusedWithTheObjectForm(t *testing.T) {
	src := mountSourceDir(t, "lib")
	h := newMountsHost(t)
	h.user(t, `{"mounts": ["`+src+`:/ctx/lib:optional"]}`)
	errs, _ := h.validate(t)
	want := `yolo config add mounts '{ host = "` + src + `", at = "/ctx/lib", mode = "ro", optional = true }'`
	got := joined(errs)
	if !strings.Contains(got, `ends in ":optional"`) || !strings.Contains(got, want) {
		t.Fatalf("errors = %q, want the :optional refusal suggesting %s", errs, want)
	}
	h.user(t, `{"mounts": [`+mountAddedBy(t, got)+`]}`)
	if errs, _ := h.validate(t); len(errs) != 0 {
		t.Fatalf("the suggestion does not validate: %q", errs)
	}
}

// OptionalSpelling is a TOML value, not Go syntax: a host path with a quote, a backslash or a
// control byte still yields a value the edit verb parses back to the same path.
func TestOptionalSpellingIsValidTOML(t *testing.T) {
	host := "/tmp/a\"b\\c\x01d e"
	s := ContextMount{Host: host}.OptionalSpelling()
	doc, err := configdoc.Parse([]byte("value = "+s+"\n"), configdoc.TOML)
	if err != nil {
		t.Fatalf("OptionalSpelling = %s: %v", s, err)
	}
	v, _ := doc.Values.Get("value")
	back, problems := ParseMountElement(v, "value")
	if len(problems) != 0 || back.Host != host || !back.Optional {
		t.Fatalf("OptionalSpelling = %s parsed %+v, %q", s, back, problems)
	}
}

// The refusal's "mark it optional" fix is OptionalSpelling's output, and pasting it must
// validate and keep the element's destination and mode.
func TestOptionalSpellingValidatesAndKeepsTheElement(t *testing.T) {
	src := mountSourceDir(t, "lib")
	for _, el := range []string{`"` + src + `"`, `"` + src + `:/ctx/lib2"`,
		`{"host": "` + src + `", "at": "/ctx/lib3", "mode": "ro"}`} {
		h := newMountsHost(t)
		h.user(t, `{"mounts": [`+el+`]}`)
		cfg, err := LoadConfig(h.ws, true, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		m := ParseMounts(cfg)[0]
		spelled := MountAddCommand(m.OptionalSpelling())
		h.user(t, `{"mounts": [`+mountAddedBy(t, spelled)+`]}`)
		if errs, _ := h.validate(t); len(errs) != 0 {
			t.Fatalf("OptionalSpelling(%s) = %s does not validate: %q", el, spelled, errs)
		}
		cfg, _ = LoadConfig(h.ws, true, func(string) {})
		back := ParseMounts(cfg)[0]
		if !back.Optional || back.Host != m.Host || back.At != m.At || back.RW != m.RW {
			t.Fatalf("OptionalSpelling(%s) = %s parses as %+v, want %+v plus optional", el, spelled, back, m)
		}
	}
}
