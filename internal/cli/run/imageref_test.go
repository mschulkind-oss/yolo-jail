package run

import (
	"slices"
	"strings"
	"testing"
)

// goldenImageRef is the image ref every argv fixture in this package uses.
//
// It is deliberately CONTENT-SHAPED and deliberately not `:latest`. Since C2 the
// loaded image is named `<repo>:<sha16-of-store-path>`, so a fixture carrying the
// old constant would be satisfied by an assembler that ignored its input and
// re-derived that constant — which is the exact regression these tests exist to
// catch. The digits are nonsense on purpose: nothing may recognise this value,
// it may only be carried.
const goldenImageRef = "localhost/yolo-jail:d00dfeedcafe1234"

// TestAssembledArgvCarriesTheThreadedImageRef pins the CALL SITE at
// assemble.go's "--- image + entrypoint ---" line.
//
// The golden pins the whole argv including this element, but a golden can be
// hand-updated. This one cannot be satisfied by any constant: two inputs that
// differ ONLY in imageRef must produce argvs that differ ONLY at the image
// position. Revert assembly to a jailImageRef(rt)-style constant and the two
// argvs become identical, which fails here and nowhere else.
func TestAssembledArgvCarriesTheThreadedImageRef(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	const otherRef = "localhost/yolo-jail:0123456789abcdef"

	a := o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil))

	inB := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	inB.imageRef = otherRef
	b := o.assembleRunCmd(inB)

	if len(a) != len(b) {
		t.Fatalf("argv lengths diverged (%d vs %d); only the image ref changed", len(a), len(b))
	}
	var diffs []int
	for i := range a {
		if a[i] != b[i] {
			diffs = append(diffs, i)
		}
	}
	if len(diffs) != 1 {
		t.Fatalf("changing only imageRef changed %d argv elements (%v); assembly is not "+
			"reading the field — a constant would change none", len(diffs), diffs)
	}
	i := diffs[0]
	if a[i] != goldenImageRef || b[i] != otherRef {
		t.Errorf("argv[%d] = %q / %q, want %q / %q", i, a[i], b[i], goldenImageRef, otherRef)
	}
	// And it must sit exactly where podman expects the positional image: last
	// flag before the entrypoint.
	if i+1 >= len(a) || a[i+1] != "yolo-entrypoint" {
		t.Errorf("the ref at argv[%d] is not immediately followed by the entrypoint: %v", i, a[i:])
	}
}

// TestUnthreadedImageRefDoesNotFallBackToALegacyTag pins the OTHER half of the
// same decision: an assembleInput that carries no ref must NOT quietly become
// `localhost/yolo-jail:latest`.
//
// The tempting fallback is the whole defect in miniature — assembly would name a
// different image than the load pipeline prepared, and podman would happily run
// whatever :latest points at. The placeholder cannot resolve, so a launch that
// somehow reached it dies immediately with the placeholder in the error text.
func TestUnthreadedImageRefDoesNotFallBackToALegacyTag(t *testing.T) {
	in := &assembleInput{rt: "podman"}
	got := in.jailImage()
	if got == "localhost/yolo-jail:latest" || got == "yolo-jail:latest" {
		t.Fatalf("an unset imageRef fell back to the legacy tag %q — the launch would run "+
			"whatever :latest happens to be, which is what C2 removes", got)
	}
	if got != unsetImageRef {
		t.Errorf("unset imageRef = %q, want the placeholder %q", got, unsetImageRef)
	}
	// The Apple Container spelling must not resurrect a per-runtime constant
	// either: image.JailImageRef already spells the ref per runtime, upstream.
	acIn := &assembleInput{rt: "container"}
	if acIn.jailImage() != unsetImageRef {
		t.Errorf("container unset imageRef = %q, want the same placeholder", acIn.jailImage())
	}
	if (&assembleInput{rt: "container", imageRef: "yolo-jail:abcd"}).jailImage() != "yolo-jail:abcd" {
		t.Error("a threaded ref was rewritten by the runtime branch")
	}
}

// TestHostServiceEnvIsInsertedAtTheThreadedImageRef pins runNormal's OTHER
// reader of the same field — the one the design doc's "what breaks" list omits
// and that nothing tested before.
//
// The insert point is found by SEARCHING the argv for the image ref. Hand the
// loop a ref the argv does not contain and indexOfSlice returns -1, the loop
// `continue`s, and every `-e VAR=<path>` pair is dropped: no broker endpoint, no
// cgroup delegate, no host-process socket, on a jail that starts and looks fine.
// Before C2 both sides derived the same constant so they could not disagree;
// now the ref is per-config, and this is what makes the disagreement observable.
func TestHostServiceEnvIsInsertedAtTheThreadedImageRef(t *testing.T) {
	argv := []string{"podman", "run", "--rm", goldenImageRef, "yolo-entrypoint"}
	services := []loopholeDaemon{
		{name: "broker", envVarName: "YOLO_SERVICE_BROKER_ENDPOINT", jailPath: "/run/yolo-services/broker.endpoint"},
		// A host-scoped handle carries no variable and must be skipped.
		{name: "hostscoped", envVarName: "", jailPath: "/run/yolo-services/host.endpoint"},
		{name: "cgroup", envVarName: "YOLO_SERVICE_CGROUP_ENDPOINT", jailPath: "/run/yolo-services/cgroup.endpoint"},
	}

	got := insertHostServiceEnv(argv, goldenImageRef, services)
	want := []string{
		"podman", "run", "--rm",
		"-e", "YOLO_SERVICE_BROKER_ENDPOINT=/run/yolo-services/broker.endpoint",
		"-e", "YOLO_SERVICE_CGROUP_ENDPOINT=/run/yolo-services/cgroup.endpoint",
		goldenImageRef, "yolo-entrypoint",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("insertion:\ngot:  %v\nwant: %v", got, want)
	}

	// The failure mode, stated: a ref that is not in the argv silently drops
	// every pair. This is not a behaviour to preserve — it is the reason
	// runNormal must pass assembleInput.imageRef and never re-derive.
	stale := insertHostServiceEnv(argv, "localhost/yolo-jail:latest", services)
	if !slices.Equal(stale, argv) {
		t.Fatalf("a stale ref inserted something (%v); the point of this case is that it "+
			"inserts NOTHING, which is why the two readers must share one field", stale)
	}
}

// TestRunNormalThreadsTheLoadedRefIntoAssembly is a SOURCE assertion, for the
// same reason hostsingletonwiring_test.go's are: the wiring it pins lives inside
// runNormal, which needs a real container, and the value it pins comes from a
// build+load pipeline. Both facts are one line each and neither is reachable
// from any behavioural test in this package.
//
// Delete either line and the unit gate stays green while C2 becomes decorative:
// without the first, assembly gets no ref and every launch runs the unresolvable
// placeholder; without the second, the host-service loop searches for a ref that
// is not in the argv and drops every loophole endpoint variable.
func TestRunNormalThreadsTheLoadedRefIntoAssembly(t *testing.T) {
	src := runSource(t)
	if !strings.Contains(src, "imageRef:         loadedImage.Ref,") {
		t.Error("runNormal no longer threads AutoLoadImage's ref into assembleInput. " +
			"Assembly has no other source for it, so the argv would name the " +
			"unresolvable placeholder (TestUnthreadedImageRefDoesNotFallBackToALegacyTag)")
	}
	if !strings.Contains(src, "insertHostServiceEnv(runCmd, in.imageRef, hostServices)") {
		t.Error("runNormal's host-service insert no longer reads assembleInput.imageRef. " +
			"It must be the SAME field assembly appended — a second derivation is what " +
			"silently drops every `-e <VAR>=<path>` pair " +
			"(TestHostServiceEnvIsInsertedAtTheThreadedImageRef)")
	}
}
