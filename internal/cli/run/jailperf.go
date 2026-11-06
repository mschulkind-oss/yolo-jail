package run

// jailperf.go brings the container boot's own timings into the launch's host timeline. The
// entrypoint already times every boot step and appends a block to ~/.yolo-perf.log (the jail half,
// <ws>/.yolo/home/yolo-perf.log on podman), and the provisioning stage adds its duration under the
// first session's block; until this file the host printed only the path, so a `--timing` table and
// the host perf log showed the boot as one opaque launch.run_with_proxy.
//
// So a fresh container launch reads that file twice, and records what it finds as completed
// intervals (perf.Log.Record): at the keeper's ready frame, pid 1's boot block, one span per step,
// container.boot.<step>; and at its end, the provisioning stage's line, container.provision. The
// naming is <backend>.<boot>.<step>, like macos-user's macos_user.bootstrap.<step>. An attach does
// not boot and reads nothing (only Run's fresh path calls this).
//
// THE FILE IS THE JAIL'S WRITING, so the host reads it as untrusted: beneath a root on the
// workspace's .yolo (a link escaping it is refused), regular files only, at most jailPerfReadCap
// bytes from its end, and the steps through perf.StepsFromMarks (bounded, names sanitized). A
// block identical to the one the file ended with before the launch is the last launch's, never
// this one's, so a boot that wrote nothing records nothing. Timing only: nothing here can refuse
// or change a launch.

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// jailPerfReadCap bounds how much of the jail half the host reads: its tail, which holds the
// newest blocks. The entrypoint keeps perf.MaxRuns blocks of a few KiB each.
const jailPerfReadCap = 1 << 20

// jailPerfBlockPrefix opens each boot's block (entrypoint's bootPerfRunPrefix).
const jailPerfBlockPrefix = "=== YOLO"

// jailPerfReader is one fresh launch's reads of its jail half. nil records nothing.
type jailPerfReader struct {
	ws, rel string
	// before is the last block the file held before the launch: never this launch's boot.
	before string
	// boot is the block recorded as this launch's boot, "" until one was.
	boot string
	// provisioned is whether container.provision was recorded.
	provisioned bool
}

// jailPerfRel is the jail half's path under <ws>/.yolo on rt: podman binds the single file
// <home>/yolo-perf.log at ~/.yolo-perf.log, and Apple Container binds <home>/ whole at /home/agent;
// home is the launch's home relative to <ws>/.yolo (Options.homeRel).
func jailPerfRel(rt, home string) string {
	if rt == "container" { // parity: Honored — Apple Container binds the whole home overlay, so the file keeps its leading dot
		return filepath.Join(home, ".yolo-perf.log")
	}
	return filepath.Join(home, "yolo-perf.log")
}

// snapshotJailPerf notes the jail half's last block before the boot, or returns nil when the
// launch records no timing.
func (o *Options) snapshotJailPerf(rt string) *jailPerfReader {
	if !o.Perf.Enabled() || rt == "macos-user" { // parity: HonoredBy — macos-user records its boot as macos_user.bootstrap.<step> from boot-perf.log (macosuser/bootperf.go)
		return nil
	}
	r := &jailPerfReader{ws: o.Workspace, rel: jailPerfRel(rt, o.homeRel())}
	header, body := lastJailPerfBlock(r.read())
	r.before = header + body
	return r
}

// read returns the jail half's tail, or "" when it cannot be read safely.
func (r *jailPerfReader) read() string {
	root, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(r.ws))
	if err != nil {
		return ""
	}
	defer root.Close()
	if fi, err := root.Lstat(r.rel); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := root.OpenFile(r.rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	if size := st.Size(); size > jailPerfReadCap {
		if _, err := f.Seek(size-jailPerfReadCap, io.SeekStart); err != nil {
			return ""
		}
	}
	b, _ := io.ReadAll(io.LimitReader(f, jailPerfReadCap))
	return string(b)
}

// recordBoot records pid 1's boot block as container.boot.<step>, once, when the file's last block
// is new since the snapshot. Called at the ready frame, and again before a launch whose jail never
// became ready reports, since a refused boot writes its block too.
func (r *jailPerfReader) recordBoot(log *perf.Log) {
	if r == nil || r.boot != "" {
		return
	}
	header, body := lastJailPerfBlock(r.read())
	if header == "" || header+body == r.before {
		return
	}
	r.boot = header + body
	var marks []perf.ForeignMark
	for _, line := range strings.Split(body, "\n") {
		if m, ok := parseJailPerfMark(line); ok {
			marks = append(marks, m)
		}
	}
	for _, s := range perf.StepsFromMarks(marks) {
		if s.Name == "start" { // the block's zero line, not a step
			continue
		}
		log.Record("container.boot."+s.Name, s.Dur)
	}
}

// recordProvision records the provisioning stage's duration as container.provision, once: the
// first `provisioning stage:` line after this launch's boot block, which the first session's
// provisioning appended under its own pass's block.
func (r *jailPerfReader) recordProvision(log *perf.Log) {
	if r == nil || r.boot == "" || r.provisioned {
		return
	}
	text := r.read()
	i := strings.Index(text, r.boot)
	if i < 0 {
		return
	}
	for _, line := range strings.Split(text[i+len(r.boot):], "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "provisioning stage: ")
		if !ok {
			continue
		}
		ms, _, ok := strings.Cut(rest, "ms")
		n, err := strconv.ParseInt(ms, 10, 64)
		if !ok || err != nil || n < 0 || n > 24*3600*1000 {
			return
		}
		r.provisioned = true
		log.Record("container.provision", time.Duration(n)*time.Millisecond)
		return
	}
}

// lastJailPerfBlock splits text's last block into its header line and the lines below it; "" for
// both when there is none.
func lastJailPerfBlock(text string) (header, body string) {
	i := strings.LastIndex(text, "\n"+jailPerfBlockPrefix)
	switch {
	case i >= 0:
		text = text[i+1:]
	case strings.HasPrefix(text, jailPerfBlockPrefix):
	default:
		return "", ""
	}
	header, body, _ = strings.Cut(text, "\n")
	return header + "\n", body
}

// parseJailPerfMark reads one checkpoint line of the entrypoint's dump, `<elapsed>s [+<delta>s]
// <label>`; any other line (the total, a program phase, the provisioning stage) is not one.
func parseJailPerfMark(line string) (perf.ForeignMark, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return perf.ForeignMark{}, false
	}
	secs, ok := strings.CutSuffix(fields[0], "s")
	if !ok {
		return perf.ForeignMark{}, false
	}
	at, err := strconv.ParseFloat(secs, 64)
	if err != nil {
		return perf.ForeignMark{}, false
	}
	label := fields[1:]
	if strings.HasPrefix(label[0], "+") {
		label = label[1:]
	}
	if len(label) == 0 {
		return perf.ForeignMark{}, false
	}
	return perf.ForeignMark{At: at, Label: strings.Join(label, " ")}, true
}
