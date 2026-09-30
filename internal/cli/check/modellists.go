package check

// modellists.go reports what the `models` contribution kind could not do as written
// (docs/design/model-lists-and-pickers.md §7.2: "yolo check names the duplicate", and names an
// `only` id nothing added). The launch applies the kind silently — a duplicate keeps its first
// writer, an `only` id nobody added is dropped — because none of those refuses a launch; this
// is where an author or a user hears about them.
//
// NOT A SECOND COMPOSITION: it calls packload.ComposeProviders, the composition the launch
// runs, and reads its notes (packload.WithModelNotes), so the report and the table cannot
// disagree about what was applied.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// modelListNotes returns one warning per note the selected packs' `models` contributions
// produce over the merged config's `providers`, nil when there is none. A table that does not
// compose says nothing here: the pairing gate beside it already reports that.
func modelListNotes(packs []*packload.Pack, merged *jsonx.OrderedMap) []string {
	var notes []string
	if _, err := packload.ComposeProviders(subMap(merged, "providers"), packs,
		packload.WithModelNotes(func(n string) { notes = append(notes, "Model list: "+n) })); err != nil {
		return nil
	}
	return notes
}
