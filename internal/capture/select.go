package capture

import "time"

// select.go is THE SELECTION RULE for the capture store: given a store, which entry answers
// "<bin> for <platform>?".
//
// It is one function with two callers, and the second is the first one's COMPLEMENT:
//
//   - `yolo internal capture-materialize` (internal/cli/capturematerialize.go) asks it for one
//     program and materializes the entry it names. It is the ONLY code that reads the store.
//   - PruneSupersededCaptures (gc.go) asks it for every program and reaps each entry it did
//     NOT name.
//
// The rule therefore lives HERE rather than at either caller. GC is derived from the reader
// instead of agreeing with it, which is what makes the two unable to drift
// (program-delivery.md OQ-PD17): a change to newest-wins moves the reap set in the same
// commit, and a reap rule that kept an entry the resolver would return is unrepresentable.
//
// # There is no index, and the receipts are the only attribution there is
//
// The store is content-addressed, so no key is computable from a program name; `yolo capture`
// puts the (bin, platform, key) triple in each entry's own `receipts.jsonl` and the answer is
// found by SCANNING. That was slice 4's decision and it stands for three reasons, all of which
// this file's second caller sharpens rather than weakens:
//
//   - COST. The question is asked from a launcher's cold `if [ ! -x "$REAL_BIN" ]` branch —
//     once per program per workspace, never on a warm launch — and the work is one ReadDir
//     plus one small file read per entry. An index would save microseconds on a path that runs
//     a handful of times in a workspace's life, and prune runs even less often than that.
//   - CORRECTNESS. An index is a second record that admit AND the reap would both have to keep
//     true, and a stale one is a WRONG ANSWER — a key that no longer exists, or an entry reaped
//     out from under it. The receipts live INSIDE the entry they describe, so they cannot go
//     stale relative to it: deleting the entry deletes the claim.
//   - SCHEMA. install-capture.md's Blockers say to stop and ask before adding per-entry
//     metadata a later yolo must parse. A scan adds none.

// Program is what a capture is selected BY: the binary it installs and the platform it was
// captured for. The pair exists nowhere in the content address — only in the entry's own
// `record` receipt — which is why selection has to read them.
type Program struct {
	// Bin is the binary name the captured installer installs.
	Bin string
	// Platform is "<GOOS>/<GOARCH>" AS THE CAPTURE OBSERVED IT (capture.Platform() inside
	// the capture jail), not the host's.
	Platform string
}

// String renders a program the way both callers print it: "claude (linux/amd64)".
func (p Program) String() string { return p.Bin + " (" + p.Platform + ")" }

// Record is one `record` receipt AS SELECTION READS IT — the three fields the choice needs
// (bin, platform, time) plus the digest, which it never looks at and carries so the caller that
// writes a materialize receipt does not read the file a second time.
//
// IT IS A VIEW, NOT A SECOND SCHEMA. The receipt schema has one writer and one reader in this
// repo (internal/entrypoint), and a capture receipt parsed by a second parser would be the
// parallel ledger program-delivery.md §6 warns about — so this store does not parse anything:
// it is HANDED records by a Records func, and the one adapter that fills this struct out of
// entrypoint.CaptureReceipt lives at the CLI boundary where both callers reach it.
//
// The dependency direction is the reason for the seam. internal/entrypoint is the jail
// provisioner and sits above this store; it pulls internal/config, whose package init
// materializes the embedded pack tree. Importing it here would put that init behind every
// consumer of a content-addressed directory — including internal/prune, which deliberately
// does not import internal/config.
type Record struct {
	// Bin and Platform are the program this entry was captured for.
	Bin, Platform string
	// Time is the receipt's stamp — one-second resolution, which is why the selection has a
	// tie-break at all. A receipt whose stamp did not parse arrives here as the zero time,
	// the safe end of the ordering ("I cannot tell" sorts oldest).
	Time time.Time
	// Digest is the full sha256 of the canonical file manifest, carried for the caller. The
	// selection never looks at it.
	Digest string
}

// Records reads the `record` receipts beside ONE entry, given the entry's directory.
//
// The store's only inbound dependency on the receipt schema, and it is a function rather than
// an import for the reason Record's doc gives. An absent or unreadable log is not an error:
// return no records and the entry is attributed to no program, which is a miss for the
// resolver and reapable for the GC — the same answer, from the same rule.
type Records func(entryDir string) ([]Record, error)

// Selected is the entry Select chose for one Program.
type Selected struct {
	// Key is the ENTRY key — the directory name under entries/, which is what Store.Resolve
	// takes. Deliberately NOT the receipt's own `resolved` field: the two say the same thing
	// today, and if they ever disagreed the directory is the one that exists.
	Key string
	// Record is the winning record.
	Record Record
}

// Select returns, for every program the store's entries record, the ONE entry the materialize
// path will use.
//
// NEWEST WINS, by receipt time, with the greater key breaking a tie — the stamp has one-second
// resolution, so two captures in one second are possible and an arbitrary answer would make a
// re-capture's effect depend on directory order.
//
// Three kinds of entry are NOT candidates, and each is a miss rather than an error:
//
//   - a TORN or in-flight one, because the candidate list is Store.EntryKeys, which reads the
//     completion marker exactly as Store.Resolve does;
//   - one whose receipt log is missing or unreadable, which is in the store but attributed to
//     no program at all;
//   - a record naming no bin or no platform, because a query never carries an empty one
//     (`capture-materialize` refuses an empty --bin and capture.Platform() is never empty), so
//     such a line could not be matched by any caller.
//
// An unreadable store is the one real error: an empty store reads as an empty map (EntryKeys
// says why), so a nil error with no entries is the normal state of a machine that has never
// captured anything.
func Select(s *Store, read Records) (map[Program]Selected, error) {
	scan, err := scanRecords(s, read)
	if err != nil {
		return nil, err
	}
	return selectFrom(scan), nil
}

// entryRecords is one complete entry and the records beside it that name a program. It is what
// a store scan yields: gc.go reaps from the SAME scan Select chooses from, so the losers it
// reports are the ones this selection actually rejected.
type entryRecords struct {
	// Key is the entry's directory name under entries/.
	Key string
	// Records are its candidate records in file order. Empty for an entry nothing attributes
	// to a program, which is a real state (an admit whose receipt write failed) and a
	// reapable one.
	Records []Record
}

// scanRecords reads every complete entry's candidate records, in key order.
func scanRecords(s *Store, read Records) ([]entryRecords, error) {
	keys, err := s.EntryKeys()
	if err != nil {
		return nil, err
	}
	out := make([]entryRecords, 0, len(keys))
	for _, key := range keys {
		er := entryRecords{Key: key}
		recs, rerr := read(s.EntryDir(key))
		if rerr != nil {
			// Unreadable is the same answer as absent: nothing attributes this entry to
			// a program. Not an error — see the Select doc.
			out = append(out, er)
			continue
		}
		for _, r := range recs {
			if r.Bin == "" || r.Platform == "" {
				continue
			}
			er.Records = append(er.Records, r)
		}
		out = append(out, er)
	}
	return out, nil
}

// selectFrom is the choice itself, over an already-read scan — pure, so the newest-wins rule
// and its tie-break are testable without a store on disk.
func selectFrom(scan []entryRecords) map[Program]Selected {
	out := map[Program]Selected{}
	for _, er := range scan {
		for _, r := range er.Records {
			p := Program{Bin: r.Bin, Platform: r.Platform}
			cur, seen := out[p]
			if !seen || r.Time.After(cur.Record.Time) ||
				(r.Time.Equal(cur.Record.Time) && er.Key > cur.Key) {
				out[p] = Selected{Key: er.Key, Record: r}
			}
		}
	}
	return out
}
