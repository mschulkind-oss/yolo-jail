package cli

import (
	"strings"
	"testing"
)

// TestAtGuestNamesBothSeatbeltSetups: `--at guest` launched the Seatbelt sandbox as the yolo
// account unless the removed `macos_user_mode` said otherwise, so its refusal names
// seatbelt-yolo-account and seatbelt-self beside it, and no removed key.
func TestAtGuestNamesBothSeatbeltSetups(t *testing.T) {
	withHomeAndCwd(t)
	for verb, want := range map[string][]string{
		"":      {"yolo --setup seatbelt-yolo-account -- <cmd>", "yolo --setup seatbelt-self -- <cmd>"},
		"apply": {"yolo apply --setup seatbelt-yolo-account", "yolo apply --setup seatbelt-self"},
	} {
		got := atRemovedFor(verb, "guest").Error()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("verb %q: `--at guest` refusal = %q, want it to name %q", verb, got, w)
			}
		}
		if strings.Contains(got, "macos_user_mode") {
			t.Errorf("verb %q: the refusal names the removed key: %q", verb, got)
		}
	}
}
