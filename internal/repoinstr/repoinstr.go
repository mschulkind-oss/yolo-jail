// Package repoinstr resolves the `repo_instructions` config table for one launch: the mode, the
// replacement text, the selection every pack is handed, the one-line wire form that carries it into
// a jail, and the disclosure a launch prints (docs/design/workspace-instruction-files.md).
//
// THERE IS NO MASK, by the owner's ruling (IF-D14). Nothing in this package, or anything built on
// it, binds, replaces or hides a workspace file; an agent whose pack declares no mechanism is
// unsupported, and its launch continues and says it still reads the files (IF-D15).
package repoinstr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// EnvName is the session input that carries a launch's mode into a jail's boot (and macos-user's
// bootstrap), where the entrypoint rebuilds the selection every loaded pack is handed.
const EnvName = "YOLO_REPO_INSTRUCTIONS"

// maxReplaceBytes bounds the replacement file read. The text counts toward the briefing's size
// line and yolo never trims it, so this only stops a mistaken path from reading a huge file.
const maxReplaceBytes = 4 << 20

// Resolved is one launch's repo_instructions, after the replacement file was read.
type Resolved struct {
	// Mode is keep, ignore or replace. A replace whose file is empty or whitespace-only resolves to
	// ignore (IF-D11): no empty section is composed.
	Mode string
	// ReplaceWith is the path as the user spelled it, for the disclosure and the section's lead line.
	ReplaceWith string
	// ReplaceText is the replacement's text, "" unless Mode is replace.
	ReplaceText string
}

// Ignoring reports whether the launch asks agents to stop reading the repository's files.
func (r *Resolved) Ignoring() bool {
	return r != nil && (r.Mode == config.RepoInstructionsModeIgnore || r.Mode == config.RepoInstructionsModeReplace)
}

// Resolve reads the table out of cfg and, for replace, the file it names.
//
// WHERE THE PATH IS READ FROM turns on the layer that wrote it (design §3.1): a value the user scope
// carries is the user's own and is read as spelled (an absolute path or `~/…`); any other is a
// project layer's, which names a path INSIDE the workspace, read without following a link, so a
// repository cannot point a launch at a file of the host. A launch whose replacement cannot be read
// is refused, naming the path and the command that removes it.
func Resolve(cfg *jsonx.OrderedMap, workspace string) (*Resolved, error) {
	mode, with := config.RepoInstructions(cfg)
	out := &Resolved{Mode: mode}
	if mode != config.RepoInstructionsModeReplace {
		return out, nil
	}
	if strings.TrimSpace(with) == "" {
		return nil, errors.New("repo_instructions.mode is \"replace\" but repo_instructions.replace_with names no file; " +
			"`yolo config set repo_instructions.replace_with <path>` names one, or " +
			"`yolo config set repo_instructions.mode ignore` ignores the repository's files without a replacement")
	}
	out.ReplaceWith = with
	_, userWith := config.RepoInstructionsUser()
	var text []byte
	var err error
	if with == userWith {
		text, err = readUserFile(with)
	} else {
		text, err = ReadWorkspaceFile(workspace, with)
	}
	if err != nil {
		return nil, fmt.Errorf("repo_instructions.replace_with %q cannot be used: %v; "+
			"fix the path, or `yolo config unset repo_instructions.replace_with` removes it", with, err)
	}
	if strings.TrimSpace(string(text)) == "" {
		out.Mode = config.RepoInstructionsModeIgnore
		return out, nil
	}
	out.ReplaceText = strings.TrimRight(string(text), "\n") + "\n"
	return out, nil
}

// readUserFile reads a user-written path as spelled: absolute, or `~/` under the home.
func readUserFile(path string) ([]byte, error) {
	p := path
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return nil, errors.New("a path in your own config is absolute or starts with ~/")
	}
	return readRegular(p, false)
}

// ReadWorkspaceFile reads a path a PROJECT layer named: relative, inside the workspace, and without
// following a link at any step (the walk opens each component with O_NOFOLLOW).
func ReadWorkspaceFile(workspace, rel string) ([]byte, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "~") || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, errors.New("a project config names a path inside the workspace, relative to it")
	}
	if workspace == "" {
		return nil, errors.New("the workspace is unknown")
	}
	cur := workspace
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a link, and a project config's path is read without following one", filepath.Join(parts[:i+1]...))
		}
	}
	return readRegular(cur, true)
}

func readRegular(path string, noFollow bool) ([]byte, error) {
	flags := os.O_RDONLY
	if noFollow {
		flags |= syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("it is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReplaceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReplaceBytes {
		return nil, fmt.Errorf("it is larger than %d MiB", maxReplaceBytes>>20)
	}
	return data, nil
}

// Section is the replacement as one titled briefing section, "" when the launch replaces nothing.
// It joins the briefing through the user block, the way `agents_md_extra` does, so it lands in every
// destination a selected pack declares (design §5).
func (r *Resolved) Section() string {
	if r == nil || r.Mode != config.RepoInstructionsModeReplace || r.ReplaceText == "" {
		return ""
	}
	return "## Instructions that replace this repository's own\n\n" +
		"The user replaced the instruction files this repository carries (AGENTS.md, CLAUDE.md and their kin) " +
		"with the text below, from `" + r.ReplaceWith + "`. Follow it in their place.\n\n" + r.ReplaceText
}

// WithExtra appends the replacement section to the user's `agents_md_extra` text.
func (r *Resolved) WithExtra(extra string) string {
	section := r.Section()
	if section == "" {
		return extra
	}
	if strings.TrimSpace(extra) == "" {
		return section
	}
	return strings.TrimRight(extra, "\n") + "\n\n" + section
}

// Selection is the value every pack is handed for this launch (packload.ApplyInstructions): the
// mode, and where the agent sees the workspace and its home. host is true on the unconfined path.
// nil for a keep launch, so a launch that keeps the files composes as it did before the key existed.
func (r *Resolved) Selection(workspace, home string, host bool) *packdecl.InstructionsSelection {
	if !r.Ignoring() {
		return nil
	}
	return &packdecl.InstructionsSelection{Mode: r.Mode, Workspace: workspace, Home: home, Host: host}
}

// Wire is the session input's value: the mode alone, since the booting side knows its own workspace
// and home. "" for a launch that keeps the files.
func (r *Resolved) Wire() string {
	if !r.Ignoring() {
		return ""
	}
	b, _ := json.Marshal(struct {
		Mode string `json:"mode"`
	}{r.Mode})
	return string(b)
}

// WireFromConfig is Wire from the config alone, for the session-input composer, which has the
// effective config and no resolved file. A replace and an ignore cross as they were written: the
// booting side ignores in both, and the replacement text is composed host-side into the briefing.
func WireFromConfig(cfg *jsonx.OrderedMap) string {
	mode, _ := config.RepoInstructions(cfg)
	return (&Resolved{Mode: mode}).Wire()
}

// FromWire rebuilds the selection a jail's boot hands its packs from the session input, with the
// workspace and home as the booting side sees them. nil for an empty, unreadable or keep value.
func FromWire(raw, workspace, home string) *packdecl.InstructionsSelection {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var w struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal([]byte(raw), &w) != nil {
		return nil
	}
	if w.Mode != config.RepoInstructionsModeIgnore && w.Mode != config.RepoInstructionsModeReplace {
		return nil
	}
	return &packdecl.InstructionsSelection{Mode: w.Mode, Workspace: workspace, Home: home}
}
