package unifiedhome

import (
	"os"
	"path"
	"strings"
)

// Pending counts, read-only, the regular files a Merge of src would carry out of its home: every
// regular file beneath src.Dirs that is not a link, not a lock or pid file (leftReason), and not at
// or under a src.Stay path. Links are never followed or counted (a composed slice is made of them),
// and an unreadable directory counts as holding nothing. It is the plan `yolo check` shows; nothing
// is touched.
func Pending(src Source) int {
	n := 0
	for _, dir := range src.Dirs {
		n += pendingBelow(src.Root, dir, dir, src.Stay)
	}
	return n
}

func pendingBelow(root *os.Root, dir, base string, stay []string) int {
	if fi, err := root.Lstat(dir); err != nil || !fi.IsDir() {
		return 0
	}
	f, err := root.Open(dir)
	if err != nil {
		return 0
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		if staysPut(strings.TrimPrefix(p, base+"/"), stay) {
			continue
		}
		switch {
		case e.Type()&os.ModeSymlink != 0:
		case e.IsDir():
			n += pendingBelow(root, p, base, stay)
		case e.Type().IsRegular():
			if leftReason(Entry{Name: e.Name(), Kind: KindFile}) == "" {
				n++
			}
		}
	}
	return n
}

// staysPut reports whether rel (relative to a state directory) is a Stay path or beneath one.
func staysPut(rel string, stay []string) bool {
	for _, s := range stay {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}
