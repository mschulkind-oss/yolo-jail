package manifest

import (
	"strings"
	"testing"
)

// goodSurface is a valid baseline; tests tweak one field to exercise validation.
func goodSurface() Surface {
	return Surface{
		Agent:    "pi",
		Name:     "settings",
		Path:     "~/.pi/agent/settings.json",
		Codec:    "json",
		Defaults: map[string]any{"theme": "system"},
		Managed:  map[string]any{"defaultProjectTrust": "always"},
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name      string
		surfaces  []Surface
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "single good surface loads",
			surfaces: []Surface{goodSurface()},
		},
		{
			name: "multiple distinct surfaces load",
			surfaces: []Surface{
				goodSurface(),
				{Agent: "claude", Name: "settings", Path: "~/.claude/settings.json", Codec: "json"},
				{Agent: "claude", Name: "config", Path: "~/.claude.json", Codec: "json"},
				{Agent: "codex", Name: "config", Path: "~/.codex/config.toml", Codec: "toml"},
			},
		},
		{
			name:     "empty manifest is valid",
			surfaces: nil,
		},
		{
			name: "empty path rejected",
			surfaces: []Surface{func() Surface {
				s := goodSurface()
				s.Path = ""
				return s
			}()},
			wantErr:   true,
			errSubstr: "empty Path",
		},
		{
			name: "whitespace-only path rejected",
			surfaces: []Surface{func() Surface {
				s := goodSurface()
				s.Path = "   "
				return s
			}()},
			wantErr:   true,
			errSubstr: "empty Path",
		},
		{
			name: "empty codec rejected",
			surfaces: []Surface{func() Surface {
				s := goodSurface()
				s.Codec = ""
				return s
			}()},
			wantErr:   true,
			errSubstr: "empty Codec",
		},
		{
			name: "unknown codec rejected",
			surfaces: []Surface{func() Surface {
				s := goodSurface()
				s.Codec = "xml"
				return s
			}()},
			wantErr:   true,
			errSubstr: "unknown Codec",
		},
		{
			name: "empty agent rejected",
			surfaces: []Surface{func() Surface {
				s := goodSurface()
				s.Agent = ""
				return s
			}()},
			wantErr:   true,
			errSubstr: "empty Agent",
		},
		{
			name: "empty name rejected",
			surfaces: []Surface{func() Surface {
				s := goodSurface()
				s.Name = ""
				return s
			}()},
			wantErr:   true,
			errSubstr: "empty Name",
		},
		{
			name: "duplicate (agent,name) rejected",
			surfaces: []Surface{
				goodSurface(),
				func() Surface {
					s := goodSurface()
					s.Path = "~/other.json" // differ elsewhere; same key
					return s
				}(),
			},
			wantErr:   true,
			errSubstr: "duplicate surface key",
		},
		{
			name: "same name different agent is not a duplicate",
			surfaces: []Surface{
				{Agent: "pi", Name: "settings", Path: "~/.pi/agent/settings.json", Codec: "json"},
				{Agent: "claude", Name: "settings", Path: "~/.claude/settings.json", Codec: "json"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.surfaces...)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New: expected error, got nil (manifest=%+v)", m)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("New: error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				if m != nil {
					t.Fatalf("New: expected nil manifest on error, got %+v", m)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: unexpected error: %v", err)
			}
			if m == nil {
				t.Fatal("New: nil manifest without error")
			}
			if got, want := m.Len(), len(tt.surfaces); got != want {
				t.Fatalf("Len = %d, want %d", got, want)
			}
		})
	}
}

func TestAllKnownCodecsAccepted(t *testing.T) {
	for _, codec := range CodecNames() {
		s := goodSurface()
		s.Codec = codec
		if _, err := New(s); err != nil {
			t.Errorf("codec %q rejected: %v", codec, err)
		}
	}
}

func TestLookupAndForAgent(t *testing.T) {
	m, err := New(
		Surface{Agent: "pi", Name: "settings", Path: "~/.pi/agent/settings.json", Codec: "json"},
		Surface{Agent: "claude", Name: "settings", Path: "~/.claude/settings.json", Codec: "json"},
		Surface{Agent: "claude", Name: "config", Path: "~/.claude.json", Codec: "json"},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if s, ok := m.Lookup("claude", "config"); !ok || s.Path != "~/.claude.json" {
		t.Fatalf("Lookup(claude,config) = %+v, %v", s, ok)
	}
	if _, ok := m.Lookup("nope", "settings"); ok {
		t.Fatal("Lookup of missing surface returned ok")
	}

	claude := m.ForAgent("claude")
	if len(claude) != 2 {
		t.Fatalf("ForAgent(claude) = %d surfaces, want 2", len(claude))
	}
	if got := m.ForAgent("ghost"); got != nil {
		t.Fatalf("ForAgent(ghost) = %v, want nil", got)
	}
}

func TestSurfacesIsCopy(t *testing.T) {
	m, err := New(goodSurface())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := m.Surfaces()
	got[0].Path = "MUTATED"
	if again := m.Surfaces(); again[0].Path == "MUTATED" {
		t.Fatal("Surfaces() exposed the manifest's backing slice")
	}
}

func TestSurfaceKeyString(t *testing.T) {
	if got := (SurfaceKey{Agent: "pi", Name: "settings"}).String(); got != "pi/settings" {
		t.Fatalf("SurfaceKey.String() = %q, want pi/settings", got)
	}
}
