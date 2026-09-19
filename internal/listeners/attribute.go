package listeners

import (
	"sort"
	"strconv"
	"strings"
)

// socketFDPrefix is what a socket file descriptor's /proc/<pid>/fd entry points at.
// It is not a path, which is why [Source.ReadLink] must return the target text
// unresolved.
const socketFDPrefix = "socket:["

// attribute maps socket inodes to the processes holding them, by walking
// /proc/<pid>/fd, and records what it could not see.
//
// ONE PASS, ONLY FOR INODES OF INTEREST. The inode set comes from the tables
// already read, so an fd link that is not one of them costs a string compare and
// nothing else, and a machine with nothing listening costs ZERO readlinks — the walk
// does not start. The alternative shape, building a complete inode→pid map first, pays
// for every socket on the machine to answer about three.
//
// IT DOES NOT STOP EARLY WHEN EVERY INODE HAS AN OWNER. Two processes sharing one
// listening socket (a forked child, a supervisor that passed the fd on) is precisely
// the shape of the bug this package was built for, so the walk is exhaustive within
// its budget rather than satisfied by the first hit.
//
// A process whose fd directory cannot be listed is counted, not reported: exited
// between the readdir and the open, another user's, another namespace's. That is the
// normal case, and treating it as an error would make every snapshot an error.
func attribute(src Source, snap *Snapshot, opts Options) {
	want := map[uint64][]int{}
	for i, l := range snap.Sockets {
		if l.Inode == 0 {
			continue
		}
		want[l.Inode] = append(want[l.Inode], i)
	}
	if len(want) == 0 {
		return
	}

	names, err := src.ReadDirNames(".")
	if err != nil {
		snap.Gaps = append(snap.Gaps, Gap{Source: ".", Reason: err.Error()})
		return
	}
	snap.AttributionRan = true
	pids := pidsAscending(names)
	snap.PIDsFound = len(pids)

	described := map[int]Owner{}
	visited := 0
walk:
	for _, pid := range pids {
		if visited >= opts.MaxPIDs || snap.FDLinksRead >= opts.MaxFDLinks {
			snap.Capped = true
			break
		}
		visited++
		dir := strconv.Itoa(pid) + "/fd"
		fds, err := src.ReadDirNames(dir)
		if err != nil {
			snap.PIDsUnreadable++
			continue
		}
		snap.PIDsScanned++
		for _, fd := range fds {
			if snap.FDLinksRead >= opts.MaxFDLinks {
				snap.Capped = true
				break walk
			}
			target, err := src.ReadLink(dir + "/" + fd)
			snap.FDLinksRead++
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok {
				continue
			}
			idxs, wanted := want[inode]
			if !wanted {
				continue
			}
			owner, known := described[pid]
			if !known {
				owner = describe(src, pid)
				described[pid] = owner
			}
			for _, i := range idxs {
				// A process may hold one listening socket on several
				// descriptors (dup, or an inherited copy); it is one owner.
				if !holdsPID(snap.Sockets[i].Owners, pid) {
					snap.Sockets[i].Owners = append(snap.Sockets[i].Owners, owner)
				}
			}
		}
	}

	if snap.Capped {
		snap.Gaps = append(snap.Gaps, Gap{
			Source: "fd-scan",
			Reason: "budget reached after " + strconv.Itoa(visited) + " of " +
				strconv.Itoa(len(pids)) + " processes and " +
				strconv.Itoa(snap.FDLinksRead) + " readlinks — an owner may be missing",
		})
	}
	if snap.PIDsUnreadable > 0 {
		snap.Gaps = append(snap.Gaps, Gap{
			Source: "<pid>/fd",
			Reason: strconv.Itoa(snap.PIDsUnreadable) + " of " + strconv.Itoa(len(pids)) +
				" process fd directories unreadable (normal: exited, another user, another namespace)",
		})
	}
}

// socketInode reads the inode out of a "socket:[12345]" fd target.
func socketInode(target string) (uint64, bool) {
	if !strings.HasPrefix(target, socketFDPrefix) || !strings.HasSuffix(target, "]") {
		return 0, false
	}
	n, err := strconv.ParseUint(target[len(socketFDPrefix):len(target)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// pidsAscending keeps the all-numeric entries of /proc and sorts them numerically.
// The order matters twice: it makes the pid budget cut at a reproducible place, and
// it makes a socket's Owners list deterministic.
func pidsAscending(names []string) []int {
	out := make([]int, 0, len(names))
	for _, n := range names {
		pid, err := strconv.Atoi(n)
		if err != nil || pid <= 0 || strings.HasPrefix(n, "+") {
			continue
		}
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

// describe reads a pid's name. Both files are read at most once per owning process,
// which is why they are read here and not during the fd walk.
func describe(src Source, pid int) Owner {
	o := Owner{PID: pid}
	if b, err := src.ReadFile(strconv.Itoa(pid) + "/comm"); err == nil {
		o.Comm = strings.TrimRight(string(b), "\n")
	}
	if b, err := src.ReadFile(strconv.Itoa(pid) + "/cmdline"); err == nil {
		o.Cmdline = cmdlineText(b)
	}
	return o
}

// cmdlineText turns /proc/<pid>/cmdline's NUL-separated argv into one line. An
// empty result is legitimate — kernel threads have no cmdline.
func cmdlineText(b []byte) string {
	s := strings.TrimRight(string(b), "\x00")
	return strings.ReplaceAll(s, "\x00", " ")
}

func holdsPID(owners []Owner, pid int) bool {
	for _, o := range owners {
		if o.PID == pid {
			return true
		}
	}
	return false
}
