package journald

// settings.go names the two modes a bridge request runs in. Neither is a config value any
// more: both bridges' manifests declare `[[grants]]` and no setting, and grant.go maps the
// grant file's standings onto a mode at each request (plan PD-11).
//
// # What it replaced
//
// The mode used to arrive as `--mode user|full`, from a TOP-LEVEL `journal` config key that a
// workspace file, one the agent inside the jail can rewrite, could set to `full`. It became a
// user-scope `full` setting (OQ-K4), and is now the `read_all` grant kind, which only the
// user's ceiling can make requestable and only an accepted gate can grant
// (docs/design/loophole-access-approval.md §4.3).

// Bridge modes — what a request's args are allowed to reach. These are the strings
// ParseRequest and PlanMacosLog take; grant.go's decide is the only producer.
const (
	// ModeUser is the `read` grant: journalctl gets `--user` forced onto every invocation,
	// and `log` keeps `show` and `stream` with output narrowed to the sandbox account. It is
	// the SAFE end.
	ModeUser = "user"
	// ModeFull is the `read_all` grant: the client's args pass through unchanged, which needs
	// host read access to the journal or the unified log.
	ModeFull = "full"
)
