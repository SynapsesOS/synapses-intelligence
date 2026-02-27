// Package server provides the HTTP server for synapses-intelligence sidecar mode.
//
// When running as a sidecar (`brain serve`), this server exposes the Brain's
// capabilities over a simple JSON REST API on localhost:11435 (default).
// Synapses (or any other tool) can call these endpoints to get semantic enrichment
// without importing the Go module directly.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/synapses/synapses-intelligence/pkg/brain"
)

// Server is the HTTP server wrapping the Brain.
type Server struct {
	brain  brain.Brain
	port   int
	server *http.Server
}

// New creates a Server that delegates to the given Brain.
func New(b brain.Brain, port int) *Server {
	s := &Server{brain: b, port: port}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/summary/{nodeId}", s.handleGetSummary)
	mux.HandleFunc("POST /v1/ingest", s.handleIngest)
	mux.HandleFunc("POST /v1/enrich", s.handleEnrich)
	mux.HandleFunc("POST /v1/explain-violation", s.handleExplainViolation)
	mux.HandleFunc("POST /v1/coordinate", s.handleCoordinate)

	s.server = &http.Server{
		Addr:         fmt.Sprintf("127.0.0.1:%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second, // LLM calls can take up to ~3s
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// ListenAndServe starts the HTTP server. Blocks until the context is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.server.Addr, err)
	}
	log.Printf("brain: listening on http://%s (model: %s)", s.server.Addr, s.brain.ModelName())

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(shutdownCtx)
	}()

	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// --- Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"model":     s.brain.ModelName(),
		"available": s.brain.Available(),
	})
}

func (s *Server) handleGetSummary(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	summary := s.brain.Summary(nodeID)
	if summary == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no summary found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"summary": summary})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req brain.IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.NodeID == "" || req.Code == "" {
		writeError(w, http.StatusBadRequest, "node_id and code are required")
		return
	}

	resp, err := s.brain.Ingest(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleEnrich(w http.ResponseWriter, r *http.Request) {
	var req brain.EnrichRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.RootName == "" {
		writeError(w, http.StatusBadRequest, "root_name is required")
		return
	}

	resp, err := s.brain.Enrich(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleExplainViolation(w http.ResponseWriter, r *http.Request) {
	var req brain.ViolationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.RuleID == "" {
		writeError(w, http.StatusBadRequest, "rule_id is required")
		return
	}

	resp, err := s.brain.ExplainViolation(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCoordinate(w http.ResponseWriter, r *http.Request) {
	var req brain.CoordinateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.NewAgentID == "" || req.NewScope == "" {
		writeError(w, http.StatusBadRequest, "new_agent_id and new_scope are required")
		return
	}

	resp, err := s.brain.Coordinate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
