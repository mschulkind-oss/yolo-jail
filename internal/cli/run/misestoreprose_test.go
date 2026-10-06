package run

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// misestoreprose_test.go ties the user-facing prose about the /mise tool store to the
// persistence map, which is computed from the same definitions as the mount argv.
//
// OQ-MB1 gave each Apple Container workspace its own tool disk while podman kept one
// machine-wide store. The commit that made the change updated two user-guide pages and
// missed two other places that state it: `yolo config-ref` and the per-setup matrix, which
// AGENTS.md names as the authority for every key per backend. Both went on saying every
// jail shares one /mise. Every sentence cannot be checked mechanically. This one can,
// because the scope of /mise per backend is a function (persistenceMapFor).

// miseClassFor is the class the persistence map gives /mise on runtime rt.
func miseClassFor(t *testing.T, rt string) jailcontent.PathClass {
	t.Helper()
	m := persistenceMapFor(rt, newConfig(), nil, "/ws", false)
	if m == nil {
		t.Fatalf("persistenceMapFor(%q) is nil; this test has lost its subject", rt)
	}
	for _, p := range m.Paths {
		if p.Path == "/mise" {
			return p.Class
		}
	}
	t.Fatalf("persistenceMapFor(%q) has no /mise entry; this test has lost its subject", rt)
	return 0
}

// configRefSection returns the text from heading to the next "[bold]" heading.
func configRefSection(t *testing.T, ref, heading string) string {
	t.Helper()
	i := strings.Index(ref, heading)
	if i < 0 {
		t.Fatalf("no %q section in config_ref.txt; this test has lost its subject and "+
			"would pass vacuously. Point it at wherever /mise is now explained.", heading)
	}
	rest := ref[i+len(heading):]
	if j := strings.Index(rest, "[bold]"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestConfigRefAgreesWithTheMiseStoreScope(t *testing.T) {
	b, err := os.ReadFile("../config_ref.txt")
	if err != nil {
		t.Fatalf("read config_ref.txt: %v", err)
	}
	ref := string(b)
	differ := miseClassFor(t, "podman") != miseClassFor(t, "container")

	for _, heading := range []string{
		"[bold]Home Directory (/home/agent)[/bold]",
		"[bold]Mise Tool Management[/bold]",
	} {
		section := configRefSection(t, ref, heading)
		if !strings.Contains(section, "/mise") {
			t.Fatalf("config_ref.txt's %s section no longer mentions /mise; repoint this "+
				"test at wherever it is explained", heading)
		}
		// Prose wraps, so compare with the whitespace collapsed.
		names := strings.Contains(strings.Join(strings.Fields(section), " "), "Apple Container")
		if differ && !names {
			t.Errorf("config_ref.txt's %s section describes /mise as one store for every "+
				"jail, but on Apple Container the persistence map gives each workspace its "+
				"own tool disk (OQ-MB1). Say that Apple Container keeps one per project:\n%s",
				heading, section)
		}
		if !differ && names {
			t.Errorf("config_ref.txt's %s section singles out Apple Container's /mise, but "+
				"the persistence map now gives /mise the same scope on both container "+
				"backends:\n%s", heading, section)
		}
	}
}

func TestSettingsPerSetupAgreesWithTheMiseStoreScope(t *testing.T) {
	const doc = "../../../userguide/reference/settings-per-setup.md"
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("read %s: %v", doc, err)
	}
	cells := func(line string) []string {
		parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}

	var header, row []string
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case header == nil && strings.HasPrefix(line, "| You want to…"):
			header = cells(line)
		case strings.HasPrefix(line, "| **Add language runtimes with mise**"):
			row = cells(line)
		}
	}
	if header == nil || row == nil {
		t.Fatalf("%s has lost its capability table header or its mise row; this test "+
			"would pass vacuously. Repoint it at wherever the mise store is described.", doc)
	}
	col := -1
	for i, h := range header {
		if strings.HasPrefix(h, "`container`") {
			col = i
		}
	}
	if col < 0 || col >= len(row) {
		t.Fatalf("no `container` column in %s's capability table (header %q, row %q)",
			doc, header, row)
	}
	cell := row[col]

	if miseClassFor(t, "podman") == miseClassFor(t, "container") {
		return
	}
	if strings.Contains(cell, "the same") || !strings.Contains(cell, "project") {
		t.Errorf("%s's Apple Container cell for mise says %q, but the persistence map "+
			"gives each Apple Container workspace its own tool disk while podman shares one "+
			"store across projects (OQ-MB1). The cell must say the store is per project.",
			doc, cell)
	}
}
