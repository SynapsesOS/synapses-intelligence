package brain

import (
	"context"
	"io"
)

// NullBrain is a no-op Brain implementation used when the brain is disabled
// or when the LLM backend is unavailable. All methods return zero values without
// errors, so callers never need to guard against a nil Brain.
type NullBrain struct{}

// Ingest is a no-op implementation.
func (n *NullBrain) Ingest(_ context.Context, req IngestRequest) (IngestResponse, error) {
	return IngestResponse{NodeID: req.NodeID}, nil
}

// Enrich is a no-op implementation.
func (n *NullBrain) Enrich(_ context.Context, _ EnrichRequest) (EnrichResponse, error) {
	return EnrichResponse{Summaries: map[string]string{}}, nil
}

// ExplainViolation is a no-op implementation.
func (n *NullBrain) ExplainViolation(_ context.Context, _ ViolationRequest) (ViolationResponse, error) {
	return ViolationResponse{}, nil
}

// Coordinate is a no-op implementation.
func (n *NullBrain) Coordinate(_ context.Context, _ CoordinateRequest) (CoordinateResponse, error) {
	return CoordinateResponse{}, nil
}

// Summary is a no-op implementation.
func (n *NullBrain) Summary(_ string) string { return "" }

// Available always returns false — no brain is configured.
func (n *NullBrain) Available() bool { return false }

// ModelName returns an empty string when no brain is configured.
func (n *NullBrain) ModelName() string { return "" }

// EnsureModel is a no-op implementation.
func (n *NullBrain) EnsureModel(_ context.Context, _ io.Writer) error { return nil }

// BuildContextPacket returns nil — the caller should fall back to raw Synapses context.
func (n *NullBrain) BuildContextPacket(_ context.Context, _ ContextPacketRequest) (*ContextPacket, error) {
	return nil, nil
}

// LogDecision is a no-op implementation.
func (n *NullBrain) LogDecision(_ context.Context, _ DecisionRequest) error { return nil }

// SetSDLCPhase is a no-op implementation.
func (n *NullBrain) SetSDLCPhase(_ SDLCPhase, _ string) error { return nil }

// SetQualityMode is a no-op implementation.
func (n *NullBrain) SetQualityMode(_ QualityMode, _ string) error { return nil }

// GetSDLCConfig returns safe development-phase defaults.
func (n *NullBrain) GetSDLCConfig() SDLCConfig {
	return SDLCConfig{Phase: PhaseDevelopment, QualityMode: QualityStandard}
}

// GetPatterns returns nil — no patterns are stored when brain is disabled.
func (n *NullBrain) GetPatterns(_ string, _ int) []PatternHint { return nil }
