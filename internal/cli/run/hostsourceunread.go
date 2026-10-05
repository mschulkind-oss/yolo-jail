package run

// hostsourceunread.go is the JAIL-LAUNCH LINE for a host file the launch was declared to read and
// did not (docs/design/agent-directory-map.md §4.2's "Jail launch (host side)" row, §6.4 item 5):
// a selected pack's `reads-host` grant or `readsHost` surface (hostFileArgs, buildMacosCtxTree), or
// a briefing's `after: "host:<path>"` file (refreshJailBriefings). The user's own `host_files`
// config key is a different mechanism with its own source probe (config.LoadHostFiles).
//
// THE INCIDENT. A dotfiles manager left ~/.pi/agent/settings.json as a link into a deleted
// ~/.dotfiles/pi. Host apply has refused that destination by name since entrypoint.FindBrokenLink,
// but every jail launch kept composing pi's settings without the user's host layer and said
// nothing, because the source probe (isFile, an os.Stat) answers "not a regular file" alike for a
// file the user never created and a link that points nowhere. The first is the normal state and
// stays silent; this file names everything else.
//
// A LINE, NEVER A REFUSAL: the jail still starts and composes from the surface's other layers, as
// it did before. The source is the user's, so the line names it and, for a link, its target, and
// the remedy is theirs.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostSourceSkip is why a launch did not read a host file it was declared to read.
type hostSourceSkip = entrypoint.HostSourceSkip

// unreadHostSource says why the host file at src (an absolute path) cannot be read, or nil when
// there is nothing to say. The predicate is entrypoint.UnreadHostSource, which `yolo host apply`
// asks of a briefing's `after` file too, so the two notches say the same thing about one file;
// what stays here is the launch's line.
func unreadHostSource(src string) *hostSourceSkip { return entrypoint.UnreadHostSource(src) }

// hostFileSubject is how the line names a pack's host-file grant: the pack and the
// home-relative path it declared, the spelling the disclosure banner and `yolo pack footprint` use.
func hostFileSubject(pack, from string) string {
	return "pack " + pack + "'s host file ~/" + from
}

// prependHostBriefing is jailcontent.PrependHostBriefing with the line: the user's host briefing
// at src (declared as `after: "host:<from>"` by the destination into) is prepended to content when
// it can be read, and named when it is there and cannot be — a dangling link, which the read
// itself cannot tell from absence, is checked first.
func (o *Options) prependHostBriefing(src, from, into, content string) string {
	subject := "the host briefing ~/" + from
	if from != into {
		subject += " for ~/" + into
	}
	const without = "The jail's briefing is composed without it"
	if skip := unreadHostSource(src); skip != nil {
		o.noteUnreadHostSource(subject, without, skip)
		return content
	}
	out, err := jailcontent.PrependHostBriefing(src, content)
	if err != nil {
		o.noteUnreadHostSource(subject, without, &hostSourceSkip{Why: "it could not be read (" + err.Error() + ")"})
	}
	return out
}

// noteUnreadHostSource prints the launch line for one host file the launch did not read, and
// nothing for a nil skip. subject names the file and who declared it; without says what the jail
// does instead.
func (o *Options) noteUnreadHostSource(subject, without string, skip *hostSourceSkip) {
	if skip == nil {
		return
	}
	line := "Warning: " + subject + " was not read: " + skip.Why + ". " + without + "."
	if skip.Remedy != "" {
		line += " " + skip.Remedy + "."
	}
	o.pr(o.Stderr).print("[yellow]" + richtext.Escape(line) + "[/yellow]")
}
