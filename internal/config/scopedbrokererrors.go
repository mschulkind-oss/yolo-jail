package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// scopedBrokerConflict retains both authored paths for positioned public errors.
// File-only origins remain the policy input's existing provenance contract.
type scopedBrokerConflict struct {
	paths   []string
	message string
	origins map[string][]string
}

func (e *scopedBrokerConflict) Error() string {
	lines := make([]string, len(e.paths))
	for i, path := range e.paths {
		lines[i] = positionedPolicyLine(path, e.origins)
	}
	return termsafe.Visible(strings.Join(lines, " and ") + " " + e.message)
}

func annotateScopedBrokerConflict(err error, sources ...*Sources) error {
	var conflict *scopedBrokerConflict
	if !errors.As(err, &conflict) {
		return err
	}
	var locations []string
	for _, path := range conflict.paths {
		for _, source := range sources {
			if source == nil {
				continue
			}
			for _, location := range source.Locations("config." + path) {
				if !slices.Contains(locations, location) {
					locations = append(locations, location)
				}
			}
		}
	}
	if len(locations) == 0 {
		return err
	}
	return fmt.Errorf("%s (written at %s)", err, termsafe.Visible(strings.Join(locations, ", ")))
}
