package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// stagedslug_test.go pins Pack.StagedSlug — the key both halves mount a `reads-host`
// grant under — as a thing DISTINCT from Pack.Name.
//
// The two used to be one string, and the bug that separated them (measured 2026-09-05)
// was that they are not: the host loads a configured pack under its CONFIG name and the
// jail loads the same pack under its STAGED DIRECTORY, which is config.PackEntry.Slug's
// escaping of that name. For every pack yolo ships those are equal, so the divergence was
// invisible in-tree; a user pack named with a `_` or a space had its host file mounted
// where the entrypoint did not look, and the fail-open host-layer read composed defaults
// in silence.

// TestStagedSlugIsTheDirectoryNotTheName is the distinction in one measurement: a pack
// loaded the way the CLI loads a staged configured pack — root named by the slug, name
// carried from the config line — reports both, and they differ.
func TestStagedSlugIsTheDirectoryNotTheName(t *testing.T) {
	// `house_5frules` is what config.PackEntry.Slug makes of `house_rules`: `_` is
	// outside [A-Za-z0-9.-], so it is rewritten as `_5f`.
	root := filepath.Join(t.TempDir(), "house_5frules")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"house_rules"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, "house_rules")
	if len(problems) != 0 {
		t.Fatalf("LoadDir: %v", problems)
	}
	if p.Name != "house_rules" {
		t.Errorf("Name = %q, want house_rules — the name is the user's handle, the string "+
			"in their `packs` line that `yolo pack ls` prints", p.Name)
	}
	if p.StagedSlug() != "house_5frules" {
		t.Errorf("StagedSlug() = %q, want house_5frules — the directory the pack was "+
			"loaded from, which is the only string the jail can name a pack by", p.StagedSlug())
	}
	// And the /ctx destination is keyed on the DIRECTORY, so it is the one the jail —
	// which has nothing but that directory — will derive.
	got := CtxPath(p.StagedSlug(), packdecl.HostFile{From: ".acme/settings.json"})
	if got != "/ctx/host-house_5frules/settings.json" {
		t.Errorf("CtxPath = %q, want /ctx/host-house_5frules/settings.json", got)
	}
}

// TestEveryEmbeddedPackStagedSlugEqualsItsName is the "nothing shipped moves" measurement
// for keying the mount on the staged directory instead of the name.
//
// Every pack yolo ships is slug-clean, so Base(Root) and Name are the same string for all
// of them and no shipped mount path can move. Asserted rather than asserted-in-prose
// because it is what makes this change safe, and because a future pack with a `_` in its
// directory name would break the equality silently.
func TestEveryEmbeddedPackStagedSlugEqualsItsName(t *testing.T) {
	got, problems := MaterializeEmbedded(packs.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	if len(got) == 0 {
		t.Fatal("no embedded packs — this test would pass vacuously")
	}
	for _, p := range got {
		if p.StagedSlug() != p.Name {
			t.Errorf("pack %s stages as %q — a shipped pack whose staged directory is not "+
				"its name moves every /ctx mount its reads-host grants land at",
				p.Name, p.StagedSlug())
		}
		// The stronger property the equality rests on: the name survives the staging
		// escaping unchanged. `-` and `.` are safe, everything else is rewritten.
		for i := 0; i < len(p.Name); i++ {
			c := p.Name[i]
			ok := c == '.' || c == '-' ||
				(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
			if !ok {
				t.Errorf("pack %s has %q in its name, which config.PackEntry.Slug rewrites "+
					"as _%02x — the staged directory would stop matching the name",
					p.Name, string(c), c)
			}
		}
	}
}

// TestHostFileConflictsNamesThePackHandleNotItsStagedDir pins the ONE CtxPath call site
// that is deliberately keyed on Name.
//
// It is reporting, not a mount: both sides of its own comparison use the same key, so the
// collisions it finds are identical whichever string is passed, and the choice decides
// only what the message says. Name is what the user typed, and it is the only safe thing
// to print here — this check is lint-shaped, so its natural caller loads a pack out of a
// throwaway staging dir, where the staged slug names the temp tree.
func TestHostFileConflictsNamesThePackHandleNotItsStagedDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "house_5frules")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two grants, no `into`, same basename → one /ctx destination, which is the collision.
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"house_rules","contributes":[`+
			`{"kind":"reads-host","host":".acme/settings.json"},`+
			`{"kind":"reads-host","host":".other/settings.json"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, "house_rules")
	if len(problems) != 0 {
		t.Fatalf("LoadDir: %v", problems)
	}
	conflicts := p.HostFileConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want the one collision the fixture declares", conflicts)
	}
	if !strings.Contains(conflicts[0], "pack house_rules:") {
		t.Errorf("message %q does not name the pack by its handle — a user cannot map "+
			"a staged slug back to the line they wrote in `packs`", conflicts[0])
	}
}
