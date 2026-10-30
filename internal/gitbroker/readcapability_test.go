package gitbroker

import (
	"maps"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ReadCapability is the shipped manifest's `brokered.read_capability` for this source, so a
// refusal for want of a read grant names the `yolo allow` a TOML user can actually run.
func TestReadCapabilityIsTheManifests(t *testing.T) {
	for _, pack := range packload.Embedded() {
		modules, _, _, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			continue
		}
		for _, m := range modules {
			if m.Decl != nil && m.Decl.Brokered != nil && m.Decl.Brokered.Source == Source {
				if got := m.Decl.Brokered.ReadCapability; got != ReadCapability {
					t.Fatalf("pack %s declares read_capability %q for source %q; ReadCapability is %q", pack.Name, got, Source, ReadCapability)
				}
				return
			}
		}
	}
	t.Fatalf("no shipped pack declares a broker for source %q", Source)
}

// KindCapabilities is the shipped manifest's `brokered.kind_capabilities` for this source, so a
// push hint's `yolo allow` and `yolo request` name capabilities the verbs accept.
func TestKindCapabilitiesAreTheManifests(t *testing.T) {
	for _, pack := range packload.Embedded() {
		modules, _, _, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			continue
		}
		for _, m := range modules {
			if m.Decl != nil && m.Decl.Brokered != nil && m.Decl.Brokered.Source == Source {
				if got := m.Decl.Brokered.KindCapabilities; !maps.Equal(got, KindCapabilities) {
					t.Fatalf("pack %s declares kind_capabilities %v for source %q; KindCapabilities is %v",
						pack.Name, got, Source, KindCapabilities)
				}
				return
			}
		}
	}
	t.Fatalf("no shipped pack declares a broker for source %q", Source)
}
