package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real child reaches the actual command constructor after outer admission;
// removing its active marker must not reinterpret the run as nonfixture mode.
func TestPrivateSuiteLostMarkerRefusesEveryCommandConstructor(t *testing.T) {
	requireJail(t)
	if !privateSuite.armed {
		t.Skip("requires explicit private suite receipt wrapper")
	}
	home := os.Getenv("HOME")
	dir := writeProject(t, `{}`)
	tripwire := filepath.Join(dir, "vendor-executable-attempts")
	for _, route := range []string{"central", "background", "terminal", "keeper-terminal"} {
		t.Run(route, func(t *testing.T) {
			marker := filepath.Join(home, ".private-official-producer")
			raw, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(marker, raw, 0600); err != nil {
					t.Error(err)
				}
			}()
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(self, "-test.short", "-test.run=^TestPrivateSuiteLostMarkerConstructorChild$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PRIVATE_NEGATIVE_ROUTE="+route, "PRIVATE_NEGATIVE_MACHINE="+privateSuite.machine, "PRIVATE_NEGATIVE_TRIPWIRE="+tripwire)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "private suite active HOME adapter missing") {
				t.Fatalf("%s did not refuse at actual constructor: %v\n%s", route, err, out)
			}
			if _, err := os.Lstat(tripwire); !os.IsNotExist(err) {
				t.Fatalf("%s attempted the ordinary vendor executable: %v", route, err)
			}
		})
	}
}

func TestPrivateSuiteLostMarkerConstructorChild(t *testing.T) {
	route := os.Getenv("PRIVATE_NEGATIVE_ROUTE")
	if route == "" {
		return
	}
	privateSuite.armed, privateSuite.machine = true, os.Getenv("PRIVATE_NEGATIVE_MACHINE")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	yoloBin = filepath.Join(dir, "forbidden-vendor-cli")
	if err := os.WriteFile(yoloBin, []byte("#!/bin/sh\nprintf attempted >> '"+os.Getenv("PRIVATE_NEGATIVE_TRIPWIRE")+"'\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	switch route {
	case "central":
		runCommand(t, dir, []string{"run", "--", "true"})
	case "background":
		startYoloBackgroundWith(t, "lost-marker", dir, nil, "true")
	case "terminal":
		launchAtTerminal(t, dir, []string{"run", "--", "true"}, "")
	case "keeper-terminal":
		startYoloAtTerminal(t, "lost-marker", dir, "true")
	default:
		t.Fatal("unknown negative route")
	}
}
