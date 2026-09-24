package lingerprobe

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// HostContext is how busy the machine's podman was at the moment the
// container died — the "busier machine, slower quit" hypothesis, from /proc
// alone (no podman call: podman's own lock is one of the suspects).
type HostContext struct {
	Podman int // processes named podman owned by uid (a rootless client is TWO: wrapper + child)
	Conmon int // conmon processes owned by uid
	// ExecClients counts `podman exec` clients naming this container — other
	// attached terminals — counted once per client (a rootless wrapper and the
	// podman it spawned are one client).
	ExecClients int
	// OtherYoloConmons is conmons whose container name is another yolo jail's.
	OtherYoloConmons int
	Loadavg          string
}

// Line renders the context as a note detail.
func (h HostContext) Line() string {
	return fmt.Sprintf("podman_procs=%d conmons=%d exec_clients_this_jail=%d other_yolo_jails=%d loadavg=%q",
		h.Podman, h.Conmon, h.ExecClients, h.OtherYoloConmons, h.Loadavg)
}

// ReadHostContext scans the /proc root once. cname is this jail's container
// name; yolo's names all start with "yolo-".
func (s Sampler) ReadHostContext(uid int, cname string) HostContext {
	var h HostContext
	if b, err := os.ReadFile(s.path("loadavg")); err == nil {
		h.Loadavg = strings.TrimSpace(string(b))
	}
	ents, err := os.ReadDir(s.Root)
	if err != nil {
		return h
	}
	type execClient struct{ pid, ppid int }
	var execs []execClient
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fi, err := os.Stat(s.path(e.Name()))
		if err != nil {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		comm := ""
		if b, err := os.ReadFile(s.path(e.Name(), "comm")); err == nil {
			comm = baseComm(strings.TrimSpace(string(b)))
		}
		switch comm {
		case "podman":
			h.Podman++
			args := s.cmdline(pid)
			if hasArg(args, "exec") && hasArg(args, cname) {
				st, _ := s.Stat(pid)
				execs = append(execs, execClient{pid, st.PPID})
			}
		case "conmon":
			h.Conmon++
			if n := conmonName(s.cmdline(pid)); strings.HasPrefix(n, "yolo-") && n != cname {
				h.OtherYoloConmons++
			}
		}
	}
	isExec := map[int]bool{}
	for _, c := range execs {
		isExec[c.pid] = true
	}
	for _, c := range execs {
		if !isExec[c.ppid] { // the rootless child of a counted wrapper is the same client
			h.ExecClients++
		}
	}
	return h
}

// baseComm undoes nix's wrapper naming: a wrapped program runs as
// `.<name>-wrapped`, and comm is that name truncated to 15 bytes — measured
// here, `podman` is `.podman-wrapped`. Every comm comparison goes through this.
func baseComm(comm string) string {
	if strings.HasPrefix(comm, ".") {
		c := strings.TrimPrefix(comm, ".")
		if i := strings.Index(c, "-wrap"); i > 0 {
			return c[:i]
		}
	}
	return comm
}

func (s Sampler) cmdline(pid int) []string {
	b, err := os.ReadFile(s.path(strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// conmonName is conmon's `-n <name>` / `--name <name>` (podman passes the
// container's name).
func conmonName(args []string) string {
	for i, a := range args {
		switch {
		case (a == "-n" || a == "--name") && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "--name="):
			return strings.TrimPrefix(a, "--name=")
		}
	}
	return ""
}
