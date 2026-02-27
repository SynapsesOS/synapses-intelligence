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
	"strconv"
	"time"

	"github.com/Divish1032/synapses-intelligence/pkg/brain"
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

	// v0.2.0 endpoints
	mux.HandleFunc("POST /v1/context-packet", s.handleContextPacket)
	mux.HandleFunc("GET /v1/sdlc", s.handleGetSDLC)
	mux.HandleFunc("PUT /v1/sdlc/phase", s.handleSetPhase)
	mux.HandleFunc("PUT /v1/sdlc/mode", s.handleSetMode)
	mux.HandleFunc("POST /v1/decision", s.handleLogDecision)
	mux.HandleFunc("GET /v1/patterns", s.handleGetPatterns)

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

// --- v0.2.0 Handlers ---

// handleContextPacket assembles a Context Packet for the calling agent.
// POST /v1/context-packet
// Body: brain.ContextPacketRequest (JSON)
// Returns: brain.ContextPacket or 204 if Brain unavailable (caller uses raw context).
func (s *Server) handleContextPacket(w http.ResponseWriter, r *http.Request) {
	var req brain.ContextPacketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	pkt, err := s.brain.BuildContextPacket(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pkt == nil {
		// Brain is unavailable or feature disabled — caller falls back to raw context.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, pkt)
}

// handleGetSDLC returns the current SDLC config.
// GET /v1/sdlc
func (s *Server) handleGetSDLC(w http.ResponseWriter, r *http.Request) {
	cfg := s.brain.GetSDLCConfig()
	writeJSON(w, http.StatusOK, cfg)
}

// handleSetPhase updates the project SDLC phase.
// PUT /v1/sdlc/phase
// Body: {"phase": "testing", "agent_id": "agent-1"}
func (s *Server) handleSetPhase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phase   brain.SDLCPhase `json:"phase"`
		AgentID string          `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Phase == "" {
		writeError(w, http.StatusBadRequest, "phase is required")
		return
	}
	if err := s.brain.SetSDLCPhase(body.Phase, body.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := s.brain.GetSDLCConfig()
	writeJSON(w, http.StatusOK, cfg)
}

// handleSetMode updates the project quality mode.
// PUT /v1/sdlc/mode
// Body: {"mode": "enterprise", "agent_id": "agent-1"}
func (s *Server) handleSetMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode    brain.QualityMode `json:"mode"`
		AgentID string            `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Mode == "" {
		writeError(w, http.StatusBadRequest, "mode is required")
		return
	}
	if err := s.brain.SetQualityMode(body.Mode, body.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := s.brain.GetSDLCConfig()
	writeJSON(w, http.StatusOK, cfg)
}

// handleLogDecision records an agent decision and updates learning patterns.
// POST /v1/decision
// Body: brain.DecisionRequest (JSON)
func (s *Server) handleLogDecision(w http.ResponseWriter, r *http.Request) {
	var req brain.DecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.EntityName == "" {
		writeError(w, http.StatusBadRequest, "entity_name is required")
		return
	}
	if err := s.brain.LogDecision(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

// handleGetPatterns returns learned co-occurrence patterns from brain.sqlite.
// GET /v1/patterns
// Query params:
//   - trigger (optional): filter to patterns for a specific entity name
//   - limit   (optional): max results, default 20
func (s *Server) handleGetPatterns(w http.ResponseWriter, r *http.Request) {
	trigger := r.URL.Query().Get("trigger")
	limit := 20
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			limit = n
		}
	}
	patterns := s.brain.GetPatterns(trigger, limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"patterns": patterns,
		"count":    len(patterns),
	})
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
