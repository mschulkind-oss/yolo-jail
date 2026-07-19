package cgd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// ensureAgentCgroup ensures the agent cgroup subtree exists with controllers
// enabled, returning its path or "" on failure.
// mkdir init+agent, move existing procs to init (cgroup v2 no-internal-process
// rule), enable cpu/memory/pids on root->agent subtree_control.
func ensureAgentCgroup(containerCgroup string) string {
	agentCg := filepath.Join(containerCgroup, "agent")
	initCg := filepath.Join(containerCgroup, "init")
	if dirExists(agentCg) {
		return agentCg
	}
	if err := os.Mkdir(initCg, 0o755); err != nil && !os.IsExist(err) {
		return ""
	}
	if err := os.Mkdir(agentCg, 0o755); err != nil && !os.IsExist(err) {
		return ""
	}
	// Move existing processes to 'init'.
	if b, err := os.ReadFile(filepath.Join(containerCgroup, "cgroup.procs")); err == nil {
		for _, pid := range strings.Fields(string(b)) {
			_ = os.WriteFile(filepath.Join(initCg, "cgroup.procs"), []byte(pid), 0o644)
		}
	}
	// Enable controllers root -> agent.
	for _, cg := range []string{containerCgroup, agentCg} {
		b, err := os.ReadFile(filepath.Join(cg, "cgroup.controllers"))
		if err != nil {
			continue
		}
		available := strings.Fields(string(b))
		var wanted []string
		for _, c := range []string{"cpu", "memory", "pids"} {
			if contains(available, c) {
				wanted = append(wanted, "+"+c)
			}
		}
		if len(wanted) > 0 {
			_ = os.WriteFile(filepath.Join(cg, "cgroup.subtree_control"),
				[]byte(strings.Join(wanted, " ")), 0o644)
		}
	}
	return agentCg
}

// createAndJoin creates a child cgroup under agent/, applies limits, and moves
// the caller (peerPID) into it.
// cpu.max quota formula (pct*1000*nproc / 100000 period), the 1MB memory floor,
// the pid range (1..1000000), and the warnings collection.
func createAndJoin(containerCgroup, name string, r *Request, peerPID int) *jsonx.OrderedMap {
	agentCg := ensureAgentCgroup(containerCgroup)
	if agentCg == "" {
		return errResp("Failed to set up agent cgroup hierarchy")
	}
	jobCg := filepath.Join(agentCg, name)
	if err := os.Mkdir(jobCg, 0o755); err != nil && !os.IsExist(err) {
		return errResp("Cannot create cgroup " + name + ": " + err.Error())
	}

	var errs []string

	// CPU: percentage of all CPUs -> cpu.max quota.
	if r.present("cpu_pct") {
		pct, ok := r.intField("cpu_pct")
		if !ok {
			errs = append(errs, "cpu.max: invalid cpu_pct")
		} else {
			nproc := int64(cpuCount())
			if pct < 1 || pct > 100*nproc {
				errs = append(errs, "cpu_pct out of range: "+strconv.FormatInt(pct, 10))
			} else {
				quota := pct * 1000 * nproc
				if err := os.WriteFile(filepath.Join(jobCg, "cpu.max"),
					[]byte(strconv.FormatInt(quota, 10)+" 100000"), 0o644); err != nil {
					errs = append(errs, "cpu.max: "+err.Error())
				}
			}
		}
	}

	// Memory.
	if r.present("memory") {
		memBytes, ok := ParseMemoryValue(r.rawValue("memory"))
		if !ok || memBytes < 1048576 {
			errs = append(errs, "Invalid memory value: "+r.rawValue("memory"))
		} else if err := os.WriteFile(filepath.Join(jobCg, "memory.max"),
			[]byte(strconv.FormatInt(memBytes, 10)), 0o644); err != nil {
			errs = append(errs, "memory.max: "+err.Error())
		}
	}

	// PIDs.
	if r.present("pids") {
		pidsVal, ok := r.intField("pids")
		if !ok {
			errs = append(errs, "pids.max: invalid pids")
		} else if pidsVal < 1 || pidsVal > 1000000 {
			errs = append(errs, "pids out of range: "+strconv.FormatInt(pidsVal, 10))
		} else if err := os.WriteFile(filepath.Join(jobCg, "pids.max"),
			[]byte(strconv.FormatInt(pidsVal, 10)), 0o644); err != nil {
			errs = append(errs, "pids.max: "+err.Error())
		}
	}

	// Move the caller in.
	if err := os.WriteFile(filepath.Join(jobCg, "cgroup.procs"),
		[]byte(strconv.Itoa(peerPID)), 0o644); err != nil {
		resp := errResp("Cannot move PID " + strconv.Itoa(peerPID) + " into cgroup: " + err.Error())
		resp.Set("limit_errors", strSlice(errs))
		return resp
	}

	cgPath := relToCgroupRoot(jobCg)
	resp := okResp("cgroup", cgPath)
	if len(errs) > 0 {
		resp.Set("warnings", strSlice(errs))
	}
	return resp
}

// destroy removes an empty child cgroup (idempotent).
// the procs read AND the rmdir are in ONE try/except OSError, so a read error
// takes the same "Cannot remove" path as an rmdir error (Python semantics —
// on a real cgroup fs cgroup.procs always exists, so this only matters on the
// fake test tree).
func destroy(containerCgroup, name string) *jsonx.OrderedMap {
	jobCg := filepath.Join(containerCgroup, "agent", name)
	if !dirExists(jobCg) {
		return okResp() // already gone — idempotent
	}
	b, err := os.ReadFile(filepath.Join(jobCg, "cgroup.procs"))
	if err != nil {
		return errResp("Cannot remove cgroup " + name + ": " + err.Error())
	}
	if procs := strings.TrimSpace(string(b)); procs != "" {
		return errResp("Cgroup " + name + " still has processes: " + procs)
	}
	if err := os.Remove(jobCg); err != nil {
		return errResp("Cannot remove cgroup " + name + ": " + err.Error())
	}
	return okResp()
}

func relToCgroupRoot(jobCg string) string {
	root := "/sys/fs/cgroup"
	if rel, err := filepath.Rel(root, jobCg); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return jobCg
}

// reprName renders {name!r} for error messages (Python repr).
func reprName(s string) string { return pytext.Repr(s) }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func strSlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
