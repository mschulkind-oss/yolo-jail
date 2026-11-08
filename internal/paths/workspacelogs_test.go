package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A log is written in <workspace>/.yolo/logs, the directory made beneath the ensured `.yolo`
// (so the README and the .gitignore are there too), and nothing is written at the top level.
func TestOpenWorkspaceLogFileWritesInTheLogsDirectory(t *testing.T) {
	ws := t.TempDir()
	f, err := OpenWorkspaceLogFile(ws, "launch.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("line\n")
	f.Close()
	if b, err := os.ReadFile(filepath.Join(ws, ".yolo", "logs", "launch.log")); err != nil || string(b) != "line\n" {
		t.Fatalf("the log is not in .yolo/logs: %q, %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo", "launch.log")); err == nil {
		t.Error("a log was written at the top of .yolo")
	}
	for _, name := range []string{WorkspaceStateIgnoreName, WorkspaceStateReadmeName} {
		if _, err := os.Stat(filepath.Join(ws, ".yolo", name)); err != nil {
			t.Errorf("the logs directory was made without the ensure of .yolo: %s is missing", name)
		}
	}
}

// `logs` is jail-writable like `.yolo`: a link the last jail left there is refused, never
// followed onto the host directory it names.
func TestOpenWorkspaceLogFileRefusesALinkedLogsDirectory(t *testing.T) {
	ws := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(WorkspaceStateDir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, WorkspaceLogsDir(ws)); err != nil {
		t.Fatal(err)
	}
	_, err := OpenWorkspaceLogFile(ws, "boot.log", os.O_CREATE|os.O_WRONLY, 0o644)
	var linked *LinkedStateDirError
	if !errors.As(err, &linked) {
		t.Fatalf("a linked logs directory was not refused: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the write went through the link: %v", entries)
	}
}

// The writer that may outlive its workspace makes neither `.yolo` nor, without O_CREATE, `logs`.
func TestOpenExistingWorkspaceLogFileCreatesNoStateDir(t *testing.T) {
	ws := t.TempDir()
	if _, err := OpenExistingWorkspaceLogFile(ws, "housekeeping.log", os.O_CREATE|os.O_WRONLY, 0o644); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist for a missing .yolo, got %v", err)
	}
	if _, err := os.Lstat(WorkspaceStateDir(ws)); err == nil {
		t.Fatal(".yolo was created")
	}
	if err := os.MkdirAll(WorkspaceStateDir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExistingWorkspaceLogFile(ws, "launch.log", os.O_WRONLY|os.O_APPEND, 0); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist without O_CREATE, got %v", err)
	}
	if _, err := os.Lstat(WorkspaceLogsDir(ws)); err == nil {
		t.Fatal("logs was created by an open that does not create")
	}
	f, err := OpenExistingWorkspaceLogFile(ws, "housekeeping.log", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := os.Stat(WorkspaceLogPath(ws, "housekeeping.log")); err != nil {
		t.Fatal(err)
	}
}

// A jail an older yolo started still writes the top-level name: the reader takes whichever of the
// two exists, the newer when both do, and never a link.
func TestFindWorkspaceLogFallsBackToTheTopLevel(t *testing.T) {
	ws := t.TempDir()
	cur, legacy := WorkspaceLogPath(ws, "boot.log"), LegacyWorkspaceLogPath(ws, "boot.log")
	if got := FindWorkspaceLog(ws, "boot.log"); got != cur {
		t.Errorf("with neither, FindWorkspaceLog = %s, want %s", got, cur)
	}
	if err := os.MkdirAll(filepath.Dir(cur), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("old jail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindWorkspaceLog(ws, "boot.log"); got != legacy {
		t.Errorf("with only the top-level log, FindWorkspaceLog = %s, want %s", got, legacy)
	}
	if err := os.WriteFile(cur, []byte("new jail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	_ = os.Chtimes(legacy, past, past)
	if got := FindWorkspaceLog(ws, "boot.log"); got != cur {
		t.Errorf("with the logs directory's newer, FindWorkspaceLog = %s, want %s", got, cur)
	}
	_ = os.Chtimes(legacy, future, future)
	if got := FindWorkspaceLog(ws, "boot.log"); got != legacy {
		t.Errorf("with the top-level log newer (an older jail running now), FindWorkspaceLog = %s, want %s", got, legacy)
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cur, legacy); err != nil {
		t.Fatal(err)
	}
	if got := FindWorkspaceLog(ws, "boot.log"); got != cur {
		t.Errorf("a link at the top-level name was chosen: %s", got)
	}
}

// movedWorkspaceLogs are the logs yolo writes in <workspace>/.yolo/logs, by literal and by the
// constant that names each. A writer that opens one of them at the top of `.yolo` again would
// split the logs across two places for good.
var movedWorkspaceLogs = []string{
	`"launch.log"`, `"boot.log"`, `"boot.log.prev"`, `"boot.session.log"`, `"boot.session.log.prev"`,
	`"boot-perf.log"`, `"startup.log"`, `"host-perf.log"`, `"housekeeping.log"`,
	"LaunchLogName", "HostPerfLogName", "housekeepingLogName", "bootLogName", "bootLogPrevName",
	"bootSessionLogName", "bootSessionLogPrevName", "bootPerfName", "BootPerfName", "StartupLogName",
	"BuildLogName", "buildLogName",
}

// topLevelStateWrite matches the calls that write directly under <workspace>/.yolo, and a path
// built by joining a name onto it.
var topLevelStateWrite = regexp.MustCompile(`OpenWorkspaceStateFile\(|OpenExistingWorkspaceStateFile\(|` +
	`WriteWorkspaceStateFile\(|filepath\.Join\(paths\.WorkspaceStateDir\([^)]*\), |` +
	`filepath\.Join\([^,]+, "\.yolo", `)

// SD-D15: the project's logs live in .yolo/logs. Red if any writer or path in the shipped code
// names a moved log at the top of `.yolo` again. Only the compatibility reads may name the top
// level, and they do it through LegacyWorkspaceLogPath, which this does not match.
func TestNoWriterPutsAMovedLogAtTheTopOfTheStateDir(t *testing.T) {
	root := repoRootForTest(t)
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") || !topLevelStateWrite.MatchString(line) {
					continue
				}
				for _, name := range movedWorkspaceLogs {
					if strings.Contains(line, name) {
						rel, _ := filepath.Rel(root, p)
						t.Errorf("%s:%d writes or names the log %s at the top of .yolo; use paths.OpenWorkspaceLogFile "+
							"or paths.WorkspaceLogPath (paths.FindWorkspaceLog to read):\n\t%s", rel, i+1, name, strings.TrimSpace(line))
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The scan above is only as good as its pattern: it must catch each shape a regression would take.
func TestTheTopLevelLogScanCatchesEachShape(t *testing.T) {
	for _, line := range []string{
		`f, err := paths.OpenWorkspaceStateFile(o.Workspace, LaunchLogName, os.O_CREATE, 0o644)`,
		`mirror, _ := paths.OpenExistingWorkspaceStateFile(p.Workspace, LaunchLogName, os.O_WRONLY, 0)`,
		`return filepath.Join(paths.WorkspaceStateDir(workspace), bootLogName)`,
		`return filepath.Join(workspace, ".yolo", "startup.log")`,
	} {
		hit := false
		for _, name := range movedWorkspaceLogs {
			hit = hit || (topLevelStateWrite.MatchString(line) && strings.Contains(line, name))
		}
		if !hit {
			t.Errorf("the scan misses %s", line)
		}
	}
}
