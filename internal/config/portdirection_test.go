package config

// The two port keys take their ports in OPPOSITE orders — network.ports is
// "<host>:<jail>" (handed to podman's -p verbatim), network.forward_host_ports is
// "<jail>:<host>". A validation error is the last chance to say so before someone
// debugs the service instead of the entry, so these messages must name the sides.
//
// The word "local" is banned in both: it means the host to a reader standing on
// the host and the jail to a reader standing in the jail, which is the exact
// ambiguity the direction table in `yolo config-ref` exists to remove.

import (
	"strings"
	"testing"
)

func TestPortErrorsNameTheDirection(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		wants []string
	}{
		{
			// A three-field publish-style string borrowed into forward_host_ports.
			name: "forward_host_ports malformed",
			body: `{"network": {"forward_host_ports": ["127.0.0.1:5000:5000"]}}`,
			wants: []string{
				"'<jail>:<host>'",
				"jail side FIRST",
				"the reverse of network.ports",
			},
		},
		{
			// A four-field string, so neither the 2- nor the 3-field shape matches.
			name: "ports malformed",
			body: `{"network": {"ports": ["1:2:3:4"]}}`,
			wants: []string{
				"'<host>:<jail>'",
				"host side FIRST",
				"the reverse of network.forward_host_ports",
			},
		},
		{
			name:  "ports wrong type",
			body:  `{"network": {"ports": [8000]}}`,
			wants: []string{"'<host>:<jail>'"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs, _ := ValidateConfig(decode(t, c.body), t.TempDir(), nil)
			joined := strings.Join(errs, "\n")
			if joined == "" {
				t.Fatalf("no errors reported for %s", c.body)
			}
			for _, want := range c.wants {
				if !strings.Contains(joined, want) {
					t.Errorf("errors missing %q:\n%s", want, joined)
				}
			}
			if strings.Contains(joined, "<local>") || strings.Contains(joined, ".local:") {
				t.Errorf("a port error used the ambiguous word \"local\":\n%s", joined)
			}
		})
	}
}

// TestForwardHostPortErrorPathNamesJailSide: a bad port NUMBER on either side must
// say which side it was, and the jail side must not be called "local".
func TestForwardHostPortErrorPathNamesJailSide(t *testing.T) {
	errs, _ := ValidateConfig(decode(t, `{"network": {"forward_host_ports": ["99999:70000"]}}`), t.TempDir(), nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{".jail:", ".host:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors missing the %q side label:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, ".local") {
		t.Errorf("the jail side must not be labelled \"local\":\n%s", joined)
	}
}
