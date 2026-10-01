package macosuser

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// A DRY RUN IS READ TO CHECK WHAT WILL RUN AS ROOT, so each privileged command it prints has to
// read back as the argv the launch will execute. A Mac workspace is often under a path with a
// space in it ("My Projects"), and the argvs carry ACE strings with spaces of their own; joined
// with bare spaces, one argument read as several and the plan showed commands that were not the
// ones it would run.
func TestPrintPlanSpellsEachPrivilegedCommandAsItsArgv(t *testing.T) {
	plan := BuildRunPlan("/Users/Jane Doe/My Projects/proj", jsonx.NewOrderedMap(), nil,
		[]string{"claude"}, "/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), nil, nil)
	var want [][]string
	for _, cmds := range [][][]string{plan.StageCommands, plan.EnvFileCommands, plan.EnvFileGrantCommands} {
		for _, c := range cmds {
			want = append(want, append([]string{"sudo"}, c...))
		}
	}
	want = append(want, append([]string{"sudo"}, plan.BootstrapArgv[1:]...), plan.BootstrapArgv, plan.LaunchArgv)
	if len(plan.ProvisionArgv) > 1 {
		want = append(want, append([]string{"sudo"}, plan.ProvisionArgv[1:]...), plan.ProvisionArgv)
	}
	if len(plan.JailDaemonArgv) > 0 {
		want = append(want, plan.JailDaemonArgv)
	}
	if !slices.ContainsFunc(want, func(argv []string) bool {
		return slices.ContainsFunc(argv, func(a string) bool { return strings.Contains(a, " ") })
	}) {
		t.Fatal("no privileged argv holds an argument with a space in it, so this test proves nothing")
	}

	var buf bytes.Buffer
	PrintPlan(&buf, plan, nil)
	printed := printedCommands(t, buf.String(), want)
	for _, argv := range want {
		if !slices.ContainsFunc(printed, func(got []string) bool { return slices.Equal(got, argv) }) {
			t.Errorf("the dry run prints no line that reads back as %q:\n%s", argv, buf.String())
		}
	}
}

// The capture's dry run prints its privileged commands the same way, for the same reader.
func TestPrintCapturePlanSpellsEachPrivilegedCommandAsItsArgv(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	var want [][]string
	for _, cmds := range [][][]string{plan.PrepareCommands, plan.StageCommands, plan.EnvFileCommands,
		plan.EnvFileGrantCommands} {
		for _, c := range cmds {
			want = append(want, append([]string{"sudo"}, c...))
		}
	}
	want = append(want, plan.BootstrapArgv, plan.DriverArgv)
	if !slices.ContainsFunc(want, func(argv []string) bool {
		return slices.ContainsFunc(argv, func(a string) bool { return strings.Contains(a, " ") })
	}) {
		t.Fatal("no privileged argv holds an argument with a space in it, so this test proves nothing")
	}

	var buf bytes.Buffer
	PrintCapturePlan(&buf, plan, nil)
	printed := printedCommands(t, buf.String(), want)
	for _, argv := range want {
		if !slices.ContainsFunc(printed, func(got []string) bool { return slices.Equal(got, argv) }) {
			t.Errorf("the capture dry run prints no line that reads back as %q:\n%s", argv, buf.String())
		}
	}
}

// printedCommands reads, through a shell, every line of a printed plan that is indented two
// spaces and opens with the first word of one of the argvs in want: the lines that print an argv.
func printedCommands(t *testing.T, out string, want [][]string) [][]string {
	t.Helper()
	var printed [][]string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "(content on stdin") {
			continue // prose beside the command, and the one line no argv is printed on
		}
		for _, argv := range want {
			if strings.HasPrefix(line, "  "+argv[0]+" ") {
				printed = append(printed, testsupport.ShellWords(t, strings.TrimSpace(line)))
				break
			}
		}
	}
	return printed
}
