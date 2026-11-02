// Package loopholegrant is the grant file of a grant-declaring loophole
// (docs/design/loophole-access-approval.md §4.6; plan PD-2, PD-3): the per-launch private
// settings snapshot the launch already writes in the loophole's state dir, with one reserved
// key, `grants`, carrying the standing of every kind the manifest declares. The launch (or
// `yolo config apply`) is its one writer; the daemon reads only it, never the config. Nothing
// but this package parses the `grants` key.
//
// It also renders the refusal a mediated client prints (refusal.go), so every client says
// §5's text and no client spells its own.
package loopholegrant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"syscall"
)

// ReservedKey is the settings-object key the grant block lives under. A loophole may not
// declare a setting of that name (loopholedecl refuses it), so the two never collide.
const ReservedKey = "grants"

// fileCap bounds a grant file read: settings and a handful of kinds are a few kilobytes.
const fileCap = 1 << 20

// Standing is where one grant kind stands for one project (design §4.6).
type Standing string

const (
	// Granted: the gate approved it; Values carries the approved value of a valued kind.
	Granted Standing = "granted"
	// Requestable: in the ceiling's on_request, not requested by the project.
	Requestable Standing = "requestable"
	// Pending: requested, in the ceiling, not yet approved at a gate.
	Pending Standing = "pending"
	// NotAllowed: outside the user's ceiling.
	NotAllowed Standing = "not_allowed"
)

// Valid reports whether s is one of the four standings.
func (s Standing) Valid() bool {
	switch s {
	case Granted, Requestable, Pending, NotAllowed:
		return true
	}
	return false
}

// Kind is one declared grant kind's standing for this launch.
type Kind struct {
	Standing Standing `json:"standing"`
	// Values is a valued kind's value: the approved one when Granted, the requested one when
	// Pending. Empty for an unvalued kind.
	Values []string `json:"values,omitempty"`
	// Text is the manifest's line for the kind ("read your user journal").
	Text string `json:"text,omitempty"`
	// ValueNoun is a valued kind's noun as the manifest declares it ("process names"), for a
	// refusal's placeholder. Empty for an unvalued kind, and absent from a file an older launch
	// wrote, where a daemon falls back to its own spelling.
	ValueNoun string `json:"value_noun,omitempty"`
}

// Block is the reserved `grants` key: the launch's identity and one Kind per declared kind.
type Block struct {
	Pack      string `json:"pack"`
	Workspace string `json:"workspace"` // the host path of the workspace
	Container string `json:"container"`
	LaunchID  string `json:"launch_id"`
	// PID is the launching yolo process; LiveFiles keeps a file only while it is alive.
	PID int `json:"pid"`
	// Access is the manifest's `access` line ("read the host's journal"), which every refusal
	// prints. Absent from a file an older launch wrote, where a daemon falls back to its own
	// spelling.
	Access string          `json:"access,omitempty"`
	Kinds  map[string]Kind `json:"kinds"`
}

// check is the shape every Block written or read must have.
func (b Block) check() error {
	if b.Pack == "" {
		return errors.New("grant file: the grant block names no pack")
	}
	names := make([]string, 0, len(b.Kinds))
	for k := range b.Kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if k == "" {
			return errors.New("grant file: a grant kind has no name")
		}
		if s := b.Kinds[k].Standing; !s.Valid() {
			return fmt.Errorf("grant file: kind %q has standing %q, which is none of granted, requestable, pending, not_allowed", k, s)
		}
	}
	return nil
}

func (b Block) sameIdentity(o Block) bool {
	return b.Pack == o.Pack && b.Workspace == o.Workspace && b.Container == o.Container &&
		b.LaunchID == o.LaunchID && b.PID == o.PID
}

// Merge returns the grant file's bytes: settings, the frozen settings object the launch
// resolved (nil or empty for a loophole with no settings), with the grant block added under
// ReservedKey. The settings bytes are kept as they are, so the daemon reads the settings it
// was validated with; the result is newline-terminated.
func Merge(settings []byte, b Block) ([]byte, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	body, existing, err := split(settings)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("grant file: the settings already hold the reserved key %q", ReservedKey)
	}
	return join(body, b)
}

// Read reads a grant file: the settings, without the reserved key, and the grant block, nil
// when the file has no `grants` key (a loophole that declares no grants, or a file an older
// build wrote). It refuses a symlink, a non-regular file and a malformed block.
func Read(path string) (map[string]any, *Block, error) {
	data, err := readCapped(path, fileCap)
	if err != nil {
		return nil, nil, fmt.Errorf("grant file %s: %w", path, err)
	}
	body, raw, err := split(data)
	if err != nil {
		return nil, nil, fmt.Errorf("grant file %s: %w", path, err)
	}
	settings := map[string]any{}
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, nil, fmt.Errorf("grant file %s: %w", path, err)
	}
	if raw == nil {
		return settings, nil, nil
	}
	var b Block
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, nil, fmt.Errorf("grant file %s: %w", path, err)
	}
	if err := b.check(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return settings, &b, nil
}

// member is one top-level key of the settings object, its value kept as written.
type member struct {
	key string
	raw json.RawMessage
}

// split reads a JSON object and returns it without the reserved key, members in their
// written order and values verbatim, plus the reserved key's raw value (nil when absent).
// Empty input is the empty object.
func split(data []byte) ([]byte, json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("{}"), nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, errors.New("the settings are not a JSON object")
	}
	var kept []member
	var reserved json.RawMessage
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, err
		}
		if key == ReservedKey {
			if reserved != nil {
				return nil, nil, fmt.Errorf("the reserved key %q appears twice", ReservedKey)
			}
			reserved = raw
			continue
		}
		kept = append(kept, member{key, raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("trailing data after the settings object")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, m := range kept {
		if i > 0 {
			out.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		out.Write(k)
		out.WriteByte(':')
		out.Write(m.raw)
	}
	out.WriteByte('}')
	return out.Bytes(), reserved, nil
}

// join adds b under the reserved key to body, an object split returned.
func join(body []byte, b Block) ([]byte, error) {
	block, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(body[:len(body)-1])
	if len(body) > 2 {
		out.WriteByte(',')
	}
	out.WriteString(`"` + ReservedKey + `":`)
	out.Write(block)
	out.WriteString("}\n")
	return out.Bytes(), nil
}

var errNotRegular = errors.New("not a regular file")

// readCapped reads a regular file of at most limit bytes, deciding on the file it opened:
// O_NOFOLLOW refuses a symlink, O_NONBLOCK keeps a FIFO from hanging the read, and the fstat
// is of what it holds (brokerscope's readCapped, for the same reason).
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, errNotRegular
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("too large")
	}
	return data, nil
}
