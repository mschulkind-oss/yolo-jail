package run

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The CAP_SYS_PTRACE grant (security-shim.md#jail-capabilities, approved by the owner
// 2026-11-13). crun runs as the jail's root and has to open /proc/<pid>/ns/* of a nested
// container's process to `podman exec` or `podman healthcheck run` into it; for a container
// running as a non-root uid the kernel allows that only to the same uid or to CAP_SYS_PTRACE.
//
// The grant is safe only BECAUSE of two other properties of the same argv, so these tests pin
// all three together:
//
//   - the jail is in a non-initial user namespace: the host branches pass the
//     `--uidmap 0:0:1 --uidmap 1:1:65536` pair (rootful included), and the nested branch
//     shares its launcher's namespace through `--userns host`, which is itself a jail's;
//   - no `--pid` flag is assembled, so the jail keeps its own pid namespace and can name no
//     host process.
//
// A change that drops either property while keeping the capability fails here.

// ptraceLaunch is one podman launch shape the grant has to reach.
type ptraceLaunch struct {
	name   string
	nested bool
	argv   []string
}

func ptracePlainInput(t *testing.T) *assembleInput {
	t.Helper()
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return &assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		homeSkeleton: goldenHomeSkeleton,
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}
}

func withRootless(t *testing.T, o *Options, rootless bool) {
	t.Helper()
	facts := &podmanFacts{json: `{"host":{"security":{"rootless":false}}}`}
	if rootless {
		facts.json = `{"host":{"security":{"rootless":true}}}`
	}
	if err := json.Unmarshal([]byte(facts.json), &facts.info); err != nil {
		t.Fatal(err)
	}
	facts.parsed = true
	o.podmanFacts = facts
}

// ptraceLaunches assembles every podman launch shape: the host branch on a rootless and on a
// rootful podman (one argv branch, two hosts), the host NVIDIA branch, and the nested branch.
func ptraceLaunches(t *testing.T) []ptraceLaunch {
	t.Helper()
	var out []ptraceLaunch
	for _, rootless := range []bool{true, false} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		emptyLoopholeDirs(t)
		o := goldenOptions("/ws", home)
		withRootless(t, o, rootless)
		name := "host rootful"
		if rootless {
			name = "host rootless"
		}
		out = append(out, ptraceLaunch{name: name, argv: o.assembleRunCmd(ptracePlainInput(t))})
	}

	o, _, _ := nvidiaHostOptions(t, false)
	out = append(out, ptraceLaunch{name: "host NVIDIA", argv: o.assembleRunCmd(gpuInput(t, "nvidia"))})

	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o = goldenOptions("/ws", home)
	o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
	out = append(out, ptraceLaunch{name: "nested", nested: true, argv: o.assembleRunCmd(ptracePlainInput(t))})
	return out
}

func hasFlagValue(argv []string, flag, value string) bool {
	for _, v := range argvValues(argv, flag) {
		if v == value {
			return true
		}
	}
	return false
}

// TestEveryPodmanLaunchGrantsSysPtrace pins the capability on every podman launch shape.
func TestEveryPodmanLaunchGrantsSysPtrace(t *testing.T) {
	for _, l := range ptraceLaunches(t) {
		if !hasFlagValue(l.argv, "--cap-add", "SYS_PTRACE") {
			t.Errorf("%s: the jail is not granted CAP_SYS_PTRACE, so a nested `podman exec` into "+
				"a container running as a non-root uid fails opening /proc/<pid>/ns/mnt:\nargv: %s",
				l.name, strings.Join(l.argv, " "))
		}
		// The caps it sits beside stay too: the grant is an addition, not a replacement.
		for _, c := range []string{"SYS_ADMIN", "NET_ADMIN", "NET_RAW"} {
			if !hasFlagValue(l.argv, "--cap-add", c) {
				t.Errorf("%s: --cap-add %s is missing:\nargv: %s", l.name, c, strings.Join(l.argv, " "))
			}
		}
	}
}

// TestThePtraceGrantRidesOnAPrivateUserAndPidNamespace ties the grant to the two properties
// that keep it inside the jail (condition 1 of the owner's approval).
func TestThePtraceGrantRidesOnAPrivateUserAndPidNamespace(t *testing.T) {
	for _, l := range ptraceLaunches(t) {
		if !hasFlagValue(l.argv, "--cap-add", "SYS_PTRACE") {
			continue // TestEveryPodmanLaunchGrantsSysPtrace reports it.
		}
		for _, a := range l.argv {
			if a == "--pid" || strings.HasPrefix(a, "--pid=") {
				t.Errorf("%s: the argv carries %q next to CAP_SYS_PTRACE; a shared pid namespace "+
					"would let the jail ptrace processes outside it. Drop one or the other:\nargv: %s",
					l.name, a, strings.Join(l.argv, " "))
			}
		}
		if l.nested {
			// The nested branch shares its launcher's user namespace, which is a jail's own.
			if !hasFlagValue(l.argv, "--userns", "host") {
				t.Errorf("%s: a nested launch must share its launcher's (non-initial) user namespace "+
					"with --userns host:\nargv: %s", l.name, strings.Join(l.argv, " "))
			}
			continue
		}
		if !hasFlagValue(l.argv, "--uidmap", "0:0:1") || !hasFlagValue(l.argv, "--uidmap", "1:1:65536") {
			t.Errorf("%s: CAP_SYS_PTRACE is granted without the `--uidmap 0:0:1 --uidmap 1:1:65536` "+
				"pair, so on a rootful host the capability would be held in the initial user "+
				"namespace:\nargv: %s", l.name, strings.Join(l.argv, " "))
		}
	}
}

// TestThePtraceGrantKeepsDefaultSeccomp: condition 2 — the grant adds no seccomp change. Only
// the AMD `seccomp_unconfined` opt-in turns the filter off, and an ordinary launch never does.
func TestThePtraceGrantKeepsDefaultSeccomp(t *testing.T) {
	for _, l := range ptraceLaunches(t) {
		for _, v := range argvValues(l.argv, "--security-opt") {
			if strings.HasPrefix(v, "seccomp") {
				t.Errorf("%s: --security-opt %s on a launch that did not opt in; the ptrace grant "+
					"was approved on the condition that default seccomp stays on", l.name, v)
			}
		}
	}
}
