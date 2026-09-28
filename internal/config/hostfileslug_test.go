package config

import "testing"

// HostFilePathFromSlug inverts Slug (OQ-NC8: a dropped entry is found by its record's slug), and
// refuses a string Slug cannot produce rather than guessing a path from it.
func TestHostFilePathFromSlugInvertsSlug(t *testing.T) {
	for _, p := range []string{".config/mytool/config.json", "a_b c/ü.toml", ".x20", "-a.b"} {
		slug := (HostFileEntry{Path: p}).Slug()
		got, ok := HostFilePathFromSlug(slug)
		if !ok || got != p {
			t.Errorf("HostFilePathFromSlug(%q) = %q, %v; want %q", slug, got, ok, p)
		}
	}
	for _, bad := range []string{"", "a/b", "_2", "_zz", "_2F"} {
		if got, ok := HostFilePathFromSlug(bad); ok {
			t.Errorf("HostFilePathFromSlug(%q) = %q, want a refusal", bad, got)
		}
	}
}
