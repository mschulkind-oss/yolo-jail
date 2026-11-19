package hostskills

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// PrintPackInspectionHeader keeps a configured tuple's authored position and roots
// visible even when several selections share its refresh handle.
func PrintPackInspectionHeader(pr richtext.Printer, entry config.PackEntry, index int) {
	mode, dir := "whole", ""
	if entry.Skills != nil {
		mode = "typed"
		dir = entry.Skills.Dir
		if dir != "" {
			mode = "raw"
		}
	}
	pr.Printf("[bold]packs[%d] %s[/bold]: origin=%s subdir=%q skills.dir=%q mode=%s", index, richtext.Escape(entry.Name), richtext.Escape(entry.ConfigOrigin), richtext.Escape(entry.Subdir), richtext.Escape(dir), mode)
}
