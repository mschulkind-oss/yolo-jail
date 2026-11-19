package mcpsignin

// LaunchVersion is independent of the token-store and share versions.
const LaunchVersion = 1

const WireEnv = "YOLO_MCP_SIGN_IN"

// Launch carries token-free, already-delivered facts. It grants no MCP authority.
type Launch struct {
	Version            int            `json:"version"`
	Advice             *AdviceContext `json:"advice,omitempty"`
	Bundles            []Bundle       `json:"bundles,omitempty"`
	Decisions          []Decision     `json:"decisions,omitempty"`
	CallbackGeneration string         `json:"callback_generation,omitempty"`
	Callbacks          []Callback     `json:"callbacks,omitempty"`
}

type Bundle struct {
	Pack          string   `json:"pack"`
	Agent         string   `json:"agent"`
	Format        string   `json:"format"`
	Origin        string   `json:"origin"`
	Revision      string   `json:"revision"`
	ManifestName  string   `json:"manifest_name,omitempty"`
	Placement     string   `json:"placement"`
	Reason        string   `json:"reason,omitempty"`
	CallbackRoute bool     `json:"callback_route,omitempty"`
	Servers       []Server `json:"servers,omitempty"`
}

type Once struct {
	Name             string
	URL              string
	Transport        string
	ExpectedClientID string
}

type Agent struct {
	Name       string
	Formats    []string
	Capturable bool
	HeaderForm string
	Paste      bool
	NativeName func(deliveredName, serverKey string) (string, error)
}

type Point struct {
	Pack          string `json:"pack"`
	NativeName    string `json:"native_name"`
	Origin        string `json:"origin"`
	Revision      string `json:"revision"`
	ClientID      string `json:"client_id,omitempty"`
	CallbackPort  int    `json:"callback_port,omitempty"`
	CallbackRoute bool   `json:"callback_route,omitempty"`
}

type Decision struct {
	Server       string  `json:"server"`
	Agent        string  `json:"agent"`
	Capturable   bool    `json:"capturable"`
	UsableRecord bool    `json:"usable_record,omitempty"`
	HeaderForm   string  `json:"header_form,omitempty"`
	Paste        bool    `json:"paste,omitempty"`
	Point        *Point  `json:"point,omitempty"`
	Alternatives []Point `json:"alternatives,omitempty"`
	Reason       string  `json:"reason,omitempty"`
}

// Resolve preserves composition order. It cannot read configuration, credentials
// or filesystem content, and never derives a name from a source directory.
func Resolve(servers []Once, bundles []Bundle, agents []Agent) []Decision {
	var decisions []Decision
	for _, server := range servers {
		transport := server.Transport
		if transport == "" {
			transport = "http"
		}
		for _, agent := range agents {
			decision := Decision{Server: server.Name, Agent: agent.Name, Capturable: agent.Capturable, HeaderForm: agent.HeaderForm, Paste: agent.Paste, Reason: "no sign-in point"}
			for _, bundle := range bundles {
				supported := false
				for _, format := range agent.Formats {
					if format == bundle.Format {
						supported = true
					}
				}
				if !supported || bundle.Agent != agent.Name {
					continue
				}
				for _, entry := range bundle.Servers {
					if entry.URL != server.URL || entry.Transport != transport {
						continue
					}
					if bundle.Placement != "placed" {
						decision.Reason = "not delivered"
						continue
					}
					if entry.Reason != "" {
						decision.Reason = entry.Reason
						continue
					}
					if server.ExpectedClientID != "" && entry.ClientID != server.ExpectedClientID {
						decision.Reason = "client mismatch"
						continue
					}
					if agent.NativeName == nil {
						decision.Reason = "native name unavailable"
						continue
					}
					name, err := agent.NativeName(bundle.ManifestName, entry.Key)
					if err != nil || name == "" {
						decision.Reason = "native name unavailable"
						continue
					}
					point := Point{Pack: bundle.Pack, NativeName: name, Origin: bundle.Origin, Revision: bundle.Revision, ClientID: entry.ClientID, CallbackPort: entry.CallbackPort, CallbackRoute: bundle.CallbackRoute}
					if decision.Point == nil {
						decision.Point = &point
					} else {
						decision.Alternatives = append(decision.Alternatives, point)
					}
				}
			}
			if decision.Point != nil {
				decision.Reason = ""
			}
			decisions = append(decisions, decision)
		}
	}
	return decisions
}
