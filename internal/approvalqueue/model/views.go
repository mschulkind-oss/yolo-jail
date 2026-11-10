// Package model holds the queue's in-memory state and the pure reducer that is its only
// writer: (model, input, now) -> model. Nothing here starts a process, reads a clock or
// touches the network.
package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// SchemaSupported is the only schema number of yolo's JSON surface this queue reads.
const SchemaSupported = 1

// The structs below mirror yolo's schema 1 JSON by shape only (the queue does not import
// yolo). Unknown fields are ignored; every object's schema number is checked by Validate.

// RequestView is a request as "yolo approve --json" shows it.
type RequestView struct {
	Schema        int        `json:"schema"`
	ID            string     `json:"id"`
	Source        string     `json:"source"`
	SourceStatus  string     `json:"source_status,omitempty"`
	Jail          string     `json:"jail"`
	Workspace     string     `json:"workspace"`
	Set           string     `json:"set"`
	Argv          []string   `json:"argv"`
	Command       string     `json:"command"`
	Repo          string     `json:"repo"`
	CommandDigest string     `json:"command_digest,omitempty"`
	StdinDigest   string     `json:"stdin_digest,omitempty"`
	Filed         time.Time  `json:"filed"`
	Expires       time.Time  `json:"expires"`
	Decided       *time.Time `json:"decided,omitempty"`
	State         string     `json:"state"`
	HookFailure   string     `json:"hook_failure,omitempty"`
	HookOutput    string     `json:"hook_output,omitempty"`
	NeedsApproval []string   `json:"needs_approval,omitempty"`
	Action        string     `json:"action,omitempty"`
	Target        string     `json:"target,omitempty"`
	Headline      string     `json:"headline"`
	Title         string     `json:"title,omitempty"`
	Refs          []string   `json:"refs,omitempty"`
	Description   string     `json:"description"`
}

// GrantView is a grant as the surface shows it.
type GrantView struct {
	Schema       int        `json:"schema"`
	ID           string     `json:"id"`
	Source       string     `json:"source"`
	SourceStatus string     `json:"source_status,omitempty"`
	Request      string     `json:"request"`
	Jail         string     `json:"jail"`
	Workspace    string     `json:"workspace"`
	UsedBy       string     `json:"used_by,omitempty"`
	Set          string     `json:"set"`
	Coverage     string     `json:"coverage"`
	UsesLeft     int        `json:"uses_left"`
	Expires      time.Time  `json:"expires"`
	Ended        *time.Time `json:"ended,omitempty"`
	EndReason    string     `json:"end_reason,omitempty"`
	State        string     `json:"state"`
}

// WorkingView is a live working-set entry.
type WorkingView struct {
	Schema       int       `json:"schema"`
	ID           string    `json:"id"`
	Source       string    `json:"source"`
	SourceStatus string    `json:"source_status,omitempty"`
	Workspace    string    `json:"workspace"`
	Repo         string    `json:"repo"`
	Kind         string    `json:"kind"`
	Resource     string    `json:"resource"`
	By           string    `json:"by"`
	Added        time.Time `json:"added,omitzero"`
	Expires      time.Time `json:"expires,omitzero"`
	File         string    `json:"file,omitempty"`
}

// SourceView labels each listed source.
type SourceView struct {
	Source         string `json:"source"`
	Status         string `json:"status"`
	Authority      string `json:"authority"`
	SelectionBasis string `json:"selection_basis"`
}

// StateView is one snapshot: the output of "yolo approve --json", or the "state" line that
// opens a watch.
type StateView struct {
	Schema      int           `json:"schema"`
	Event       string        `json:"event,omitempty"`
	Sources     []SourceView  `json:"sources,omitempty"`
	Diagnostics []string      `json:"diagnostics,omitempty"`
	Requests    []RequestView `json:"requests"`
	Grants      []GrantView   `json:"grants"`
	Working     []WorkingView `json:"working"`
}

// EventView is one watch event.
type EventView struct {
	Schema  int          `json:"schema"`
	Event   string       `json:"event"`
	Source  string       `json:"source"`
	Time    time.Time    `json:"time"`
	Summary string       `json:"summary"`
	Request *RequestView `json:"request,omitempty"`
	Grant   *GrantView   `json:"grant,omitempty"`
}

// The watch event names.
const (
	EvFiled   = "request-filed"
	EvDecided = "request-decided"
	EvExpired = "request-expired"
	EvUsed    = "grant-used"
	EvEnded   = "grant-ended"
	EvRevoked = "grant-revoked"
	EvState   = "state"
)

// SchemaError is returned for an object whose schema is not SchemaSupported.
type SchemaError struct{ Seen int }

func (e *SchemaError) Error() string {
	return fmt.Sprintf("yolo printed schema %d and this queue reads schema %d only; update the queue", e.Seen, SchemaSupported)
}

// Validate refuses any schema number but 1 on the snapshot and every object inside it.
func (s *StateView) Validate() error {
	if s.Schema != SchemaSupported {
		return &SchemaError{s.Schema}
	}
	for i := range s.Requests {
		if s.Requests[i].Schema != SchemaSupported {
			return &SchemaError{s.Requests[i].Schema}
		}
	}
	for i := range s.Grants {
		if s.Grants[i].Schema != SchemaSupported {
			return &SchemaError{s.Grants[i].Schema}
		}
	}
	for i := range s.Working {
		if s.Working[i].Schema != SchemaSupported {
			return &SchemaError{s.Working[i].Schema}
		}
	}
	return nil
}

// Validate refuses any schema number but 1 on the event and the objects it carries.
func (e *EventView) Validate() error {
	if e.Schema != SchemaSupported {
		return &SchemaError{e.Schema}
	}
	if e.Request != nil && e.Request.Schema != SchemaSupported {
		return &SchemaError{e.Request.Schema}
	}
	if e.Grant != nil && e.Grant.Schema != SchemaSupported {
		return &SchemaError{e.Grant.Schema}
	}
	return nil
}

// ParseState decodes one snapshot tolerantly and validates its schema.
func ParseState(b []byte) (StateView, error) {
	var s StateView
	if err := json.Unmarshal(b, &s); err != nil {
		return StateView{}, err
	}
	return s, s.Validate()
}

// ParseEvent decodes one watch line that is not a state line.
func ParseEvent(b []byte) (EventView, error) {
	var e EventView
	if err := json.Unmarshal(b, &e); err != nil {
		return EventView{}, err
	}
	return e, e.Validate()
}
