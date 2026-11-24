package brokerstore

import (
	"fmt"
	"time"
)

type TimedBound struct {
	Maximum    time.Duration
	Cell, Role string
}

type TimedCell struct {
	Name, Kind string
	Maximum    time.Duration
}

// RoleTimedLimit is the sole role-cap calculation. Inputs are the current
// selected declaration and current native policy, never stored authority.
func RoleTimedLimit(q Request, cells []TimedCell, native bool, nativeRead, nativeWrite time.Duration) (TimedBound, error) {
	if err := ValidateOperationBinding(q); err != nil {
		return TimedBound{}, err
	}
	bound := TimedBound{}
	add := func(cell, role string, implicitRead bool) error {
		cap := TimedGrantDefault
		if native {
			switch role {
			case RoleRead:
				if nativeRead > 0 {
					cap = nativeRead
				}
			case RoleWithheldWrite:
				cap = min(TimedGrantDefault, nativeWrite)
			case RoleKnownWrite:
				cap = nativeWrite
			}
		} else {
			found := false
			for _, declared := range cells {
				if declared.Name != cell {
					continue
				}
				kind := "write"
				if role == RoleRead {
					kind = "read"
				}
				if declared.Kind != kind {
					return fmt.Errorf("cell %s is not a declared %s; relaunch and retry", cell, kind)
				}
				if declared.Maximum > 0 {
					cap = declared.Maximum
				}
				found = true
				break
			}
			if !found && !implicitRead {
				return fmt.Errorf("cell %s is not declared by the selected pack; relaunch and retry", cell)
			}
		}
		if cap <= 0 {
			return fmt.Errorf("cell %s role %s has no positive current timed cap; use yolo approve to Allow once", cell, role)
		}
		if bound.Maximum == 0 || cap < bound.Maximum {
			bound = TimedBound{Maximum: cap, Cell: cell, Role: role}
		}
		return nil
	}
	for _, read := range q.CallReads {
		cell := read.Cell
		if cell == "" {
			cell = "read"
		}
		if err := add(cell, RoleRead, read.Cell == ""); err != nil {
			return TimedBound{}, err
		}
	}
	for _, write := range q.CallWrites {
		role := write.Role
		if role == "" {
			role = RoleKnownWrite
		}
		candidates := write.Candidates
		if len(candidates) == 0 {
			candidates = []string{write.Cell}
		}
		for _, cell := range candidates {
			if err := add(cell, role, false); err != nil {
				return TimedBound{}, err
			}
		}
	}
	if bound.Maximum == 0 && native && !q.HasScopedRoles() {
		return TimedBound{Maximum: nativeWrite, Role: RoleKnownWrite}, nil
	}
	if bound.Maximum == 0 {
		return TimedBound{}, fmt.Errorf("request names no timed operation; use yolo approve to Allow once")
	}
	return bound, nil
}

func (s *Store) timedMaximum(q Request, current Policy) (TimedBound, error) {
	if s.TimedLimit != nil && (!s.NativeTimedPolicy || q.HasScopedRoles()) {
		q.Policy.MaxWindow = min(q.Policy.MaxWindow, current.MaxWindow)
		bound, err := s.TimedLimit(q)
		if err != nil {
			return TimedBound{}, err
		}
		if bound.Maximum <= 0 {
			return TimedBound{}, fmt.Errorf("current timed limit is invalid; use yolo approve to Allow once")
		}
		return bound, nil
	}
	return TimedBound{Maximum: min(q.Policy.MaxWindow, current.MaxWindow), Role: RoleKnownWrite}, nil
}

// TimedMaximum rereads authority under the same source lock as answering. A
// frontend uses this current offer; DecideWorking uses the same calculation.
func (s *Store) TimedMaximum(q Request) (TimedBound, error) {
	var out TimedBound
	err := s.transaction(func(r *records, now time.Time) error {
		if q.ID != "" {
			found := false
			for _, stored := range r.requests {
				if stored.ID != q.ID {
					continue
				}
				found = true
				if stored.State != "pending" {
					return refusal(ErrAlreadyDecided, "request %s is already %s", q.ID, stored.State)
				}
				q = stored
				break
			}
			if !found {
				return refusal(ErrUnknownID, "request %q not found", q.ID)
			}
		}
		q = revalidationRequest(q, r, now)
		current, err := currentPolicy(q, s.Revalidate)
		if err != nil {
			return err
		}
		out, err = s.timedMaximum(q, current)
		return err
	})
	return out, err
}
