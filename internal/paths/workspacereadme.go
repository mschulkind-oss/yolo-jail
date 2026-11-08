package paths

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The two files yolo keeps current in <workspace>/.yolo besides creating it
// (docs/design/workspace-state-safety.md §3.1): README.md, which says what the directory holds
// and that the agents' own sessions are in it, and the .gitignore, refreshed only while its
// bytes are a text yolo itself shipped. Both are written by EnsureWorkspaceStateDir, every
// time it runs, so every creator of `.yolo` keeps them current without being taught about them.

// WorkspaceStateReadmeName is the file that says what <workspace>/.yolo holds.
const WorkspaceStateReadmeName = "README.md"

// shippedWorkspaceStateIgnoreSHA256 lists the SHA-256 of every text yolo has ever written as
// .yolo/.gitignore, the current one included. A file whose bytes hash to one of these is yolo's
// own text, not a user's edit, so EnsureWorkspaceStateDir replaces it with the current text;
// any other content is the user's and is never touched (SD-D10).
//
// ⚠ APPEND, NEVER REPLACE: when WorkspaceStateIgnore changes, add the new text's hash and keep
// every old one, or the workspaces that hold the old text keep it forever.
// TestEveryShippedIgnoreTextIsRecognised pins the list against the texts themselves.
var shippedWorkspaceStateIgnoreSHA256 = []string{
	// 7f52f2406 (2026-09-12) to SD-D10: the text that named the retired `home/` layout.
	"73bfa39303e0296f2f6d79cf3c9315c7405d3f5eed2755e955387d715cf23b6b",
	// SD-D10: the current WorkspaceStateIgnore.
	"e3ef0b71e2fc3cd03912299001bf4d1a1909037fd79863f9cbf4880097c75a8d",
}

// isShippedWorkspaceStateIgnore reports whether b is byte-for-byte a text yolo shipped.
func isShippedWorkspaceStateIgnore(b []byte) bool {
	sum := sha256.Sum256(b)
	return slices.Contains(shippedWorkspaceStateIgnoreSHA256, hex.EncodeToString(sum[:]))
}

// refreshWorkspaceStateFiles writes the .gitignore when it is absent or an older yolo text, and
// the README whenever its bytes differ from the current rendering. Best-effort, like the
// .gitignore write always was: a launch must not die because it could not describe itself.
func refreshWorkspaceStateFiles(r *os.Root, workspace string) {
	if f, err := r.OpenFile(WorkspaceStateIgnoreName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		_, _ = f.WriteString(WorkspaceStateIgnore)
		_ = f.Close()
	} else if cur, ok := readRegular(r, WorkspaceStateIgnoreName); ok &&
		string(cur) != WorkspaceStateIgnore && isShippedWorkspaceStateIgnore(cur) {
		replaceStateFile(r, WorkspaceStateIgnoreName, WorkspaceStateIgnore)
	}
	readme := WorkspaceStateReadme(workspace)
	if cur, ok := readRegular(r, WorkspaceStateReadmeName); !ok || string(cur) != readme {
		replaceStateFile(r, WorkspaceStateReadmeName, readme)
	}
}

// readRegular reads name when it is a regular file, and reports false for anything else (a
// link the last jail left, a directory, nothing).
func readRegular(r *os.Root, name string) ([]byte, bool) {
	fi, err := r.Lstat(name)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	b, err := r.ReadFile(name)
	return b, err == nil
}

// replaceStateFile writes content to a fresh name beside name and renames it over name, so a
// link at name is replaced as a link (the name is acted on) rather than followed onto whatever
// it points at, and a reader never sees half a file.
func replaceStateFile(r *os.Root, name, content string) {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	tmp := "." + name + "." + strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(nonce[:]) + ".tmp"
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return
	}
	_, werr := f.WriteString(content)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = r.Rename(tmp, name)
	}
	if werr != nil {
		_ = r.Remove(tmp)
	}
}

// WorkspaceStateReadme renders <workspace>/.yolo/README.md for the homes the workspace holds
// now (WorkspaceHomeStates), so it names the layout launches actually bind: the legacy `home/`,
// each setup's `setups/<setup>/home/`, or both while a legacy home awaits its move.
//
// The home rows are rendered from HomeSurfaces, the one list of the install dirs a launch binds
// into the jail home under a different name, plus the directories each home really holds (the
// agents' own, such as `claude/` and `pi/`, which the selected packs decide and this package
// cannot see). Nothing about the homes is hand-written, so a pack that adds a home dir is listed
// without anyone remembering to.
func WorkspaceStateReadme(workspace string) string {
	var rows []string
	for _, home := range WorkspaceHomeStates(workspace) {
		rel, ok := HomeStateRel(workspace, home)
		if !ok {
			continue
		}
		rows = append(rows, readmeHomeRow(filepath.ToSlash(rel), homeDirs(workspace, home)))
	}
	if len(rows) == 0 {
		rows = append(rows, "- `setups/<setup>/home/` IRREPLACEABLE: each setup's agent home, "+
			"made at its first launch and bound into the jail's home directory.")
	}
	return strings.Join([]string{
		"# yolo-jail's state for this project",
		"",
		"yolo rewrites this file at every launch, so an edit to it does not last.",
		"",
		"The agents' homes are in here: every transcript, login and install of every agent that " +
			"ran in this workspace, including any session running now.",
		"",
		"Never move, rename, delete or `git clean` this directory or anything under it, except " +
			"your own files under `durable/`. To start an agent without this state, run " +
			"`yolo --fresh-home`.",
		"",
		"## What is here",
		"",
		strings.Join(rows, "\n"),
		"- `durable/` IRREPLACEABLE: agents' scratch that survives restarts (worktrees, clones, evidence).",
		"- `approvals/`, `approval-waiters/` IRREPLACEABLE: the host's approval records for this project.",
		"- `archive/` IRREPLACEABLE: verbatim copies of your own agent config from before yolo " +
			"managed it, kept for good.",
		"- `nix-roots/`, `receipts.jsonl`, `config-boot.json`: records yolo's cleanup and drift checks read.",
		"- Everything else (`*.log`, `*.lock`, `last-launch*.json` and the like): logs and locks yolo regenerates.",
		"",
	}, "\n")
}

// readmeHomeRow is one home's row: which home it is, the install dirs by both names
// (HomeSurfaces), and every other directory the home holds.
func readmeHomeRow(rel string, dirs []string) string {
	what := "the home every setup of this project shared before each setup got its own; a " +
		"launch binds it until yolo moves it to the setup's own home"
	if strings.HasPrefix(rel, "setups/") {
		what = "setup `" + strings.Split(rel, "/")[1] + "`'s agent home"
	}
	var installs []string
	known := map[string]bool{}
	for _, s := range HomeSurfaces() {
		installs = append(installs, "`"+filepath.ToSlash(s.Subtree)+"/` is `~/"+filepath.ToSlash(s.HomeRel)+"`")
		known[s.Subtree] = true
	}
	row := "- `" + rel + "/` IRREPLACEABLE: " + what + ", bound into the jail's home directory. " +
		"Installs: " + strings.Join(installs, ", ") + "."
	var others []string
	for _, d := range dirs {
		if !known[d] {
			others = append(others, "`"+d+"/`")
		}
	}
	if len(others) > 0 {
		row += " Other dirs: " + strings.Join(others, ", ") + "."
	}
	return row
}

// homeDirs lists the directories directly inside home, sorted. A symbolic link is not a
// directory here and is not listed; the listing reads names only.
func homeDirs(workspace, home string) []string {
	r, err := OpenWorkspaceHomeRoot(workspace, home)
	if err != nil {
		return nil
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return nil
	}
	names, _ := f.Readdirnames(-1)
	_ = f.Close()
	var out []string
	for _, n := range names {
		if fi, err := r.Lstat(n); err == nil && fi.IsDir() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
