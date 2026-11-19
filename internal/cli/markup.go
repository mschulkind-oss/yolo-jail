package cli

import "github.com/mschulkind-oss/yolo-jail/internal/richtext"

// renderMarkup renders the briefing's rich tags to ANSI (color) or strips them
// (plain) through the shared richtext renderer.
func renderMarkup(s string, color bool) string {
	return richtext.Render(s, color)
}
