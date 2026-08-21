package jailcontent

// The briefing's two port sections, which exist because a jail could previously
// see only ONE of the two directions: forward_host_ports was rendered, network.ports
// was not. An agent in here could tell which host ports had been imported and had
// no way to know which of its own ports were published outward — the asymmetry
// that makes "which way is forwarding" a coin flip.
//
// Both sections must name BOTH sides of every mapping, and must not present the
// two keys as if they took their ports in the same order. They do not:
// network.ports is "HOST:JAIL", forward_host_ports is "JAIL:HOST".

import (
	"strings"
	"testing"
)

func TestBriefingPublishedPortsNameHostAndJailSides(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace:    "/w",
		PublishPorts: []any{"8000:3000", 9090, "127.0.0.1:5000:5000", "8080:80/tcp"},
	})

	if !strings.Contains(got, "Published Ports") {
		t.Fatalf("no published-ports section rendered:\n%s", got)
	}
	// "HOST:JAIL" — the jail's 3000 is the host's 8000, never the reverse.
	for _, want := range []string{
		"jail port 3000",
		"localhost:8000` on the host",
		// A bare int is the same port on both sides.
		"jail port 9090",
		// ip:host:jail — the middle field is the host port.
		"jail port 5000",
		// A /tcp suffix must not leak into the jail port.
		"jail port 80",
		"localhost:8080` on the host",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("published ports missing %q; section:\n%s", want, portSection(got, "Published Ports"))
		}
	}
	// The inverted reading must not appear anywhere.
	if strings.Contains(got, "jail port 8000") {
		t.Error("8000 is the HOST port of \"8000:3000\" and must not be shown as the jail's")
	}
}

func TestBriefingForwardedPortsNameHostAndJailSides(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace:        "/w",
		ForwardHostPorts: []any{"5432:3306", 6379},
	})

	// "JAIL:HOST" — the opposite order from PublishPorts above. The jail listens
	// on 5432 and the host answers on 3306.
	for _, want := range []string{
		"localhost:5432` in here",
		"host port 3306",
		"localhost:6379` in here",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("forwarded ports missing %q; section:\n%s", want, portSection(got, "Forwarded Host Ports"))
		}
	}
	if strings.Contains(got, "localhost:3306` in here") {
		t.Error("3306 is the HOST port of \"5432:3306\" and must not be shown as the jail's")
	}
}

// TestBriefingPortSectionsAbsentWhenUnconfigured keeps the briefing quiet for the
// common case: neither key set means neither section.
func TestBriefingPortSectionsAbsentWhenUnconfigured(t *testing.T) {
	got := BriefingContent(BriefingInput{Workspace: "/w"})
	for _, absent := range []string{"Published Ports", "Forwarded Host Ports"} {
		if strings.Contains(got, absent) {
			t.Errorf("%q rendered with no ports configured", absent)
		}
	}
}

// TestBriefingPortSectionsSuppressedInHostMode: with a shared network stack there
// is nothing to map, and both keys are ignored at launch. Saying otherwise in the
// briefing would describe forwarding that is not happening.
func TestBriefingPortSectionsSuppressedInHostMode(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace:        "/w",
		NetMode:          "host",
		PublishPorts:     []any{"8000:3000"},
		ForwardHostPorts: []any{5432},
	})
	for _, absent := range []string{"Published Ports", "Forwarded Host Ports"} {
		if strings.Contains(got, absent) {
			t.Errorf("%q rendered in host networking mode", absent)
		}
	}
}

// portSection returns the briefing lines from a heading to the next blank line,
// for readable failure output.
func portSection(briefing, heading string) string {
	lines := strings.Split(briefing, "\n")
	for i, l := range lines {
		if !strings.Contains(l, heading) {
			continue
		}
		out := []string{l}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				break
			}
			out = append(out, next)
		}
		return strings.Join(out, "\n")
	}
	return "(section not found)"
}
