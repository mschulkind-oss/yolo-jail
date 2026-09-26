package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPinReadmeLinks(t *testing.T) {
	const blob = "https://github.com/mschulkind-oss/yolo-jail/blob/v1.2.3/"
	const raw = "https://raw.githubusercontent.com/mschulkind-oss/yolo-jail/v1.2.3/"
	cases := []struct{ name, in, want string }{
		{"relative file", "See [the license](LICENSE).", "See [the license](" + blob + "LICENSE)."},
		{"relative doc with fragment", "[Storage](userguide/guides/storage.md#relocating)",
			"[Storage](" + blob + "userguide/guides/storage.md#relocating)"},
		{"dot-slash and root-relative", "[a](./flake.nix) [b](/go.mod)",
			"[a](" + blob + "flake.nix) [b](" + blob + "go.mod)"},
		{"bare fragment goes to the README on GitHub", "[Agents](#agents)",
			"[Agents](" + blob + "README.md#agents)"},
		{"absolute URL untouched", "[docs](https://docs.yolo-jail.mschulkind.dev)",
			"[docs](https://docs.yolo-jail.mschulkind.dev)"},
		{"mailto untouched", "[mail](mailto:x@example.invalid)", "[mail](mailto:x@example.invalid)"},
		{"protocol-relative untouched", "[x](//example.invalid/a)", "[x](//example.invalid/a)"},
		{"image gets a raw URL", "![diagram](docs/img/a.png)", "![diagram](" + raw + "docs/img/a.png)"},
		{"badge link: outer relative target, inner absolute image",
			"[![License](https://img.shields.io/badge/x.svg)](LICENSE)",
			"[![License](https://img.shields.io/badge/x.svg)](" + blob + "LICENSE)"},
		{"title kept", `[a](LICENSE "the license")`, `[a](` + blob + `LICENSE "the license")`},
		{"nested emphasis in text", "[**Claude Code**](docs/a.md)", "[**Claude Code**](" + blob + "docs/a.md)"},
		{"inline code is text", "run `[x](y)` then [z](LICENSE)", "run `[x](y)` then [z](" + blob + "LICENSE)"},
		{"link text that is itself code", "edit [`flake.nix`](flake.nix)", "edit [`flake.nix`](" + blob + "flake.nix)"},
		{"reference definition", "[lic]: LICENSE", "[lic]: " + blob + "LICENSE"},
		{"version with a v prefix", "[a](LICENSE)", "[a](" + blob + "LICENSE)"},
	}
	for _, c := range cases {
		version := "1.2.3"
		if strings.Contains(c.name, "v prefix") {
			version = "v1.2.3"
		}
		if got := pinReadmeLinks(c.in, version); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestPinReadmeLinksLeavesFencedCodeAlone(t *testing.T) {
	in := "before [a](LICENSE)\n```jsonc\n// see [b](NOTICE)\n```\n~~~\n[c](go.mod)\n~~~\nafter [d](go.sum)"
	got := pinReadmeLinks(in, "1.0.0")
	for _, keep := range []string{"[b](NOTICE)", "[c](go.mod)"} {
		if !strings.Contains(got, keep) {
			t.Errorf("a link inside a fenced block was rewritten; want %q kept:\n%s", keep, got)
		}
	}
	for _, pinned := range []string{"/blob/v1.0.0/LICENSE)", "/blob/v1.0.0/go.sum)"} {
		if !strings.Contains(got, pinned) {
			t.Errorf("a link outside the fences was not pinned; want %q:\n%s", pinned, got)
		}
	}
}

// TestTheRealReadmeKeepsNoRelativeLink runs the rewrite over the README that ships, so a
// link shape the unit cases above never imagined fails here rather than on pypi.org. The
// first draft missed every [`file`](file) link, which is most of this README's.
func TestTheRealReadmeKeepsNoRelativeLink(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	out := pinReadmeLinks(string(b), "9.9.9")
	target := regexp.MustCompile(`\]\(([^)\s]+)`)
	fenced := false
	for n, line := range strings.Split(out, "\n") {
		if fenceRe.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range target.FindAllStringSubmatch(line, -1) {
			if !strings.HasPrefix(m[1], "https://") && !strings.HasPrefix(m[1], "http://") {
				t.Errorf("README.md line %d still links to %q after pinning", n+1, m[1])
			}
		}
	}
}

// TestTheWheelsDescriptionHasNoRelativeLinks is the call-site pin: it builds a real wheel
// and reads METADATA back out of it, so it fails if buildWheel stops pinning the README as
// surely as if pinReadmeLinks breaks.
func TestTheWheelsDescriptionHasNoRelativeLinks(t *testing.T) {
	root := t.TempDir()
	readme := "# yolo\n\nSee [the license](LICENSE) and [Agents](#agents).\n"
	for name, body := range map[string]string{"README.md": readme, "LICENSE": "L\n", "NOTICE": "N\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	wheel, err := buildWheel(map[string][]byte{"yolo": []byte("binary")}, "2.0.0", "manylinux_2_17_x86_64", out, root)
	if err != nil {
		t.Fatalf("buildWheel: %v", err)
	}

	zr, err := zip.OpenReader(wheel)
	if err != nil {
		t.Fatalf("open %s: %v", wheel, err)
	}
	defer zr.Close()
	var metadata string
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".dist-info/METADATA") {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			metadata = string(b)
		}
	}
	if metadata == "" {
		t.Fatal("the wheel has no dist-info/METADATA")
	}
	for _, want := range []string{
		"(https://github.com/mschulkind-oss/yolo-jail/blob/v2.0.0/LICENSE)",
		"(https://github.com/mschulkind-oss/yolo-jail/blob/v2.0.0/README.md#agents)",
	} {
		if !strings.Contains(metadata, want) {
			t.Errorf("METADATA's description does not carry %s — PyPI would render a link "+
				"that 404s:\n%s", want, metadata)
		}
	}
	if strings.Contains(metadata, "](LICENSE)") || strings.Contains(metadata, "](#agents)") {
		t.Errorf("METADATA still carries a repository-relative link:\n%s", metadata)
	}
}
