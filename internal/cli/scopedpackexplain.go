package cli

// Architecture and invariants: docs/reference/toml-scoped-pack-imports/README.md

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func printScopedPackInspection(out, errw io.Writer, entry config.PackEntry, color bool) bool {
	_, err := hostskills.InspectScopedPack(richtext.Printer{W: out, Color: color}, entry, nil)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack: %v\n", err)
		return false
	}
	return true
}

type indexedPackEntry struct {
	Index int
	Entry config.PackEntry
}

func explainPackEntries(entries []indexedPackEntry, out, errw io.Writer, color bool) int {
	pr := richtext.Printer{W: out, Color: color}
	rc := 0
	for _, match := range entries {
		index, entry := match.Index, match.Entry
		hostskills.PrintPackInspectionHeader(pr, entry, index)
		if entry.Skills != nil {
			if !printScopedPackInspection(out, errw, entry, color) {
				rc = 1
			}
		} else {
			if explainWholePack(entry, out, errw, color, true) != 0 {
				rc = 1
			}
		}
	}
	return rc
}
