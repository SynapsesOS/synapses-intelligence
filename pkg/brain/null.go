package brain

import "context"

// NullBrain is a no-op Brain implementation used when the brain is disabled
// or when the LLM backend is unavailable. All methods return zero values without
// errors, so callers never need to guard against a nil Brain.
type NullBrain struct{}

func (n *NullBrain) Ingest(_ context.Context, req IngestRequest) (IngestResponse, error) {
	return IngestResponse{NodeID: req.NodeID}, nil
}

func (n *NullBrain) Enrich(_ context.Context, _ EnrichRequest) (EnrichResponse, error) {
	return EnrichResponse{Summaries: map[string]string{}}, nil
}

func (n *NullBrain) ExplainViolation(_ context.Context, _ ViolationRequest) (ViolationResponse, error) {
	return ViolationResponse{}, nil
}

func (n *NullBrain) Coordinate(_ context.Context, _ CoordinateRequest) (CoordinateResponse, error) {
	return CoordinateResponse{}, nil
}

func (n *NullBrain) Summary(_ string) string { return "" }
func (n *NullBrain) Available() bool         { return false }
func (n *NullBrain) ModelName() string       { return "" }

func (n *NullBrain) BuildContextPacket(_ context.Context, _ ContextPacketRequest) (*ContextPacket, error) {
	return nil, nil // nil packet → caller uses raw Synapses context
}

func (n *NullBrain) LogDecision(_ context.Context, _ DecisionRequest) error { return nil }

func (n *NullBrain) SetSDLCPhase(_ SDLCPhase, _ string) error   { return nil }
func (n *NullBrain) SetQualityMode(_ QualityMode, _ string) error { return nil }

func (n *NullBrain) GetSDLCConfig() SDLCConfig {
	return SDLCConfig{Phase: PhaseDevelopment, QualityMode: QualityStandard}
}

func (n *NullBrain) GetPatterns(_ string, _ int) []PatternHint { return nil }
