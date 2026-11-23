package jailcontent

// The TOOL-SKILLS LAYER (docs/design/mise-tool-skills.md, slice 4): the skills a mise tool the user
// named in their own `install.mise` declares, which the jail snapshotted into
// <workspace>/.yolo/tool-skills (internal/toolskills) and the host composes here as the LOWEST
// layer of every destination — below the packs, the built-ins, yolo's LSP plugin and the
// repository's own mirrored skills. A name any of them holds stays with it.
//
// THE SNAPSHOT IS UNTRUSTED. The jail writes it and so can the repository, so the read is the
// workspace layer's: one descriptor walk, every component opened O_NOFOLLOW, nothing resolved
// through a link, every size and count bounded. It is composed only when its index carries the
// workspace's snapshot token (MS-D9), which a repository cannot know. The allowlist, the names
// and the caps are applied again here (MS-D5): the index is read for what it names, never for the
// order it gives.
//
// Any failure is NO LAYER, never a refused launch: the snapshot is an optimization of what an
// agent sees, and a launch that cannot trust it simply delivers without it.

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/toolskills"
)

// ToolSkillsInput is one launch's or attach's request for the tool-skills layer. The zero value,
// and any value with an empty Tools or Token, asks for no layer.
type ToolSkillsInput struct {
	// Workspace is the workspace root on the host.
	Workspace string
	// Token is the workspace's snapshot token (config.ToolSkillsToken); the index must carry it.
	Token string
	// Tools is the allowlist, in the user's declaration order (config.MiseSkillTools).
	Tools []string
}

// ToolSkillsReport is what the tool-skills layer did, for the caller to say. Slice 5 turns it into
// the lines of the design's section 6.2 and sets YOLO_TOOL_SKILLS_DELIVERED from Digest; this
// package prints nothing.
type ToolSkillsReport struct {
	// Digest is the content digest of the snapshot composed, "" when no layer was composed.
	Digest string
	// Delivered is every skill name delivered to at least one destination, in layer order.
	Delivered []string
	// Shadowed is one entry per skill a higher layer took the name of, once per name.
	Shadowed []ToolSkillShadow
	// Skipped is one entry per skill the read refused (a link, a device, the cap, a vanished
	// directory) or the allowlist dropped, with fixed, printable text.
	Skipped []ToolSkillSkip
}

// ToolSkillShadow is a tool's skill a higher layer already provided.
type ToolSkillShadow struct{ Name, Tool, By string }

// ToolSkillSkip is a skill the layer did not take, and why. Winner is the tool that holds the
// name when the reason is that an earlier tool in the allowlist came first, and "" otherwise.
type ToolSkillSkip struct{ Name, Tool, Reason, Winner string }

type toolSkill struct {
	name, tool string
	stored     string // the copy in scratch
}

// toolSkillsLayer is the snapshot's skills, copied ONCE per composition into a private scratch
// tree (which holds no links) and handed to each destination from there.
type toolSkillsLayer struct {
	dir    string
	skills []toolSkill
	report ToolSkillsReport
	seen   map[string]bool
}

const (
	toolIndexMaxBytes   = 1 << 20
	toolIndexMaxSkills  = 4096
	toolMaxDepth        = 64
	toolSnapshotVersion = 1
)

type toolIndexSkill struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

type toolIndex struct {
	Version int              `json:"version"`
	Token   string           `json:"token"`
	Digest  string           `json:"digest"`
	Skills  []toolIndexSkill `json:"skills"`
}

// ToolSkillsDelivered is the content digest of the snapshot a composition with in would read,
// "" when there is no layer (no allowlist, no token, no snapshot, or an index without the token).
// It is the value of YOLO_TOOL_SKILLS_DELIVERED (design MS-D7), and the same digest the layer's
// report carries.
func ToolSkillsDelivered(in *ToolSkillsInput) string {
	if in == nil || in.Workspace == "" || in.Token == "" || len(in.Tools) == 0 {
		return ""
	}
	_, digest, _, closeAll, ok := openToolSnapshot(in)
	if !ok {
		return ""
	}
	closeAll()
	return digest
}

// stageToolSkillsLayer reads the snapshot. It returns nil when there is no layer.
func stageToolSkillsLayer(in *ToolSkillsInput) *toolSkillsLayer {
	if in == nil || in.Workspace == "" || in.Token == "" || len(in.Tools) == 0 {
		return nil
	}
	idx, digest, skillsFD, closeAll, ok := openToolSnapshot(in)
	if !ok {
		return nil
	}
	defer closeAll()

	scratch, err := os.MkdirTemp(scratchBase, "yolo-toolskills-")
	if err != nil {
		return nil
	}
	l := &toolSkillsLayer{dir: scratch, seen: map[string]bool{}}
	l.report.Digest = digest

	// MS-D5: re-apply the allowlist, never the index's order: allowlist position, then name.
	rank := map[string]int{}
	for i, t := range in.Tools {
		if _, dup := rank[t]; !dup {
			rank[t] = i
		}
	}
	var picked []toolIndexSkill
	for _, s := range idx.Skills {
		if _, ok := rank[s.Tool]; ok {
			picked = append(picked, s)
		}
	}
	sort.SliceStable(picked, func(i, j int) bool {
		if ri, rj := rank[picked[i].Tool], rank[picked[j].Tool]; ri != rj {
			return ri < rj
		}
		return picked[i].Name < picked[j].Name
	})
	caps := workspaceCaps
	spent := workspaceSkillCaps{}
	owner := map[string]string{}
	for n, s := range picked {
		tool := toolskills.Sanitize(s.Tool)
		name := toolskills.Sanitize(s.Name)
		skip := func(reason string) {
			l.report.Skipped = append(l.report.Skipped, ToolSkillSkip{Name: name, Tool: tool, Reason: reason})
		}
		if n >= toolIndexMaxSkills {
			skip("the snapshot names too many skills")
			continue
		}
		if !toolskills.ValidName(s.Name) {
			skip("not a usable skill name")
			continue
		}
		if first, dup := owner[s.Name]; dup {
			skip(toolskills.Sanitize(first) + " comes first in your install.mise and wins")
			l.report.Skipped[len(l.report.Skipped)-1].Winner = toolskills.Sanitize(first)
			continue
		}
		stored := filepath.Join(scratch, strconv.Itoa(len(l.skills)))
		reason := copyToolSkill(skillsFD, s.Name, stored, &spent, caps)
		if reason != "" {
			_ = os.RemoveAll(stored)
			// What a refused skill wrote stays charged, as the workspace layer does.
			skip(reason)
			continue
		}
		owner[s.Name] = s.Tool
		l.skills = append(l.skills, toolSkill{name: s.Name, tool: s.Tool, stored: stored})
	}
	return l
}

// openToolSnapshot opens the snapshot's skills directory by a no-follow walk from the workspace
// and returns the validated index. Any problem is !ok.
func openToolSnapshot(in *ToolSkillsInput) (idx toolIndex, digest string, skillsFD int, closeAll func(), ok bool) {
	var fds []int
	closeAll = func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}
	fail := func() (toolIndex, string, int, func(), bool) {
		closeAll()
		return toolIndex{}, "", -1, func() {}, false
	}
	// The root is the launcher's own, trusted spelling; everything below it is not.
	root, err := unix.Open(in.Workspace, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	fds = append(fds, root)
	cur := root
	for _, part := range []string{".yolo", toolskills.SnapshotDirName} {
		fd, err := openat(cur, part, dirOpenFlags)
		if err != nil {
			return fail()
		}
		fds = append(fds, fd)
		cur = fd
	}
	snapFD := cur
	cb, err := readSmallAt(snapFD, "current", 256)
	if err != nil {
		return fail()
	}
	digest = trimSpaceBytes(cb)
	if !validDigest(digest) {
		return fail()
	}
	digestFD, err := openat(snapFD, digest, dirOpenFlags)
	if err != nil {
		return fail()
	}
	fds = append(fds, digestFD)
	body, err := readSmallAt(digestFD, "index.json", toolIndexMaxBytes)
	if err != nil {
		return fail()
	}
	if err := json.Unmarshal(body, &idx); err != nil {
		return fail()
	}
	// The token is the whole of MS-D9: an index a repository wrote cannot carry it.
	if idx.Version != toolSnapshotVersion || subtle.ConstantTimeCompare([]byte(idx.Token), []byte(in.Token)) != 1 ||
		idx.Digest != digest {
		return fail()
	}
	sfd, err := openat(digestFD, "skills", dirOpenFlags)
	if err != nil {
		return fail()
	}
	fds = append(fds, sfd)
	return idx, digest, sfd, closeAll, true
}

func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func trimSpaceBytes(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// readSmallAt reads a regular file named under dir, no-follow, refusing anything over max bytes.
func readSmallAt(dir int, name string, max int64) ([]byte, error) {
	fd, err := openat(dir, name, fileOpenFlags)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > max {
		return nil, errors.New("not a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, errors.New("unreadable or too large")
	}
	return b, nil
}

// copyToolSkill copies the skill directory `name` under skillsFD into dst, charging spent against
// caps. A non-empty return is the reason the skill is skipped (fixed text). The snapshot's writer
// never links, so a link, device or FIFO anywhere in a skill means the tree was not written by it:
// the whole skill is skipped.
func copyToolSkill(skillsFD int, name, dst string, spent *workspaceSkillCaps, caps workspaceSkillCaps) string {
	fd, err := openat(skillsFD, name, dirOpenFlags)
	if err != nil {
		return "its directory is not in the snapshot, or is not a plain directory"
	}
	defer unix.Close(fd)
	return copyToolDir(fd, dst, len(name), 0, spent, caps)
}

func copyToolDir(fd int, dst string, pathLen, depth int, spent *workspaceSkillCaps, caps workspaceSkillCaps) string {
	if depth > toolMaxDepth {
		return "it nests too deeply"
	}
	if spent.entries+1 > caps.entries {
		return "the snapshot's entry cap is reached"
	}
	spent.entries++
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "it could not be staged"
	}
	names, err := readNames(fd)
	if err != nil {
		return "it could not be read"
	}
	for _, n := range names {
		if pathLen+1+len(n) > maxSkillPath {
			return "a path inside it is too long"
		}
		var st unix.Stat_t
		if err := fstatat(fd, n, &st); err != nil {
			return "it changed while it was being read"
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			sub, err := openat(fd, n, dirOpenFlags)
			if err != nil {
				return "it changed while it was being read"
			}
			reason := copyToolDir(sub, filepath.Join(dst, n), pathLen+1+len(n), depth+1, spent, caps)
			unix.Close(sub)
			if reason != "" {
				return reason
			}
		case unix.S_IFREG:
			if reason := copyToolFile(fd, n, filepath.Join(dst, n), spent, caps); reason != "" {
				return reason
			}
		default:
			return "it holds a link or special file the snapshot never contains"
		}
	}
	return ""
}

func copyToolFile(parent int, base, dst string, spent *workspaceSkillCaps, caps workspaceSkillCaps) string {
	fd, err := openat(parent, base, fileOpenFlags)
	if err != nil {
		return "it changed while it was being read"
	}
	in := os.NewFile(uintptr(fd), base)
	defer in.Close()
	fi, err := in.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return "it changed while it was being read"
	}
	if spent.entries+1 > caps.entries || spent.bytes+fi.Size() > caps.bytes {
		return "the snapshot's size cap is reached"
	}
	spent.entries++
	spent.bytes += fi.Size()
	readErr, writeErr := writeCopy(&boundedReader{r: in, left: fi.Size()}, dst, fi.Mode())
	if readErr != nil {
		return "it changed while it was being read"
	}
	if writeErr != nil {
		return "it could not be staged"
	}
	return ""
}

// deliver copies the layer's skills into one destination's composed tree, into the names every
// layer above left free. taken holds the names a layer above wrote or reserved; a name present in
// skillsDir is taken too (the workspace layer, written just before, does not add to taken).
func (l *toolSkillsLayer) deliver(skillsDir string, taken map[string]string) error {
	if l == nil {
		return nil
	}
	for _, s := range l.skills {
		by, isTaken := taken[s.name]
		if !isTaken {
			if _, err := os.Lstat(filepath.Join(skillsDir, s.name)); err == nil {
				by, isTaken = "the workspace's skills", true
			}
		}
		if isTaken {
			l.shadow(s, by)
			continue
		}
		if err := copyPlainTree(s.stored, filepath.Join(skillsDir, s.name)); err != nil {
			return err
		}
		if !l.seen[s.name] {
			l.seen[s.name] = true
			l.report.Delivered = append(l.report.Delivered, s.name)
		}
	}
	return nil
}

func (l *toolSkillsLayer) shadow(s toolSkill, by string) {
	for _, sh := range l.report.Shadowed {
		if sh.Name == s.name {
			return
		}
	}
	l.report.Shadowed = append(l.report.Shadowed, ToolSkillShadow{
		Name: toolskills.Sanitize(s.name), Tool: toolskills.Sanitize(s.tool), By: by})
}

func (l *toolSkillsLayer) close() {
	if l != nil && l.dir != "" {
		_ = os.RemoveAll(l.dir)
		l.dir = ""
	}
}

func (l *toolSkillsLayer) finish(rep *WorkspaceSkillsReport) {
	if l == nil || rep == nil {
		return
	}
	r := l.report
	rep.ToolSkills = &r
}
