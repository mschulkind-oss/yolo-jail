package hostskills

import "testing"

func TestLaunchNamesTruncatesWithPlusMore(t *testing.T) {
	if got := launchNames([]string{"a", "b", "c"}); got != "a, b, c" {
		t.Fatalf("three names: %q", got)
	}
	if got := launchNames([]string{"a", "b", "c", "d", "e"}); got != "a, b, c, +2 more" {
		t.Fatalf("five names: %q", got)
	}
	if got := launchNames(nil); got != "none" {
		t.Fatalf("no names: %q", got)
	}
	if got := shortCommit("e435a22deadbeef"); got != "e435a22" {
		t.Fatalf("short commit: %q", got)
	}
}
