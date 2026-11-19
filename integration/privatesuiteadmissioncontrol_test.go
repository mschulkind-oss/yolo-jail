package integration

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateSuiteCommandFactoryRefusesCorruptionAndExecutableIdentityDrift(t *testing.T) {
	previous, previousBin := privateSuite, yoloBin
	defer func() { privateSuite, yoloBin = previous, previousBin }()
	machine, home, root := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	yoloBin = filepath.Join(machine, "matched-native-cli")
	write(yoloBin, "#!/bin/sh\nexit 96\n", 0755)
	outer, marker, binding := filepath.Join(machine, privateSuiteMarker), filepath.Join(home, ".private-official-producer"), filepath.Join(root, "home-binding")
	write(outer, "private-suite-v1\n", 0600)
	write(marker, root, 0600)
	write(binding, home, 0600)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(root, "private-driver")
	original := "#!/bin/sh\nexit 97\n"
	write(driver, original, 0755)
	privateSuite.armed, privateSuite.machine, privateSuite.driver = true, machine, driver
	privateSuite.driverDigest = sha256.Sum256([]byte(original))
	t.Setenv("HOME", home)
	cfg := runConfig{}
	if name, _, err := privateLaunchCommandChecked(cfg, yoloBin, []string{"run", "--", "true"}); err != nil || name != driver {
		t.Fatalf("valid constructor policy: %s %v", name, err)
	}
	for _, mutation := range []string{"outer-marker", "active-marker", "home-binding", "driver-bytes", "driver-link", "unknown-prefix", "unknown-bin"} {
		t.Run(mutation, func(t *testing.T) {
			defer func() {
				_ = os.Remove(driver)
				write(driver, original, 0755)
				write(outer, "private-suite-v1\n", 0600)
				write(marker, root, 0600)
				write(binding, home, 0600)
			}()
			callCfg, bin := cfg, yoloBin
			switch mutation {
			case "outer-marker":
				write(outer, "corrupt", 0600)
			case "active-marker":
				write(marker, "relative", 0600)
			case "home-binding":
				write(binding, machine, 0600)
			case "driver-bytes":
				write(driver, "changed executable", 0755)
			case "driver-link":
				if err := os.Remove(driver); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(yoloBin, driver); err != nil {
					t.Fatal(err)
				}
			case "unknown-prefix":
				callCfg.prefix = []string{driver, "private-official-cli"}
			case "unknown-bin":
				bin = driver
			}
			if _, _, err := privateLaunchCommandChecked(callCfg, bin, []string{"run", "--", "true"}); err == nil || !strings.Contains(err.Error(), "private") {
				t.Fatalf("%s did not refuse before command construction: %v", mutation, err)
			}
		})
	}
}
