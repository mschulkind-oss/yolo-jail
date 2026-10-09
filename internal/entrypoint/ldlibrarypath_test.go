package entrypoint

import (
	"os"
	"testing"
)

func TestScrubLegacyLDLibraryPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantVars bool
		wantVal  string
	}{
		{
			name:     "legacy baked multilib string is unset",
			input:    "/lib:/usr/lib:/usr/lib/x86_64-linux-gnu",
			wantVars: false,
		},
		{
			name:     "bare lib and usr lib are unset",
			input:    "/lib:/usr/lib",
			wantVars: false,
		},
		{
			name:     "empty remains unset",
			input:    "",
			wantVars: false,
		},
		{
			name:     "store packages lib is preserved while legacy paths stripped",
			input:    "/run/yolo/packages/lib:/lib:/usr/lib",
			wantVars: true,
			wantVal:  "/run/yolo/packages/lib",
		},
		{
			name:     "custom path is preserved",
			input:    "/opt/custom/lib",
			wantVars: true,
			wantVal:  "/opt/custom/lib",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEnv(map[string]string{})
			if tc.input != "" {
				e.Vars["LD_LIBRARY_PATH"] = tc.input
				t.Setenv("LD_LIBRARY_PATH", tc.input)
			} else {
				_ = os.Unsetenv("LD_LIBRARY_PATH")
			}

			scrubLegacyLDLibraryPath(e)

			val, hasVar := e.Vars["LD_LIBRARY_PATH"]
			if hasVar != tc.wantVars {
				t.Errorf("e.Vars[LD_LIBRARY_PATH] present = %v, want %v", hasVar, tc.wantVars)
			}
			if tc.wantVars && val != tc.wantVal {
				t.Errorf("e.Vars[LD_LIBRARY_PATH] = %q, want %q", val, tc.wantVal)
			}
			osVal, hasEnv := os.LookupEnv("LD_LIBRARY_PATH")
			if hasEnv != tc.wantVars {
				t.Errorf("os.LookupEnv(LD_LIBRARY_PATH) present = %v, want %v", hasEnv, tc.wantVars)
			}
			if tc.wantVars && osVal != tc.wantVal {
				t.Errorf("os.Getenv(LD_LIBRARY_PATH) = %q, want %q", osVal, tc.wantVal)
			}
		})
	}
}
