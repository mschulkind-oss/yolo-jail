package check

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// runImageDeliveryInSection drives the WHOLE Container Image section, not
// reportImageDelivery directly, and that is the point: the decision table lives in
// internal/image and is tested there, so what is left to pin here is that the
// section actually CALLS it. Delete the one line in sectionContainerImage and
// these tests fail while every table row stays green — which is the failure shape
// AGENTS.md names ("does it fail if I delete the call site?").
func runImageDeliveryInSection(t *testing.T, rt string, hasSh bool,
	answer func(argv []string) ExecResult) (string, [][]string) {
	t.Helper()
	var out bytes.Buffer
	var seen [][]string
	exec := func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		seen = append(seen, append([]string(nil), argv...))
		return answer(argv)
	}
	o := &Options{
		Getenv:      func(string) string { return "" },
		Exec:        exec,
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
		PathExists:  func(string) bool { return hasSh },
	}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.Exec = exec
	o.PathExists = func(string) bool { return hasSh }
	// `podman info` is the readiness gate's to ask (podmanready.go): the fixture's answer
	// to it is what the gate hears, and Exec is never asked for it.
	podmanAnswers(o, answer(image.PodmanInfoCmd(rt)))
	r := newReporter(&out, false)
	// A store path is supplied so the section's image question is the cheap
	// single-inspect one; the delivery line is what these tests read.
	o.sectionContainerImage(r, rt, "hint", "/nix/store/delivery-image")
	return out.String(), seen
}

// rootlessInfo is `podman info --format json`, trimmed to the fields the
// delivery decision reads: the namespace answer and the store.
const rootlessInfo = `{"host":{"security":{"rootless":true}},"store":{` +
	`"configFile":"/usr/share/containers/storage.conf","graphDriverName":"overlay",` +
	`"graphRoot":"/home/u/.local/share/containers/storage","runRoot":"/run/user/1000/containers"}}`

// TestImageDeliverySectionNamesTheStoreALaunchWillWrite pins the section's call
// of image.StorePreflight. Issue #47's host passed the namespace probe and failed
// every launch, because the copier chose a different store; so the store is
// reported as its own line, and a podman that answered without naming one is a
// warning.
func TestImageDeliverySectionNamesTheStoreALaunchWillWrite(t *testing.T) {
	got, _ := runImageDeliveryInSection(t, "podman", true, func(argv []string) ExecResult {
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: rootlessInfo}
		}
		return ExecResult{Ran: true, RC: 0}
	})
	if !strings.Contains(got, "Image store: overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers") {
		t.Errorf("the section does not name podman's store:\n%s", got)
	}
	got, _ = runImageDeliveryInSection(t, "podman", true, func(argv []string) ExecResult {
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: `{"host":{"security":{"rootless":true}}}`}
		}
		return ExecResult{Ran: true, RC: 0}
	})
	if !strings.Contains(got, "WARN") || !strings.Contains(got, "cannot name podman's store") {
		t.Errorf("an unnamed store was not a warning:\n%s", got)
	}
}

// TestImageDeliverySectionWarnsWhenTheNamespaceIsRefused is the whole reason this
// preflight exists. On 2026-09-09 a rootless host that could not enter podman's
// user namespace was discovered by a launch that had already built a patched
// skopeo and started a multi-gigabyte copy; the same fact is now a line in a
// report that costs two subprocesses.
func TestImageDeliverySectionWarnsWhenTheNamespaceIsRefused(t *testing.T) {
	got, seen := runImageDeliveryInSection(t, "podman", true, func(argv []string) ExecResult {
		switch {
		case len(argv) >= 2 && argv[1] == "info":
			return ExecResult{Ran: true, RC: 0, Stdout: rootlessInfo}
		case len(argv) >= 2 && argv[1] == "unshare":
			return ExecResult{Ran: true, RC: 125,
				Stderr: "Error: cannot find UID/GID for user runner: no subuid ranges found"}
		}
		return ExecResult{Ran: true, RC: 0}
	})
	if !strings.Contains(got, "will fail") {
		t.Errorf("a refused namespace was not reported as a delivery problem:\n%s", got)
	}
	if !strings.Contains(got, "WARN") {
		t.Errorf("the delivery problem is not a warning:\n%s", got)
	}
	if !strings.Contains(got, "/etc/subuid") {
		t.Errorf("the report names no remedy:\n%s", got)
	}
	// The probe has to be the namespace the COPY will use, or it verifies something
	// else and reports it as this.
	var probed string
	for _, argv := range seen {
		if len(argv) >= 2 && argv[1] == "unshare" {
			probed = strings.Join(argv, " ")
		}
	}
	if probed != "podman unshare -- /bin/sh -c :" {
		t.Errorf("namespace probe = %q, want the one the copy uses", probed)
	}
}

// TestImageDeliverySectionReportsAWorkingRootlessRoute: the healthy rootless host
// gets a PASS naming the route, because a mode nobody can see is a mode nobody
// can debug — the same rule the launch's own "Store write:" line follows.
func TestImageDeliverySectionReportsAWorkingRootlessRoute(t *testing.T) {
	got, seen := runImageDeliveryInSection(t, "podman", true, func(argv []string) ExecResult {
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: rootlessInfo}
		}
		return ExecResult{Ran: true, RC: 0}
	})
	if !strings.Contains(got, "podman unshare") || !strings.Contains(got, "verified") {
		t.Errorf("a working rootless route was not reported:\n%s", got)
	}
	if strings.Contains(got, "WARN") {
		t.Errorf("a healthy host was warned at:\n%s", got)
	}
	// The store facts are the readiness gate's answer (PR-D6): the section asks podman
	// only the namespace question of its own.
	for _, argv := range seen {
		if len(argv) >= 2 && argv[1] == "info" {
			t.Errorf("the delivery section ran its own %v; it reads the gate's answer", argv)
		}
	}
}

// TestImageDeliverySectionSaysNothingItCannotProve covers the two silences, which
// are as much a decision as the warnings: a podman that will not answer has
// nothing to report here (the runtime section is already about it), and Apple
// Container delivers through an archive that needs no namespace at all.
func TestImageDeliverySectionSaysNothingItCannotProve(t *testing.T) {
	t.Run("podman would not answer", func(t *testing.T) {
		got, _ := runImageDeliveryInSection(t, "podman", true, func(argv []string) ExecResult {
			if len(argv) >= 2 && argv[1] == "info" {
				return ExecResult{Ran: false}
			}
			return ExecResult{Ran: true, RC: 0}
		})
		if strings.Contains(got, "Image delivery") || strings.Contains(got, "unshare") {
			t.Errorf("an unproven fact produced a delivery claim:\n%s", got)
		}
	})

	t.Run("apple container is never asked", func(t *testing.T) {
		_, seen := runImageDeliveryInSection(t, "container", true,
			func([]string) ExecResult { return ExecResult{Ran: true, RC: 0} })
		for _, argv := range seen {
			if len(argv) >= 2 && (argv[1] == "info" || argv[1] == "unshare") {
				t.Errorf("apple container was asked about podman's namespace: %v", argv)
			}
		}
	})
}
