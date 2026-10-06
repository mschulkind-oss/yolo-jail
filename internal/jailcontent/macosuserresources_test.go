package jailcontent

import (
	"strings"
	"testing"
)

// TestTheMacosUserBriefingSaysWhatEachResourceKeyDoes: on macos-user three `resources` keys
// act and one does not — `io` is a macOS disk I/O policy, `memory` a sampled guard that
// terminates the session's largest process, `cpus` four parallelism defaults, `pids_limit`
// read and ignored — so the packages section says what each one does, and never that a memory
// or CPU limit is ignored. The reader this protects is the agent whose build the memory guard
// just terminated: told no limit exists, it has no reason to ask for a bigger one.
//
// The briefing that carried the Disk I/O line beside a sentence saying `resources` is ignored
// is the case that shipped, so it is the one rendered here.
func TestTheMacosUserBriefingSaysWhatEachResourceKeyDoes(t *testing.T) {
	native := BriefingContent(BriefingInput{
		Workspace:  "/Users/Shared/yolo/proj",
		Mechanism:  "macos-user",
		Home:       "/Users/_yolojail",
		IOPriority: "idle",
	})
	// One line of words, so a want is not split by where the section happens to wrap.
	section := strings.Join(strings.Fields(packagesSectionOf(t, native)), " ")

	for _, stale := range []string{
		"a memory or CPU limit in the config is read and ignored",
		"cannot be delivered",
		"Do not plan around one",
	} {
		if strings.Contains(section, stale) {
			t.Errorf("the macos-user packages section still says %q, which B14 made false:\n%s",
				stale, section)
		}
	}
	// "read and ignored" is said once, of pids_limit, and of nothing else.
	if n := strings.Count(section, "read and ignored"); n != 1 ||
		!strings.Contains(section, "`pids_limit` is read and ignored") {
		t.Errorf("want exactly one \"read and ignored\", naming `pids_limit`; got %d:\n%s", n, section)
	}
	for _, want := range []string{
		"`io` lowers the session's macOS disk I/O priority",
		"`memory` is checked by sampling",
		"not kernel-enforced",
		"its largest process is terminated",
		"a line naming `resources.memory`",
		"ask the human to raise it",
		"`cpus` only sets the GOMAXPROCS, CARGO_BUILD_JOBS, RAYON_NUM_THREADS and OMP_NUM_THREADS defaults",
		"a program that ignores them is not limited",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the macos-user packages section does not say %q:\n%s", want, section)
		}
	}

	// And a container's section gains none of it: its limits are kernel-enforced, and it
	// already says how to change them.
	for _, mech := range []string{"podman", "container", ""} {
		out := strings.Join(strings.Fields(packagesSectionOf(t, BriefingContent(BriefingInput{
			Workspace: "/host/proj", Mechanism: mech,
		}))), " ")
		for _, never := range []string{"checked by sampling", "`pids_limit` is read and ignored", "GOMAXPROCS"} {
			if strings.Contains(out, never) {
				t.Errorf("mechanism %q got the macos-user resources sentence %q:\n%s", mech, never, out)
			}
		}
	}
}
