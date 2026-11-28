package packsrc

// patchbranchtips.go answers "WAS THIS COMMIT ONCE THE REMOTE'S TIP?" for a patch branch given as a local
// checkout (PF-D99, the 2026-11-27 amendment). A local branch that diverged from its remote but was once the
// remote's tip holds nothing unpushed: the remote was rewritten elsewhere. Two read-only witnesses, either one
// enough:
//
//   - the CHECKOUT'S OWN reflog of refs/remotes/<remote>/<ref> (`git reflog show`), every tip the user's
//     fetches saw there. A reflog that is missing or expired is "unknown", never an error;
//   - YOLO'S OWN RECORD of the tips it fetched for this remote and ref, kept in yolo's store (never the user's
//     repository), one file per repository and ref under tips/, appended whenever a verified fetch shows a
//     tip. It answers when the checkout's reflog does not exist, and the launch before a rewrite is what fills it.

import (
	"os"
	"path/filepath"
	"strings"
)

// seenTipsKept bounds yolo's record: the newest tips only, which is every rewrite a launch cadence can miss.
const seenTipsKept = 256

func (s *Store) seenTipsPath(remoteURL, name string) string {
	return filepath.Join(s.Dir, "tips", mirrorSlug(remoteURL), mirrorSlug(name))
}

// readSeenTips is the remote tips yolo recorded for this remote and ref, nil for none.
func (s *Store) readSeenTips(remoteURL, name string) []string {
	data, err := os.ReadFile(s.seenTipsPath(remoteURL, name))
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// recordSeenTip appends tip to yolo's record unless it is already the newest entry. Best-effort, like a stamp:
// a record that cannot be written costs the rewritten-remote answer later, never a launch.
func (s *Store) recordSeenTip(remoteURL, name, tip string) {
	if tip == "" {
		return
	}
	tips := s.readSeenTips(remoteURL, name)
	if n := len(tips); n > 0 && tips[n-1] == tip {
		return
	}
	tips = append(tips, tip)
	if len(tips) > seenTipsKept {
		tips = tips[len(tips)-seenTipsKept:]
	}
	p := s.seenTipsPath(remoteURL, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(tips, "\n")+"\n"), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// wasRemoteTip reports whether commit was once the tip of the remote's branch, by either witness: yolo's record
// for remoteURL, and the reflog of refs/remotes/<remote>/<name> in the repository at dir.
func (s *Store) wasRemoteTip(dir, remote, remoteURL, name, commit string) bool {
	for _, t := range s.readSeenTips(remoteURL, name) {
		if t == commit {
			return true
		}
	}
	out, err := s.run("", "-C", dir, "reflog", "show", "--format=%H", "refs/remotes/"+remote+"/"+name, "--")
	if err != nil {
		return false
	}
	for _, h := range strings.Fields(out) {
		if h == commit {
			return true
		}
	}
	return false
}
