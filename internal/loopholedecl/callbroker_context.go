package loopholedecl

import (
	"fmt"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func parseCallBrokerContext(path string, raw any, scope CallBrokerScope) (*CallBrokerContext, error) {
	m, err := callObject(path, "context", raw, []string{"fields", "unset", "route_anchors"})
	if err != nil {
		return nil, err
	}
	fields, ok := getOrNil(m, "fields").(*jsonx.OrderedMap)
	if !ok || fields.Len() == 0 {
		return nil, Errorf("%s: call_broker.context.fields must declare nonsecret field-to-environment names; repair the declaration", path)
	}
	c := &CallBrokerContext{Fields: map[string]string{}}
	for _, field := range fields.Keys() {
		env, ok := getOrNil(fields, field).(string)
		if field == "" || field == "source" || !ok || !envNameRE.MatchString(env) {
			return nil, Errorf("%s: call_broker.context.fields.%q must name an environment variable; repair the declaration", path, field)
		}
		c.Fields[field] = env
	}
	for _, field := range []struct {
		name string
		dst  *[]string
	}{{"unset", &c.Unset}, {"route_anchors", &c.RouteAnchors}} {
		if _, has := m.Get(field.name); has {
			values, ok := stringSliceField(m, field.name)
			if !ok {
				return nil, Errorf("%s: call_broker.context.%s must be a string list", path, field.name)
			}
			*field.dst = values
		}
	}
	for _, env := range c.Unset {
		if !envNameRE.MatchString(env) {
			return nil, Errorf("%s: call_broker.context.unset entry %q must be an environment variable", path, env)
		}
	}
	if scope.Nesting == "segment" {
		if len(c.RouteAnchors) == 0 {
			return nil, Errorf("%s: segment context requires route_anchors; declare a subset of scope anchors", path)
		}
		seen := map[string]bool{}
		for _, anchor := range c.RouteAnchors {
			if !slices.Contains(scope.Anchors, anchor) || seen[anchor] {
				return nil, Errorf("%s: context route anchor %q must uniquely name a scope anchor; repair the declaration", path, anchor)
			}
			seen[anchor] = true
		}
	} else if _, has := m.Get("route_anchors"); has {
		return nil, Errorf("%s: exact context takes no route_anchors; remove the field", path)
	}
	return c, nil
}

// ScopeMatcher metadata is kept in the declaration hash without adding default fields.
func (s CallBrokerScope) MatchingDescription() string {
	if s.Nesting == "" {
		return ""
	}
	return fmt.Sprintf("scope nesting=%s separator=%q anchors=%q", s.Nesting, s.Separator, s.Anchors)
}
