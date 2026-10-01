package manifest

import (
	"strings"
	"testing"
)

// The remedy's list names each config table a derive builds a named entry from, each once, with
// the kind of entry it holds.
func TestEntryKindHomesNamesEachSourceTableWithItsKind(t *testing.T) {
	got := EntryKindHomes()
	for kind, table := range map[string]string{
		"an MCP server": SourceMCPServers, "an LSP server": SourceLSPServers,
		"a provider": SourceProviders,
	} {
		if want := kind + " under `" + table + "`"; strings.Count(got, want) != 1 {
			t.Errorf("EntryKindHomes() = %q, want %q exactly once", got, want)
		}
	}
	if strings.HasSuffix(got, ".") {
		t.Errorf("EntryKindHomes() = %q ends a sentence; both remedies embed it mid-sentence", got)
	}
}
