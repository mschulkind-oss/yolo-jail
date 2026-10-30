package integration

import (
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestMesaGraphicsDriverReachesContainerBootAndCheck is the actual-call-site
// companion to the isolated boot and check tests. It needs no GPU device: it
// verifies that the selected Nix outputs and check report reach a nested jail.
func TestMesaGraphicsDriverReachesContainerBootAndCheck(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("the nested container boot integration needs Linux Podman")
	}

	// nixpkgs has a 32-bit package set only on the x86 family: on aarch64,
	// pkgsi686Linux throws at evaluation, so the 32-bit driver is checked on amd64 alone.
	packages, arches, paths := `["mesa"]`, []string{"64"}, "/run/opengl-driver/lib /run/opengl-driver/share"
	if goruntime.GOARCH == "amd64" {
		packages, arches = `["mesa","pkgsi686Linux.mesa"]`, []string{"64", "32"}
		paths += " /run/opengl-driver-32/lib /run/opengl-driver-32/share"
	}
	dir := writeProject(t, `{"gpu":{"enabled":true},"packages":`+packages+`}`)
	r := runYolo(t, dir, strings.Join([]string{
		`echo GRAPHICS-MANIFEST-BEGIN`,
		`cat /run/yolo/graphics-drivers.json`,
		`echo GRAPHICS-MANIFEST-END`,
		`for p in ` + paths + `; do test -e "$p" || { echo "MISSING-GRAPHICS-PATH=$p"; exit 1; }; done`,
		`yolo check --no-build`,
	}, "; "))
	if r.rc != 0 {
		t.Fatalf("Mesa graphics boot/check integration failed (rc %d):\n%s", r.rc, r.combined())
	}

	out := r.combined()
	manifestLine := betweenMarkers(out, "GRAPHICS-MANIFEST-BEGIN", "GRAPHICS-MANIFEST-END")
	var manifest struct {
		Drivers []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Arch    string `json:"arch"`
			Lib     string `json:"lib"`
			Share   string `json:"share"`
		} `json:"drivers"`
	}
	if err := json.Unmarshal([]byte(manifestLine), &manifest); err != nil {
		t.Fatalf("decode actual runtime graphics manifest %q: %v", manifestLine, err)
	}
	byArch := make(map[string]struct {
		name, version, lib, share string
	})
	for _, driver := range manifest.Drivers {
		byArch[driver.Arch] = struct {
			name, version, lib, share string
		}{driver.Name, driver.Version, driver.Lib, driver.Share}
	}
	for _, arch := range arches {
		driver, ok := byArch[arch]
		if !ok || driver.name != "mesa" || driver.version == "" || driver.lib == "" || driver.share == "" {
			t.Fatalf("actual %s-bit Mesa runtime record = %+v, want resolved lib/share outputs: %+v", arch, driver, manifest.Drivers)
		}
		tree := "/run/opengl-driver"
		if arch == "32" {
			tree = "/run/opengl-driver-32"
		}
		for _, want := range []string{
			fmt.Sprintf("Selected driver mesa %s exposed at %s", driver.version, tree),
			"lib -> " + driver.lib,
			"share -> " + driver.share,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("actual in-jail yolo check report missing %q:\n%s", want, out)
			}
		}
	}
}

// TestGraphicsDriverBootIsANoopWhenDisabledOrUnselected reaches container boot
// with both no-op states and proves neither leaves runtime provenance or trees.
func TestGraphicsDriverBootIsANoopWhenDisabledOrUnselected(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("the nested container boot integration needs Linux Podman")
	}

	for _, tc := range []struct {
		name   string
		config string
	}{
		{name: "disabled", config: `{"gpu":{"enabled":false},"packages":["mesa"]}`},
		{name: "no driver selected", config: `{"gpu":{"enabled":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, tc.config)
			r := runYolo(t, dir, `set -eu; for p in /run/yolo/graphics-drivers.json /run/opengl-driver /run/opengl-driver-32; do if test -e "$p" || test -L "$p"; then echo "UNEXPECTED-GRAPHICS-PATH=$p"; exit 1; fi; done; echo GRAPHICS-NOOP-OK`)
			if r.rc != 0 || !strings.Contains(r.stdout, "GRAPHICS-NOOP-OK") {
				t.Fatalf("%s graphics boot did not leave runtime trees absent (rc %d):\n%s", tc.name, r.rc, r.combined())
			}
		})
	}
}

func betweenMarkers(text, start, end string) string {
	startAt := strings.Index(text, start)
	if startAt < 0 {
		return ""
	}
	startAt += len(start)
	if newline := strings.IndexByte(text[startAt:], '\n'); newline >= 0 {
		startAt += newline + 1
	}
	endAt := strings.Index(text[startAt:], end)
	if endAt < 0 {
		return ""
	}
	return strings.TrimSpace(text[startAt : startAt+endAt])
}
