package loopholegrant

import "testing"

// Each want below is docs/reference/loophole-grants.md §5's block, verbatim, with only the
// noun, the kind and <dir> filled.

func TestRefusalTextRequestable(t *testing.T) {
	got := Refusal{Client: "yolo-journalctl", Pack: "journal", Access: "read the host's journal",
		Kind: "read", Standing: "requestable"}.Text()
	want := "yolo-journalctl: this project holds no grant to read the host's journal, so nothing ran.\n" +
		"  The journal pack is selected and its service is running; it is waiting on a grant.\n" +
		"  Ask for this project:   yolo request journal read\n" +
		"  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRefusalTextNotAllowed(t *testing.T) {
	got := Refusal{Client: "yolo-ps", Pack: "host-processes", Access: "see host processes", Kind: "view",
		Values: []string{"postgres", "redis-server"}, Standing: NotAllowed}.Text()
	want := "yolo-ps: this project holds no grant to see host processes, so nothing ran.\n" +
		"  Your user config does not allow host-processes view on request, so a request would be skipped.\n" +
		"  The human allows it, directly in a host terminal (not through `yolo host-run`):\n" +
		"    yolo allow host-processes on_request view\n" +
		"  then this project asks:             yolo request host-processes view postgres redis-server\n" +
		"  then the human runs `yolo config apply` on the host, in this workspace, and answers y.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A valued kind refused with no value in hand prints its value noun as the placeholder, so the
// request line still names what to fill in.
func TestRefusalTextNotAllowedValuedWithoutValuesPrintsTheNoun(t *testing.T) {
	got := Refusal{Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		ValueNoun: "device globs", Standing: NotAllowed}.Text()
	want := "yolo-serial: this project holds no grant to open host serial devices, so nothing ran.\n" +
		"  Your user config does not allow serial devices on request, so a request would be skipped.\n" +
		"  The human allows it, directly in a host terminal (not through `yolo host-run`):\n" +
		"    yolo allow serial on_request devices\n" +
		"  then this project asks:             yolo request serial devices <device globs>\n" +
		"  then the human runs `yolo config apply` on the host, in this workspace, and answers y.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An absent or unknown standing is a grant file that says nothing, which fails closed with the
// not_allowed text (plan PD-12).
func TestRefusalTextWithoutAStandingIsNotAllowed(t *testing.T) {
	r := Refusal{Client: "yolo-ps", Pack: "host-processes", Access: "see host processes", Kind: "view",
		Values: []string{"postgres"}}
	n := r
	n.Standing = NotAllowed
	if r.Text() != n.Text() {
		t.Fatalf("no standing:\n%s\nnot_allowed:\n%s", r.Text(), n.Text())
	}
}

// A granted valued kind that does not cover the value a call named asks for that value.
func TestRefusalTextGrantedButNotCovered(t *testing.T) {
	got := Refusal{Client: "yolo-ps", Pack: "host-processes", Access: "see host processes", Kind: "view",
		Values: []string{"nginx"}, Standing: Granted}.Text()
	want := "yolo-ps: this project's grant to see host processes does not cover nginx, so nothing ran.\n" +
		"  Ask for this project:   yolo request host-processes view nginx\n" +
		"  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRefusalTextPending(t *testing.T) {
	got := Refusal{Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		Values: []string{"/dev/ttyUSB*"}, Standing: Pending}.Text()
	want := "yolo-serial: this project's request for devices /dev/ttyUSB* is not approved yet, so nothing ran.\n" +
		"  The human runs, on the host, in this workspace:  yolo config apply   and answers y.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRefusalTextInert(t *testing.T) {
	got := Refusal{Client: "yolo-journalctl", Pack: "journal", Access: "read the host's journal", Kind: "read",
		Standing: Requestable, Inert: "macOS has no systemd journal"}.Text()
	want := "yolo-journalctl: the journal loophole does nothing on this setup (macOS has no systemd journal); " +
		"nothing is missing to install.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
