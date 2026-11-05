package resourcegroups

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveSelectedIOPathsUsesOnlyTheSelectedManagerUnit(t *testing.T) {
	mount := t.TempDir()
	group := filepath.Join(mount, "owner.slice", "work.slice")
	if err := os.MkdirAll(filepath.Join(group), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mount, "owner.slice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(group, "io.max"), []byte("8:0 rbps=101 wbps=20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "owner.slice", "io.max"), []byte("8:0 rbps=1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mountInfo := fmt.Sprintf("29 23 0:26 / %s rw,nosuid - cgroup2 cgroup rw\n", mount)
	var queries []string
	readCount := 0
	paths, err := ResolveSelectedIOPaths("work.slice", func(argv []string) (string, error) {
		queries = append(queries, strings.Join(argv, " "))
		if strings.Contains(strings.Join(argv, " "), "--user") {
			return "", fmt.Errorf("unit absent from user manager")
		}
		return "/owner.slice/work.slice\n", nil
	}, []byte(mountInfo), func(path string) ([]byte, error) {
		if path != filepath.Join(group, "io.max") {
			t.Errorf("resolver read outside selected group policy: %s", path)
		}
		readCount++
		if readCount == 1 {
			return []byte("8:0 rbps=101 wbps=20\n"), nil
		}
		return []byte("8:0 rbps=9999\n"), nil // a hypothetical reread would be stale relative to enumeration.
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || !strings.Contains(queries[0], "work.slice") || !strings.Contains(queries[1], "work.slice") {
		t.Fatalf("manager queries = %v, want exact selected unit in both managers", queries)
	}
	if paths.UserManager {
		t.Fatal("system-manager unit was marked as delegated user-manager authority")
	}
	wantAssigned := filepath.Join(group, "io.max")
	if paths.Assigned != wantAssigned || !reflect.DeepEqual(paths.Devices, []string{"8:0"}) || !paths.AssignedDataObserved {
		t.Fatalf("resolved selected policy = %+v, want %s and exact device set", paths, wantAssigned)
	}
	wantAncestors := []string{filepath.Join(mount, "owner.slice", "io.max"), filepath.Join(mount, "io.max")}
	if !reflect.DeepEqual(paths.Ancestors, wantAncestors) {
		t.Fatalf("ancestor policy paths = %v, want %v", paths.Ancestors, wantAncestors)
	}
	plan := ReadAndPlanIO(paths, 5, func(path string) ([]byte, error) {
		if path == wantAssigned {
			t.Fatal("plan reread assigned io.max instead of using the resolver's observed snapshot")
		}
		return []byte("8:0 rbps=1000\n"), nil
	})
	if got := plan.For("8:0", ReadBytes); got.State != StateFinite || got.Allowance != 101 || got.Agent != 5 || got.Batch != 96 {
		t.Fatalf("plan from resolver snapshot = %+v, want assigned snapshot split 5/96", got)
	}
	if readCount != 1 {
		t.Fatalf("selected io.max reads = %d, want one consistent snapshot", readCount)
	}
}

func TestResolveSelectedIOPathsRecordsUserManagerOnlyFromTheManagerReply(t *testing.T) {
	mount := t.TempDir()
	group := filepath.Join(mount, "user.slice", "work.slice")
	if err := os.MkdirAll(group, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(group, "io.max"), []byte("8:0 rbps=101\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mountInfo := fmt.Sprintf("29 23 0:26 / %s rw,nosuid - cgroup2 cgroup rw\n", mount)
	paths, err := ResolveSelectedIOPaths("work.slice", func(argv []string) (string, error) {
		if len(argv) > 1 && argv[1] == "--user" {
			return "/user.slice/work.slice", nil
		}
		return "", nil
	}, []byte(mountInfo), os.ReadFile)
	if err != nil || !paths.UserManager {
		t.Fatalf("user manager resolution: %+v, %v", paths, err)
	}
}

func TestResolveSelectedIOPathsDoesNotInferUserAuthorityIfSystemQueryFails(t *testing.T) {
	mount := t.TempDir()
	group := filepath.Join(mount, "user.slice", "work.slice")
	if err := os.MkdirAll(group, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(group, "io.max"), []byte("8:0 rbps=101\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mountInfo := fmt.Sprintf("29 23 0:26 / %s rw,nosuid - cgroup2 cgroup rw\n", mount)
	paths, err := ResolveSelectedIOPaths("work.slice", func(argv []string) (string, error) {
		if len(argv) > 1 && argv[1] == "--user" {
			return "/user.slice/work.slice", nil
		}
		return "", os.ErrPermission
	}, []byte(mountInfo), os.ReadFile)
	if err != nil || paths.UserManager {
		t.Fatalf("unknown system manager was mistaken for unique user authority: %+v %v", paths, err)
	}
}

func TestResolveSelectedIOPathsRefusesAmbiguousManagerOwnership(t *testing.T) {
	calls := 0
	_, err := ResolveSelectedIOPaths("same.slice", func([]string) (string, error) {
		calls++
		return "/same.slice", nil
	}, nil, os.ReadFile)
	if err == nil || !strings.Contains(err.Error(), "manager ownership is ambiguous") || calls != 2 {
		t.Fatalf("ambiguous selected manager result: calls=%d err=%v", calls, err)
	}
}
