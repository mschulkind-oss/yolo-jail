package check

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio/iopriotest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const ioWS = "/yolo-test-ws/project"

type ioCheck struct {
	sys       *iopriotest.Sys
	io        any
	inJail    bool
	macOS     bool
	runtime   string
	graphRoot string
}

// runIOPrioritySection runs sectionIOPriority alone over a fake root, returning the report
// and the reporter (for its counts). podman's storage root is the readiness gate's answer, as
// in a real check; a `podman info` of the section's own fails the test (PR-D6: check asks
// podman once, through the gate).
func runIOPrioritySection(t *testing.T, c ioCheck) (string, *reporter) {
	t.Helper()
	var out bytes.Buffer
	env := map[string]string{}
	if c.inJail {
		env["YOLO_VERSION"] = "9.9.9-test"
	}
	exec := func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[0] == "podman" && argv[1] == "info" {
			t.Errorf("the I/O priority section ran its own %q; it reads the readiness gate's answer",
				strings.Join(argv, " "))
		}
		return ExecResult{Ran: false}
	}
	o := &Options{Stdout: &out, IsTTYStdout: func() bool { return false }}
	fillDefaults(o)
	o.Getenv = func(k string) string { return env[k] }
	o.Exec = exec
	info := `{"host":{}}`
	if c.graphRoot != "" {
		info = `{"host":{},"store":{"graphDriverName":"overlay","graphRoot":"` + c.graphRoot + `"}}`
	}
	answeringPodman(o, info)
	o.IsMacOS = c.macOS
	o.Workspace = ioWS
	if c.sys != nil {
		o.ioSysRoot = c.sys.Root
	} else {
		o.ioSysRoot = t.TempDir()
	}
	merged := jsonx.NewOrderedMap()
	if c.io != nil {
		res := jsonx.NewOrderedMap()
		res.Set("io", c.io)
		merged.Set("resources", res)
	}
	r := newReporter(&out, false)
	o.sectionIOPriority(r, merged, c.runtime)
	return out.String(), r
}

// TestIOPriorityCheckGradesTheDiskTable is §5.4's grading table, row by row, over the real
// resolver: a [WARN] names the declared value, the disk, its scheduler, the path and the
// host change; a [PASS] says where it acts, and that dm-crypt costs BFQ the writes.
func TestIOPriorityCheckGradesTheDiskTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sys   *iopriotest.Sys
		io    string
		want  []string
		notIn []string
	}{
		{"bfq and low", iopriotest.Scheduler(t, ioWS, "bfq"), "low",
			[]string{`[PASS] resources.io.priority "low" acts on sda (scheduler bfq), the disk under ` + ioWS}, []string{"[WARN]"}},
		{"mq-deadline and idle", iopriotest.Scheduler(t, ioWS, "mq-deadline"), "idle",
			[]string{`[PASS] resources.io.priority "idle" acts on sda (scheduler mq-deadline)`}, []string{"[WARN]"}},
		{"mq-deadline and low", iopriotest.Scheduler(t, ioWS, "mq-deadline"), "low",
			[]string{`[WARN] resources.io.priority "low" does nothing on sda (scheduler mq-deadline)`, `Or declare "idle"`, "udev rule"}, nil},
		{"kyber through LUKS", iopriotest.Kyber(t, ioWS), "idle",
			[]string{`[WARN] resources.io.priority "idle" does nothing on nvme0n1 (scheduler kyber), the disk under ` + ioWS,
				"switch nvme0n1's scheduler to bfq", "echo bfq | sudo tee /sys/block/nvme0n1/queue/scheduler",
				`KERNEL=="nvme0n1", ATTR{queue/scheduler}="bfq"`, "then: yolo check"}, []string{"Or declare"}},
		{"none", iopriotest.Scheduler(t, ioWS, "none"), "low",
			[]string{`[WARN] resources.io.priority "low" does nothing on sda (scheduler none)`}, nil},
		{"bfq under LUKS", iopriotest.New(t).Mount(ioWS, "btrfs", "/dev/mapper/root").
			Disk("sdb", "mq-deadline [bfq]").Part("sdb", "sdb2").DM("dm-0", "root", "CRYPT-LUKS2-x", "sdb2"), "low",
			[]string{"[PASS]", "sdb (scheduler bfq)", "on reads only"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, r := runIOPrioritySection(t, ioCheck{sys: tc.sys, io: tc.io, runtime: "podman"})
			if !strings.Contains(got, "Disk I/O priority") {
				t.Fatalf("no section header:\n%s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("report lacks %q:\n%s", want, got)
				}
			}
			for _, not := range tc.notIn {
				if strings.Contains(got, not) {
					t.Errorf("report carries %q:\n%s", not, got)
				}
			}
			if r.failed != 0 {
				t.Errorf("a disk that ignores the priority is a [WARN], never a [FAIL]:\n%s", got)
			}
		})
	}
}

// TestIOPriorityCheckSaysWhyItCannotGrade: VirtioFS is Warned, a mount with no block device
// is a [SKIP] naming the filesystem, and an unreadable sysfs is hostFact's [SKIP] in a jail
// and a plain one naming the path at a host.
func TestIOPriorityCheckSaysWhyItCannotGrade(t *testing.T) {
	got, _ := runIOPrioritySection(t, ioCheck{sys: iopriotest.New(t).Mount(ioWS, "virtiofs", "mount0"),
		io: "low", inJail: true, runtime: "podman"})
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "VirtioFS") {
		t.Errorf("a virtiofs workspace must be Warned:\n%s", got)
	}
	got, _ = runIOPrioritySection(t, ioCheck{sys: iopriotest.New(t).Mount(ioWS, "tmpfs", "tmpfs"), io: "low", runtime: "podman"})
	if !strings.Contains(got, "[SKIP]") || !strings.Contains(got, "tmpfs") {
		t.Errorf("a tmpfs workspace must be a [SKIP] naming tmpfs:\n%s", got)
	}
	unreadable := iopriotest.New(t).Mount(ioWS, "ext4", "/dev/sda1")
	got, _ = runIOPrioritySection(t, ioCheck{sys: unreadable, io: "low", inJail: true, runtime: "podman"})
	if !strings.Contains(got, "a host fact, not visible from inside a jail") || !strings.Contains(got, "queue/scheduler") {
		t.Errorf("an unreadable sysfs in a jail must be hostFact's [SKIP]:\n%s", got)
	}
	got, _ = runIOPrioritySection(t, ioCheck{sys: unreadable, io: "low", runtime: "podman"})
	if strings.Contains(got, "a host fact") || !strings.Contains(got, "[SKIP] could not read /sys/class/block/sda1") {
		t.Errorf("an unreadable sysfs at the host is a plain [SKIP] naming the path:\n%s", got)
	}
}

// TestIOPriorityCheckOnMacOSHosts: a macOS host never resolves a disk. Its VM backends are
// Warned by name, where nothing on a Mac can make the key act, so the step is the one that
// silences it.
func TestIOPriorityCheckOnMacOSHosts(t *testing.T) {
	for _, tc := range []struct{ rt, want string }{
		{"container", `resources.io.priority "low" is not applied on Apple Container`},
		{"podman", `resources.io.priority "low" is not applied on podman on macOS`},
	} {
		got, _ := runIOPrioritySection(t, ioCheck{io: "low", macOS: true, runtime: tc.rt})
		if !strings.Contains(got, "[WARN] "+tc.want) {
			t.Errorf("%s: report\n%s\nwant %q", tc.rt, got, tc.want)
		}
		if !strings.Contains(got, "remove resources.io.priority to silence this, then: yolo check") {
			t.Errorf("%s: the warning names no next step:\n%s", tc.rt, got)
		}
	}
}

// TestIOPriorityCheckOnMacosUserIsApplied is build step 5 (io-priority.md §5.5, IO-D7): the
// macos-user launcher sets the policy, so the row is a [PASS] naming the policy each value
// becomes, and it is no longer a warning with a step to silence it.
func TestIOPriorityCheckOnMacosUserIsApplied(t *testing.T) {
	for _, tc := range []struct{ io, want string }{
		{"low", `[PASS] resources.io.priority "low" is applied at launch by setiopolicy_np as IOPOL_UTILITY`},
		{"idle", `[PASS] resources.io.priority "idle" is applied at launch by setiopolicy_np as IOPOL_THROTTLE`},
	} {
		got, r := runIOPrioritySection(t, ioCheck{io: tc.io, macOS: true, runtime: "macos-user"})
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: report\n%s\nwant %q", tc.io, got, tc.want)
		}
		if strings.Contains(got, "[WARN]") || strings.Contains(got, "silence this") || r.warned != 0 {
			t.Errorf("%s: an applied priority is still reported as a warning:\n%s", tc.io, got)
		}
	}
	if got, _ := runIOPrioritySection(t, ioCheck{io: "normal", macOS: true, runtime: "macos-user"}); got != "" {
		t.Errorf("an undeclared priority printed a row on macos-user:\n%s", got)
	}
}

// TestIOPriorityCheckAlsoGradesPodmansStorageAtTheHost: at a host, podman's storage root is
// the second path, one row per disk; in a jail it is not asked.
func TestIOPriorityCheckAlsoGradesPodmansStorageAtTheHost(t *testing.T) {
	sys := iopriotest.Scheduler(t, ioWS, "bfq").Mount("/yolo-test-store", "ext4", "/dev/nvme0n1p1").
		Disk("nvme0n1", "[none] mq-deadline").Part("nvme0n1", "nvme0n1p1")
	got, _ := runIOPrioritySection(t, ioCheck{sys: sys, io: "low", runtime: "podman", graphRoot: "/yolo-test-store/containers"})
	if !strings.Contains(got, "[PASS]") || !strings.Contains(got, "does nothing on nvme0n1 (scheduler none), the disk under /yolo-test-store/containers") {
		t.Errorf("the host run must grade the workspace and podman's storage:\n%s", got)
	}
	got, _ = runIOPrioritySection(t, ioCheck{sys: sys, io: "low", runtime: "podman", graphRoot: "/yolo-test-store/containers", inJail: true})
	if strings.Contains(got, "yolo-test-store") {
		t.Errorf("a jail must not grade podman's storage root:\n%s", got)
	}
}

// TestIOPriorityCheckIsSilentWhenUndeclared: no row, not even a header, for an unset key,
// "normal", or an empty object.
func TestIOPriorityCheckIsSilentWhenUndeclared(t *testing.T) {
	for _, io := range []any{nil, "normal", jsonx.NewOrderedMap()} {
		if got, _ := runIOPrioritySection(t, ioCheck{sys: iopriotest.Kyber(t, ioWS), io: io, runtime: "podman"}); got != "" {
			t.Errorf("io=%v printed:\n%s", io, got)
		}
	}
}

// Two disks that both ignore the priority each get their own persistence step: one udev rules
// file per disk, so following the second disk's note does not overwrite the first disk's rule.
func TestIOPriorityUdevRulePerDisk(t *testing.T) {
	sys := iopriotest.Scheduler(t, ioWS, "kyber").Mount("/yolo-test-store", "ext4", "/dev/nvme0n1p1").
		Disk("nvme0n1", "[none] mq-deadline").Part("nvme0n1", "nvme0n1p1")
	got, _ := runIOPrioritySection(t, ioCheck{sys: sys, io: "low", runtime: "podman", graphRoot: "/yolo-test-store/containers"})
	for _, want := range []string{"/etc/udev/rules.d/60-yolo-ioscheduler-sda.rules",
		"/etc/udev/rules.d/60-yolo-ioscheduler-nvme0n1.rules"} {
		if !strings.Contains(got, want) {
			t.Errorf("no per-disk rules file %s:\n%s", want, got)
		}
	}
}

// The VM backends' note says "Nothing here can make it act" once, in the shared step, and not
// again in its own words.
func TestIOPriorityMacOSNoteSaysItOnce(t *testing.T) {
	for _, rt := range []string{"container", "podman"} {
		got, _ := runIOPrioritySection(t, ioCheck{io: "low", macOS: true, runtime: rt})
		if n := strings.Count(strings.ToLower(got), "nothing"); n != 1 {
			t.Errorf("%s: the note says \"nothing\" %d times, want once:\n%s", rt, n, got)
		}
	}
}
