//go:build darwin

package ioprio

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// diskPolicyRoleEnv names what a re-executed copy of this test binary does: "set" sets
// IOPOL_THROTTLE on itself, reads it back and starts a "read" child; "read" prints its own
// policy. The parent test process never sets anything: a process-scope policy lasts for the
// life of the process, and it would throttle every test that runs after this one.
const diskPolicyRoleEnv = "YOLO_IOPRIO_DISKPOLICY_TEST_ROLE"

// TestMain answers a re-executed role before the test framework runs anything, so the copy
// that sets the policy does nothing else.
func TestMain(m *testing.M) {
	switch os.Getenv(diskPolicyRoleEnv) {
	case "set":
		os.Exit(diskPolicySetRole())
	case "read":
		got, err := GetProcessDiskPolicy()
		if err != nil {
			fmt.Println("READ-ERR", err)
			os.Exit(1)
		}
		fmt.Println("CHILD", got)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// diskPolicySetRole is the "set" copy: set through the trampoline, read back through the
// other, then start a child that reads its own, which is the inheritance the macos-user launch
// rests on (the agent is a descendant of the launcher that sets it).
func diskPolicySetRole() int {
	before, err := GetProcessDiskPolicy()
	if err != nil {
		fmt.Println("READ-ERR", err)
		return 1
	}
	fmt.Println("BEFORE", before)
	if err := SetProcessDiskPolicy(IopolThrottle); err != nil {
		fmt.Println("SET-ERR", err)
		return 1
	}
	got, err := GetProcessDiskPolicy()
	if err != nil {
		fmt.Println("READ-ERR", err)
		return 1
	}
	fmt.Println("SELF", got)
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), diskPolicyRoleEnv+"=read")
	out, err := child.CombinedOutput()
	fmt.Print(string(out))
	if err != nil {
		fmt.Println("CHILD-ERR", err)
		return 1
	}
	return 0
}

// TestDiskPolicyIsSetReadBackAndInherited drives the real libSystem calls on a Mac
// (check-macos runs it on every push): a re-executed copy sets IOPOL_THROTTLE at process
// scope, reads IOPOL_THROTTLE back, and a child it starts reads IOPOL_THROTTLE too. The
// trampolines are the only darwin code here a Linux gate cannot run.
func TestDiskPolicyIsSetReadBackAndInherited(t *testing.T) {
	before, err := GetProcessDiskPolicy()
	if err != nil {
		t.Fatalf("getiopolicy_np through the trampoline failed in the test process: %v", err)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), diskPolicyRoleEnv+"=set")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		t.Fatalf("the re-executed setter failed (%v):\n%s", err, text)
	}
	want := fmt.Sprint(IopolThrottle)
	if !strings.Contains(text, "SELF "+want+"\n") {
		t.Errorf("after setiopolicy_np(IOPOL_THROTTLE) the setter read back something else:\n%s", text)
	}
	if !strings.Contains(text, "CHILD "+want+"\n") {
		t.Errorf("a child of the setter did not inherit IOPOL_THROTTLE, so the launch's agent "+
			"would not either:\n%s", text)
	}
	// And the test process itself is untouched: its policy is the one it started with.
	after, err := GetProcessDiskPolicy()
	if err != nil || after != before {
		t.Errorf("the test process's own policy moved from %d to %d (%v); only the re-executed "+
			"copy may set one", before, after, err)
	}
}
