package config

import (
	"strings"
	"testing"
)

// A `packages` entry is a nixpkgs ATTRIBUTE PATH and installs what
// `nix build nixpkgs#<entry>` builds (docs/design/package-nested-attribute-paths.md, OQ-1
// ruled (C) 2026-10-05). So `yolo check` must let through every path Nix can walk: a
// collection member, a member's output, and the texlive shape whose last name collides with
// one of its parent's outputs. Until that ruling the pattern allowed one dot, which made
// `rocmPackages.clr` a check error before the flake ever saw it.
//
// Driven through ValidateConfig, not validatePackages, and paired with the refusals below
// in one table: deleting the validatePackages call fails the refusal rows, and an accepting
// row cannot pass by a validator that refuses everything.
func TestPackagesEntryIsANixpkgsAttributePath(t *testing.T) {
	hostFloorHome(t)
	ws := t.TempDir()

	packageErrs := func(t *testing.T, cfg string) []string {
		t.Helper()
		errs, _ := ValidateConfig(decode(t, cfg), ws, nil)
		var out []string
		for _, e := range errs {
			if strings.HasPrefix(e, "config.packages") {
				out = append(out, e)
			}
		}
		return out
	}

	accepted := []struct{ why, cfg string }{
		{"a top-level package", `{"packages": ["strace"]}`},
		{"a top-level package's output", `{"packages": ["gtk4.dev"]}`},
		{"a collection member", `{"packages": ["rocmPackages.clr"]}`},
		{"a collection member's output", `{"packages": ["rocmPackages.clr.icd"]}`},
		{"a path three names deep whose leaf collides with an output",
			`{"packages": ["texlivePackages.abc.texsource"]}`},
		// 11 of the 84,949 member names in nixpkgs' package sets are not letters,
		// digits, '_' and '-' (measured 2026-10-06), and Nix spells them quoted.
		{"a quoted name with a character outside the bare set",
			`{"packages": ["nerd-fonts.\"m+\""]}`},
		{"a quoted name holding a dot", `{"packages": ["rubyPackages.\"http_parser.rb\""]}`},
		// The macos-user launch refusal tells the user to mark an entry Linux-only with
		// {"name": "<pkg>", "platforms": ["linux"]}. Every string entry must therefore be
		// spellable as an object name, or that advice is a dead end for exactly the
		// Linux-only collection (rocmPackages) this design exists for.
		{"an object naming a collection member",
			`{"packages": [{"name": "rocmPackages.clr", "platforms": ["linux"]}]}`},
		{"an object naming a member with outputs",
			`{"packages": [{"name": "gst_all_1.gstreamer", "outputs": ["out", "dev"]}]}`},
	}
	for _, c := range accepted {
		t.Run("accepts "+c.why, func(t *testing.T) {
			if errs := packageErrs(t, c.cfg); len(errs) != 0 {
				t.Errorf("%s was refused:\n%s", c.cfg, strings.Join(errs, "\n"))
			}
		})
	}

	refused := []struct{ why, cfg, want string }{
		{"an empty name between dots", `{"packages": ["rocmPackages..clr"]}`, "attribute path"},
		{"a leading dot", `{"packages": [".clr"]}`, "attribute path"},
		{"a trailing dot", `{"packages": ["rocmPackages."]}`, "attribute path"},
		{"a character no attribute name has", `{"packages": ["rocm Packages.clr"]}`, "attribute path"},
		{"an empty string", `{"packages": [""]}`, "attribute path"},
		{"an unclosed quote", `{"packages": ["vimPlugins.\"sourcemap.vim"]}`, "attribute path"},
		{"an empty quoted name", `{"packages": ["vimPlugins.\"\""]}`, "attribute path"},
		{"a quote inside a bare name", `{"packages": ["nerd-fonts.m\"+\""]}`, "attribute path"},
		{"an object name with an empty name between dots",
			`{"packages": [{"name": "rocmPackages..clr"}]}`, "attribute path"},
		{"an object name with a trailing dot",
			`{"packages": [{"name": "gtk4."}]}`, "attribute path"},
	}
	for _, c := range refused {
		t.Run("refuses "+c.why, func(t *testing.T) {
			errs := packageErrs(t, c.cfg)
			if len(errs) == 0 {
				t.Fatalf("%s was accepted; validatePackages no longer reaches it, or the "+
					"pattern lets an empty attribute name through", c.cfg)
			}
			if joined := strings.Join(errs, "\n"); !strings.Contains(joined, c.want) {
				t.Errorf("%s: the refusal does not say %q:\n%s", c.cfg, c.want, joined)
			}
		})
	}
}

// The refusal must say what an entry IS, so a user can fix it without opening the docs:
// a nixpkgs attribute path, with the three shapes spelled. The old wording ("at most one
// dot") is the rule this design deleted, and a message still saying it would be the lie.
func TestPackagesEntryRefusalNamesTheShapesItAccepts(t *testing.T) {
	hostFloorHome(t)
	errs, _ := ValidateConfig(decode(t, `{"packages": ["a..b"]}`), t.TempDir(), nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"'gtk4.dev'", "'rocmPackages.clr'", "nix build nixpkgs#"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not mention %s:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "at most one dot") {
		t.Errorf("the refusal still states the one-dot rule:\n%s", joined)
	}
}
