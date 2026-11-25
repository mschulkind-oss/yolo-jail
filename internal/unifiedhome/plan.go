// Package unifiedhome merges every home a workspace holds into one: the unified workspace home
// (docs/design/unified-workspace-home.md). This file is the pure layer, listings in and steps out,
// and touches nothing; merge.go applies the steps beneath no-link descriptors.
package unifiedhome

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind is what a directory entry is, read without following a link.
type Kind int

const (
	KindFile  Kind = iota // a regular file
	KindDir               // a directory
	KindLink              // a symbolic link, never followed
	KindOther             // a socket, fifo, device
)

// Entry is one child of a directory, as listed.
type Entry struct {
	Name    string
	Kind    Kind
	Size    int64
	ModTime time.Time // set for regular files; the later of two colliding files keeps the name
}

// Op is what to do with one source entry.
type Op int

const (
	// OpRename moves the entry to the destination under its own name: nothing is there.
	OpRename Op = iota
	// OpRecurse merges a directory against a directory.
	OpRecurse
	// OpSkip leaves a source file whose bytes the destination already holds.
	OpSkip
	// OpKeepBoth moves the source entry to Target, beside the destination's one of that name.
	OpKeepBoth
	// OpDisplace moves the destination's entry to Target, then the (newer) source file takes Name.
	OpDisplace
	// OpLeave leaves the source entry where it is and counts it; Reason says why.
	OpLeave
)

// Step is one planned action at one directory level.
type Step struct {
	Name   string
	Op     Op
	Target string // the free ".from-<label>" name, for OpKeepBoth and OpDisplace
	Reason string // for OpLeave
}

// PlanLevel plans one directory level of a merge. src and dst are the two listings, sameBytes
// says whether the regular files of that name hold identical bytes, and label names the source
// in the ".from-" names. Nothing is touched; the steps come back in name order, and every
// ".from-" target is free in dst, in src and among the steps already planned.
func PlanLevel(src, dst []Entry, sameBytes func(name string) bool, label string) []Step {
	label = strings.ReplaceAll(label, "/", "_")
	ordered := append([]Entry(nil), src...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	have := make(map[string]Entry, len(dst))
	taken := make(map[string]bool, len(dst)+len(src))
	for _, d := range dst {
		have[d.Name] = d
		taken[d.Name] = true
	}
	for _, s := range src {
		taken[s.Name] = true
	}
	free := func(name string) string {
		base := name + ".from-" + label
		cand := base
		for n := 2; taken[cand]; n++ {
			cand = base + "." + strconv.Itoa(n)
		}
		taken[cand] = true
		return cand
	}
	steps := make([]Step, 0, len(ordered))
	for _, s := range ordered {
		if why := leftReason(s); why != "" {
			steps = append(steps, Step{Name: s.Name, Op: OpLeave, Reason: why})
			continue
		}
		d, ok := have[s.Name]
		switch {
		case !ok:
			steps = append(steps, Step{Name: s.Name, Op: OpRename})
		case s.Kind == KindDir && d.Kind == KindDir:
			steps = append(steps, Step{Name: s.Name, Op: OpRecurse})
		case s.Kind == KindFile && d.Kind == KindFile:
			switch {
			case sameBytes != nil && sameBytes(s.Name):
				steps = append(steps, Step{Name: s.Name, Op: OpSkip})
			case s.ModTime.After(d.ModTime):
				steps = append(steps, Step{Name: s.Name, Op: OpDisplace, Target: free(s.Name)})
			default:
				steps = append(steps, Step{Name: s.Name, Op: OpKeepBoth, Target: free(s.Name)})
			}
		default:
			// A link on either side, or a file against a directory: both stay.
			steps = append(steps, Step{Name: s.Name, Op: OpKeepBoth, Target: free(s.Name)})
		}
	}
	return steps
}

// leftReason says why an entry is never merged: a socket, fifo or device, and a lock or pid file
// that belongs to a process that may still be running.
func leftReason(e Entry) string {
	switch {
	case e.Kind == KindOther:
		return "a socket, fifo or device"
	case e.Kind == KindFile && isProcessFile(e.Name):
		return "a lock or pid file"
	}
	return ""
}

func isProcessFile(name string) bool {
	switch name {
	case "lock", "pid", "SingletonLock", "SingletonSocket", "SingletonCookie":
		return true
	}
	return strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".pid") || strings.HasSuffix(name, ".sock")
}
