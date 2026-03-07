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

	"github.com/SynapsesOS/synapses-intelligence/internal/embed"
	"github.com/SynapsesOS/synapses-intelligence/pkg/brain"
)

// Server is the HTTP server wrapping the Brain.
type Server struct {
	brain       brain.Brain
	port        int
	server      *http.Server
	embedServer *embed.Server // nil when embedding is disabled
}

// New creates a Server that delegates to the given Brain.
// timeoutMS is the configured LLM timeout; WriteTimeout is set to 2× this value
// so LLM handlers always have time to write their response after inference completes.
func New(b brain.Brain, port int, timeoutMS int) *Server {
	s := &Server{brain: b, port: port}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/summary/{nodeId}", s.handleGetSummary)
	mux.HandleFunc("POST /v1/ingest", s.handleIngest)
	mux.HandleFunc("POST /v1/enrich", s.handleEnrich)
	mux.HandleFunc("POST /v1/explain-violation", s.handleExplainViolation)
	mux.HandleFunc("POST /v1/coordinate", s.handleCoordinate)
	mux.HandleFunc("POST /v1/prune", s.handlePrune)

	// v0.2.0 endpoints
	mux.HandleFunc("POST /v1/context-packet", s.handleContextPacket)
	mux.HandleFunc("GET /v1/sdlc", s.handleGetSDLC)
	mux.HandleFunc("PUT /v1/sdlc/phase", s.handleSetPhase)
	mux.HandleFunc("PUT /v1/sdlc/mode", s.handleSetMode)
	mux.HandleFunc("POST /v1/decision", s.handleLogDecision)
	mux.HandleFunc("GET /v1/patterns", s.handleGetPatterns)

	// v0.6.0 endpoints
	mux.HandleFunc("POST /v1/adr", s.handleUpsertADR)
	mux.HandleFunc("GET /v1/adr", s.handleListADRs)
	mux.HandleFunc("GET /v1/adr/{id}", s.handleGetADR)

	// v0.7.0 endpoints — Ollama-free embeddings
	mux.HandleFunc("POST /v1/embed", s.handleEmbed)

	writeTimeout := time.Duration(timeoutMS*2) * time.Millisecond
	s.server = &http.Server{
		Addr:         fmt.Sprintf("127.0.0.1:%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: writeTimeout,
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

func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	pruned, err := s.brain.Prune(r.Context(), req.Content)
	if err != nil {
		// Non-fatal: return original content with a warning header.
		pruned = req.Content
		w.Header().Set("X-Prune-Warning", err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pruned":          pruned,
		"original_length": len(req.Content),
		"pruned_length":   len(pruned),
	})
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

// --- ADR handlers ---

func (s *Server) handleUpsertADR(w http.ResponseWriter, r *http.Request) {
	var req brain.ADRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.brain.UpsertADR(req); err != nil {
		writeError(w, http.StatusInternalServerError, "upsert adr: "+err.Error())
		return
	}
	adr, err := s.brain.GetADR(req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get adr after upsert: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, adr)
}

func (s *Server) handleListADRs(w http.ResponseWriter, r *http.Request) {
	fileFilter := r.URL.Query().Get("file")
	if fileFilter != "" {
		adrs, err := s.brain.GetADRsForFile(fileFilter, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get adrs for file: "+err.Error())
			return
		}
		if adrs == nil {
			adrs = []brain.ADR{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"adrs": adrs, "count": len(adrs), "file_filter": fileFilter})
		return
	}
	adrs, err := s.brain.AllADRs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list adrs: "+err.Error())
		return
	}
	if adrs == nil {
		adrs = []brain.ADR{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"adrs": adrs, "count": len(adrs)})
}

func (s *Server) handleGetADR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adr, err := s.brain.GetADR(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "adr not found: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, adr)
}

// SetEmbedServer wires an embed.Server into this HTTP server so that
// POST /v1/embed is handled. Call this before ListenAndServe.
func (s *Server) SetEmbedServer(es *embed.Server) {
	s.embedServer = es
}

// handleEmbed handles POST /v1/embed.
// Request body: {"input": "text to embed"}
// Response:     {"embedding": [float, ...], "model": "nomic-embed-text-v1.5.Q4_K_M", "dim": N}
// Returns 503 when the embedding server is not configured or unavailable.
func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	if s.embedServer == nil {
		writeError(w, http.StatusServiceUnavailable,
			"embedding not enabled — run: brain setup --with-embeddings")
		return
	}

	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Input == "" {
		writeError(w, http.StatusBadRequest, "field 'input' is required")
		return
	}

	vec, err := s.embedServer.Embed(r.Context(), req.Input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "embed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"embedding": vec,
		"dim":       len(vec),
		"model":     "nomic-embed-text-v1.5.Q4_K_M",
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
