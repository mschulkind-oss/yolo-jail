package configmigrate

import (
	"fmt"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ConfigChangesLogName is the durable host log, under paths.LogsDir, of every config file a
// migration moved or wrote: one line per file, appended, never trimmed. A config that was a link
// and became a detached regular file once went stale for days with nothing recording when or by
// what, so every migration says what it did here.
const ConfigChangesLogName = "config-changes.log"

// recordChanges appends one line per file res says was moved or written: the time, the command
// (opts.Command: the verb or the launch's offer), the file, the link text moved when it was a
// link, and the regular file that replaced it. It is best effort: a log that cannot be opened or
// written is skipped, and never fails a migration that already happened. The log opens through
// an os.Root, so a link planted in the logs directory cannot send the line elsewhere.
func recordChanges(command string, res *ApplyResult, now time.Time) {
	if res == nil || len(res.Written) == 0 && len(res.Links) == 0 {
		return
	}
	if command == "" {
		command = "yolo config migrate"
	}
	stamp := now.Format("2006-01-02T15:04:05-0700")
	linked := map[string]bool{}
	var lines []string
	for _, l := range res.Links {
		linked[l.New] = true
		text := ""
		if t, err := os.Readlink(l.Backup); err == nil {
			text = t
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s was a link to %s (target %s, not changed), kept as %s; wrote regular file %s",
			stamp, command, l.Link, text, l.Target, l.Backup, l.New))
	}
	for _, w := range res.Written {
		if !linked[w] {
			lines = append(lines, fmt.Sprintf("%s %s: wrote regular file %s (originals kept as <name>.pre-migrate)", stamp, command, w))
		}
	}
	if err := os.MkdirAll(paths.LogsDir(), 0o755); err != nil {
		return
	}
	root, err := os.OpenRoot(paths.LogsDir())
	if err != nil {
		return
	}
	defer root.Close()
	f, err := root.OpenFile(ConfigChangesLogName, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	for _, l := range lines {
		_, _ = fmt.Fprintln(f, l)
	}
}
