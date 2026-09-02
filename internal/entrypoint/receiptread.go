package entrypoint

// receiptread.go is the READER half of the receipt schema shims.go WRITES, and it exists
// because until now there was none: `<ws>/.yolo/receipts.jsonl` was appended to by four
// generated installers and consulted by nothing (program-delivery.md §10 step one shipped
// the record; step two is the first thing that reads it).
//
// IT MIRRORS receiptPrefix + _yolo_receipt FIELD FOR FIELD AND ADDS NOTHING. The writer is a
// Go-rendered head (schema/kind/bin/declared — shims.go's receiptPrefix) plus a shell tail
// (spec/resolved/sha256/bytes/path/act/time — _yolo_receipt), and every optional field is
// OMITTED rather than zeroed when the writer could not measure it. A reader that invented a
// default for one of those would be reading a fact nobody wrote: see Resolved's comment for
// the specific forgery the writer refuses to commit and this must not undo.
//
// ONE FILE, TWO IMPLEMENTATIONS IS THE SHAPE THIS REPO KEEPS DELETING (the generated in-jail
// clients, AGENTS.md's "no generated in-jail CLIENT survives"). The defence here is that the
// round-trip test GENERATES a launcher through its production entry point, runs it, and
// parses the file that launcher wrote — so writer and reader cannot drift apart without a
// test going red. A hand-written fixture would pin this file against itself.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// receipt is one parsed line of receipts.jsonl.
//
// Every field but Schema, Kind, Act and Time is optional in the wire format, and the two
// pointer-ish absences are modelled explicitly rather than by a zero value:
//
//   - Resolved is a string whose EMPTY value means "the writer could not tell". shims.go's
//     _resolved_version comment is explicit that `_installed_version`'s `|| echo 0` is a
//     POLL sentinel and that copying it into a receipt would be a FORGERY — a run with no jq
//     on PATH would record "resolved":"0" for every install, and a reader cannot tell that
//     from a package genuinely at version 0. So an absent field is UNKNOWN and is never
//     comparable; HasResolved is the predicate every comparison must consult first.
//   - Bytes is -1 when absent, because 0 is a legitimate size and the writer omits the field
//     exactly when `wc -c` gave it nothing usable.
type receipt struct {
	// Schema is the version receiptPrefix stamps. 1 is the only value ever written.
	Schema int
	// Kind is the RESOLVER: "npm" (both launcher funnels and the pnpm one), "installer",
	// "lsp-npm", "lsp-go", "mcp-npm". The kind implies the landing prefix for the three
	// bootstrap arms, which is why they carry no Path (receiptPrefix says why).
	Kind string
	// Bin is the binary name, omitted by the two npm list arms (lsp-npm, mcp-npm) which
	// install a package whose bin name they do not know.
	Bin string
	// Declared is the DECLARATION verbatim — a pack's `package` string, an installer URL, or
	// an LSP recipe's module path. Not the install spec: the two differ the moment a pack
	// names a version, and telling "the declaration moved" from "the registry moved" is what
	// the pair is for.
	Declared string
	// Spec is what `npm install -g` was actually handed. Absent for the installer kind and
	// for the bootstrap's arms.
	Spec string
	// Resolved is what the resolver reported the install landed as — an npm package.json
	// version, or a `go version -m` mod version. See the type comment: empty means UNKNOWN.
	Resolved string
	// SHA256 is a lowercase 64-char digest of the landing path, written only by the
	// installer kind (§6.3: what the installer LEFT is the only resolved identity there is).
	SHA256 string
	// Bytes is that file's size, or -1 when the field was absent.
	Bytes int64
	// Path is §6's fourth tuple member, the LANDING PATH, carried only by the three launcher
	// funnels — the ones that install ONE program and have $REAL_BIN in hand.
	Path string
	// Act is "install" or "update": whether a human asked for this.
	Act string
	// Time is the writer's 20-char UTC ISO stamp.
	Time string
}

// HasResolved reports whether this receipt states a resolved identity at all.
//
// The one predicate a comparison must consult before comparing anything: an omitted
// `resolved` is the writer saying "I could not measure this", so a reconcile that treated it
// as a value would report a MISMATCH for every install made without jq on PATH — inventing
// drift out of the writer's honesty.
func (r receipt) HasResolved() bool { return r.Resolved != "" }

// receiptKey identifies the program a receipt is about: the (kind, bin) pair.
//
// BIN, not declared: the declaration is what MOVED when a pack repins, so keying on it would
// file the before and after as two different programs and lose the update. And kind is part
// of the key rather than derived from bin, because the same name can legitimately arrive
// through two resolvers (a `program via npm` named `tool` and an LSP go server whose binary
// is also `tool` are different bytes in different directories).
//
// The two npm LIST arms (lsp-npm, mcp-npm) write no bin, so their key's Bin is empty and
// their Declared carries the package — see latestReceipts for how they are distinguished.
type receiptKey struct {
	Kind string
	Bin  string
}

// receiptsPathFor is where the reader looks, and it is receiptsFile's path by construction —
// the writer BAKES that same value into every generated installer, so a second spelling here
// would be the drift this file exists to prevent.
func receiptsPathFor(e *Env) string { return receiptsFile(e) }

// readReceiptLog parses <ws>/.yolo/receipts.jsonl and returns the receipts in file order
// plus, for each line it could not parse, a one-line description naming it.
//
// AN ABSENT FILE IS THE NORMAL STATE and reads as (nil, nil, false): every install site is
// behind a cold `if [ ! -x "$REAL_BIN" ]` branch, so a warm home writes nothing and a jail
// provisioned before the receipts shipped has no file at all (MEASURED 2026-09-02: no
// receipts.jsonl exists anywhere on this machine). The third return distinguishes "no file"
// from "a file with nothing in it", which the caller needs because only one of those is
// worth a note.
//
// AN UNPARSEABLE LINE IS SKIPPED AND NAMED, NEVER FATAL. A jail must not refuse to boot over
// a malformed observation log: the file is appended to by shell from four concurrent
// launchers, it is inside the user's workspace where anything may edit it, and nothing
// downstream of it gates. A parse error is therefore a finding ABOUT the log, reported the
// same way a finding about a package is.
func readReceiptLog(e *Env) (recs []receipt, malformed []string, present bool) {
	path := receiptsPathFor(e)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	for i, line := range splitLines(string(data)) {
		if strings.TrimSpace(line) == "" {
			// A blank line is not a malformed receipt: the writer ends every line with \n
			// and a trailing newline is what splitLines already drops. Anything else blank
			// carries no claim to report.
			continue
		}
		r, err := parseReceiptLine(line)
		if err != nil {
			malformed = append(malformed, fmt.Sprintf("%s line %d: %v", filepath.Base(path), i+1, err))
			continue
		}
		recs = append(recs, r)
	}
	return recs, malformed, true
}

// parseReceiptLine decodes one line into a receipt.
//
// encoding/json, NOT a hand-rolled scan and NOT a shell-out to jq. The writer produced this
// line with encoding/json for the head (so a pack author's quote cannot break it) and with
// constrained shell interpolation for the tail; decoding it with anything but a real JSON
// parser would reintroduce exactly the escaping hazard receiptPrefix exists to close. jq is
// doubly wrong here: the entrypoint is Go, and shelling out would put the macos-user `env -i`
// hazard (_resolved_version's comment: "a run with no jq on PATH") inside the READER too,
// where its failure mode is a boot that reports no drift rather than an omitted field.
func parseReceiptLine(line string) (receipt, error) {
	var raw struct {
		Schema   int    `json:"schema"`
		Kind     string `json:"kind"`
		Bin      string `json:"bin"`
		Declared string `json:"declared"`
		Spec     string `json:"spec"`
		Resolved string `json:"resolved"`
		SHA256   string `json:"sha256"`
		// A POINTER so an omitted field is distinguishable from a written 0. `bytes` is the
		// one numeric field, and 0 is a size a real file can have.
		Bytes *int64 `json:"bytes"`
		Path  string `json:"path"`
		Act   string `json:"act"`
		Time  string `json:"time"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return receipt{}, err
	}
	// A line that parses as JSON but carries no kind is not a receipt — it is some other
	// JSON that ended up in this file. Reporting it as malformed is more useful than
	// carrying an entry no comparison can key on.
	if raw.Kind == "" {
		return receipt{}, fmt.Errorf("no %q field", "kind")
	}
	r := receipt{
		Schema: raw.Schema, Kind: raw.Kind, Bin: raw.Bin, Declared: raw.Declared,
		Spec: raw.Spec, Resolved: raw.Resolved, SHA256: raw.SHA256, Path: raw.Path,
		Act: raw.Act, Time: raw.Time, Bytes: -1,
	}
	if raw.Bytes != nil {
		r.Bytes = *raw.Bytes
	}
	return r, nil
}

// latestReceipts reduces an append-only log to the LAST receipt per program.
//
// The file is append-only by design — one write(2) under O_APPEND is what keeps two
// launchers installing at once from interleaving half-lines — so an update writes a SECOND
// line for a program that already has one, and a reconcile comparing against the first would
// report the version the jail was provisioned with as drift from the version it deliberately
// updated to. Last wins, in FILE ORDER rather than by the `time` field: the stamp has
// one-second resolution and two installs in the same second are ordinary, while the append
// order is the order the installs actually happened in.
//
// The two LIST arms (lsp-npm, mcp-npm) write no `bin`, so their key would collapse every
// package they install onto one entry. They are keyed by DECLARED instead — which for those
// arms is the package name, the only identity they carry.
func latestReceipts(recs []receipt) map[receiptKey]receipt {
	out := make(map[receiptKey]receipt, len(recs))
	for _, r := range recs {
		out[keyOf(r)] = r
	}
	return out
}

// keyOf builds the (kind, bin) key, falling back to the declaration for the arms that write
// no bin. See latestReceipts.
func keyOf(r receipt) receiptKey {
	name := r.Bin
	if name == "" {
		name = r.Declared
	}
	return receiptKey{Kind: r.Kind, Bin: name}
}
