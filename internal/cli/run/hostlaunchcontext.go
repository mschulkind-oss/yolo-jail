package run

// hostlaunchcontext.go is launchcontext.go's records for a `yolo host --` launch, written into
// its block in the machine-wide host launch log (HostLaunchLog).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// formatHostInvocation is a `yolo host --` launch's argv for the machine-wide host launch log:
// yolo's own tokens whole (a profile, a provider list, a notch: names, never secrets), then the
// program by its base name and its argument count, never the arguments themselves, by OQ-PR3's
// rule for that file (HostLaunchLog: an argument can be a secret or a prompt).
func formatHostInvocation(argv []string) string {
	if argv == nil {
		return "(none typed)"
	}
	own, command := argv, []string(nil)
	for i, a := range argv {
		if a == "--" {
			own, command = argv[:i+1], argv[i+1:]
			break
		}
	}
	parts := []string{"yolo"}
	if len(own) > 0 {
		parts = append(parts, shquote.Join(own))
	}
	if len(command) > 0 {
		parts = append(parts, shquote.Quote(filepath.Base(command[0])))
		if n := len(command) - 1; n == 1 {
			parts = append(parts, "<1 argument>")
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("<%d arguments>", n))
		}
	}
	return strings.Join(parts, " ")
}

// RecordInvocation writes the host launch's invocation record (formatHostInvocation of argv,
// os.Args[1:] as typed) to its block alone.
func (l *HostLaunchLog) RecordInvocation(argv []string) {
	l.record("invocation: " + formatHostInvocation(argv))
}

// HostLaunchContext is what a `yolo host --` launch's context records state, once it has
// composed.
type HostLaunchContext struct {
	// Workspace is the directory the command ran in: the key the previous launch's files are
	// stored under (its short code), never written into the log.
	Workspace string
	// Profile is the profile record's body (ProfileRecord over the host's fold).
	Profile string
	// Config is the effective config the launch composed from, Packs its selected packs.
	Config *jsonx.OrderedMap
	Packs  []*packload.Pack
	// CaptureStore is the host's capture-sidecar directory, "" when the host keeps none
	// (render.Target.SidecarDir: only host_management "own" does).
	CaptureStore string
}

// RecordContext writes a host launch's context records to its block alone: the profile and its
// sources, the config and pack changes since the previous host launch from this directory, and
// the captured edits a host apply re-applies. The previous launch's files are under
// HostLaunchStateDir, 0600, rather than beside the log: the logs directory is one a jail may
// mount, and the stored snapshot is the whole config, unredacted.
func (l *HostLaunchLog) RecordContext(c HostLaunchContext) {
	if l == nil {
		return
	}
	l.record("profile: " + c.Profile)
	if snap, err := config.SnapshotJSON(c.Config); err == nil {
		store := dirStateStore{dir: HostLaunchStateDir(),
			prefix: paths.JailShortHash(runtime.FromWorkspace(c.Workspace)) + "-"}
		for _, line := range configChangeRecords(store, snap, c.Packs, []string{paths.UserConfigPath()}, time.Now()) {
			l.record(line)
		}
	}
	var root *os.Root
	if c.CaptureStore != "" {
		if r, err := os.OpenRoot(c.CaptureStore); err == nil {
			root = r
			defer r.Close()
		}
	}
	for _, line := range capturedEditRecords(root, launchSurfaces(c.Packs), "re-applied by every host apply",
		func(slug string) string { return "yolo config reset " + slug + " --setup host-self" }) {
		l.record(line)
	}
}

// record writes one record line through the block's body (the launch id, the redaction), to the
// log alone.
func (l *HostLaunchLog) record(line string) {
	if l == nil {
		return
	}
	_, _ = hostLaunchBody{l}.Write([]byte("  " + line + "\n"))
}
