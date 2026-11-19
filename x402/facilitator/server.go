package x402facilitator

import (
	"encoding/json"
	"net/http"
	"sort"
)

const maxBodyBytes = 1 << 20 // 1 MiB; PQ-signed txs are a few KB

// Server exposes the facilitator HTTP API: GET /supported, POST /verify,
// POST /settle. Put it behind TLS and authentication of your choice; it holds
// no keys itself (the Broadcaster adapter does).
type Server struct {
	Verifier *Verifier
	Settler  *Settler
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/supported", s.handleSupported)
	mux.HandleFunc("/verify", s.handleVerify)
	mux.HandleFunc("/settle", s.handleSettle)
	return mux
}

func (s *Server) handleSupported(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.Verifier.Policy
	types := make([]string, 0, len(p.AllowedKeyTypes))
	for k, ok := range p.AllowedKeyTypes {
		if ok {
			types = append(types, k)
		}
	}
	sort.Strings(types)
	feeMode := "payer"
	if p.SponsorAddr != "" {
		feeMode = "sponsored"
	}
	writeJSON(w, http.StatusOK, SupportedResponse{Kinds: []SupportedKind{{
		X402Version: 2,
		Scheme:      SchemeExact,
		Network:     p.Network,
		Extra: RequirementsExtra{
			Flow:       FlowSend,
			KeyTypes:   types,
			Transports: []string{TransportInline},
			FeeMode:    feeMode,
		},
	}}})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.Verifier.Verify(r.Context(), req))
}

func (s *Server) handleSettle(w http.ResponseWriter, r *http.Request) {
	var req SettleRequest
	if !decodeBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.Settler.Settle(r.Context(), req))
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
