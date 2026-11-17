package s3fixture

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
)

// CEArgv returns the controlled fake's canonical invocations, separately from S3
// request count. No signing headers, credentials or environment are retained.
func (s *Server) CEArgv() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.ceArgv))
	for i, a := range s.ceArgv {
		out[i] = slices.Clone(a)
	}
	return out
}

func (s *Server) serveCE(w http.ResponseWriter, r *http.Request) {
	var args []string
	d := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	if r.Method != http.MethodPost || d.Decode(&args) != nil || d.Decode(new(any)) != io.EOF || parseCE(args) != nil {
		http.Error(w, "unsupported fixture CE request", http.StatusBadRequest)
		s.record(r, false, http.StatusBadRequest, false)
		return
	}
	s.mu.Lock()
	s.ceArgv = append(s.ceArgv, slices.Clone(args))
	s.mu.Unlock()
	options := map[string]string{}
	for i := 2; i < len(args)-1; i++ {
		if len(args[i]) > 2 && args[i][:2] == "--" {
			options[args[i]] = args[i+1]
		}
	}
	status := http.StatusOK
	out := ""
	if options["--search-string"] == "fixture-error" {
		status = http.StatusBadRequest
		out = "synthetic CE service error; retry a supported query on the host\n"
	} else if options["--search-string"] == "fixture-redaction" {
		out = "{\"Tags\":[\"" + SecretAccessKey + "\",\"" + AccessKeyID + "\"]}\n"
	} else {
		switch args[1] {
		case "get-tags":
			out = "{\"Tags\":[\"fixture\"]}\n"
			if options["--next-page-token"] == "fixture-next" {
				out = "{\"Tags\":[\"second-page\"]}\n"
			}
			if options["--search-string"] == "fixture-pages" {
				out = "{\"Tags\":[\"first-page\"],\"NextPageToken\":\"fixture-next\"}\n"
			}
		case "get-cost-and-usage":
			out = "{\"ResultsByTime\":[{\"Total\":{\"UnblendedCost\":{\"Amount\":\"12.34\",\"Unit\":\"USD\"}}}]}\n"
		case "get-cost-forecast":
			out = "{\"Total\":{\"Amount\":\"23.45\",\"Unit\":\"USD\"}}\n"
		case "get-dimension-values":
			out = "{\"DimensionValues\":[{\"Value\":\"Synthetic Service\"}]}\n"
		}
	}
	s.record(r, true, status, false)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, out)
}
