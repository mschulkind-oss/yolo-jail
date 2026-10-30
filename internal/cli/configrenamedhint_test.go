package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

const jsoncProject = "{\n  // keep project\n}\n"

// The readable-config schema reads its names from a legacy JSONC file as well (a legacy
// `packages` is lowered to install.nix on read), so the replacement a refusal names is a key the
// JSONC target can hold, and typing it works there.
func TestRenamedKeyHintNamesAKeyTheJSONCTargetCanHold(t *testing.T) {
	_, ws := genericFixture(t, "", configdoc.JSONC, jsoncProject)
	rc, _, errs := runConfigVerb(t, "add", "packages", "ripgrep")
	if rc == 0 || !strings.Contains(errs, "install.nix") {
		t.Fatalf("want a refusal naming install.nix, got rc=%d stderr=%q", rc, errs)
	}
	if got := readFile(t, ws.Path); got != jsoncProject {
		t.Fatalf("a refusal wrote the file:\n%s", got)
	}

	// The name the hint gave is accepted on that same JSONC target and read back from it.
	_, out, errs := runConfigVerb(t, "add", "install.nix", "ripgrep")
	if !strings.Contains(out, "Written") {
		t.Fatalf("the hinted key was not written: stdout=%q stderr=%q", out, errs)
	}
	got := assertValidFile(t, ws.Path, configdoc.JSONC, "// keep project", ws.Root)
	if !strings.Contains(got, `"install"`) || !strings.Contains(got, "ripgrep") {
		t.Fatalf("item not written:\n%s", got)
	}
	_, why, _ := runConfigVerb(t, "why", "install.nix")
	if !strings.Contains(why, "ripgrep") {
		t.Fatalf("the JSONC file's install.nix is not read back: %q", why)
	}
}

func TestConfigAddInstallNixBesideALegacyPackagesKeyKeepsBoth(t *testing.T) {
	_, ws := genericFixture(t, "", configdoc.JSONC, "{\n  \"packages\": [\"a\"]\n}\n")
	runConfigVerb(t, "add", "install.nix", "b")
	if got := readFile(t, ws.Path); !strings.Contains(got, `"packages": ["a"]`) {
		t.Fatalf("the legacy key was lost:\n%s", got)
	}
	_, dump, _ := runConfigVerb(t, "dump")
	if !strings.Contains(dump, `"a"`) || !strings.Contains(dump, `"b"`) {
		t.Fatalf("the effective config lost an item: %s", dump)
	}
}

// The legacy roots with no one-line rename, and a path under a moved root, name their
// replacement on either format.
func TestConfigSetOnARenamedLegacyRootNamesTheReplacement(t *testing.T) {
	for _, format := range []configdoc.Format{configdoc.TOML, configdoc.JSONC} {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"set", "resources.cpus", "2"}, "machine.resources.cpus"},
			{[]string{"set", "loopholes.audio.enabled", "true"}, "yolo loopholes enable"},
			{[]string{"set", "loopholes.audio.settings.x", "1"}, "allow.audio.<setting>"},
			{[]string{"set", "profiles.x.model", "m"}, "providers.<name>"},
		} {
			name := "toml"
			start := "# keep project\n"
			if format == configdoc.JSONC {
				name, start = "jsonc", jsoncProject
			}
			t.Run(name+" "+strings.Join(tc.args, " "), func(t *testing.T) {
				_, ws := genericFixture(t, "", format, start)
				rc, out, errs := runConfigVerb(t, tc.args...)
				if rc == 0 || !strings.Contains(errs, tc.want) {
					t.Fatalf("want a refusal naming %q, got rc=%d stdout=%q stderr=%q", tc.want, rc, out, errs)
				}
				if got := readFile(t, ws.Path); got != start {
					t.Fatalf("a refusal wrote the file:\n%s", got)
				}
			})
		}
	}
}
