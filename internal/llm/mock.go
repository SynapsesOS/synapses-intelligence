package llm

import "context"

// MockClient is a deterministic LLM client for tests.
// It returns a fixed response for every Generate call.
type MockClient struct {
	Response  string
	Err       error
	model     string
	available bool
}

// NewMockClient creates a MockClient that always returns the given response.
func NewMockClient(response string) *MockClient {
	return &MockClient{
		Response:  response,
		model:     "mock:test",
		available: true,
	}
}

// NewUnavailableMockClient creates a MockClient that reports itself unavailable.
func NewUnavailableMockClient() *MockClient {
	return &MockClient{
		model:     "mock:test",
		available: false,
	}
}

func (m *MockClient) Generate(_ context.Context, _ string) (string, error) {
	return m.Response, m.Err
}

func (m *MockClient) Available(_ context.Context) bool {
	return m.available
}

func (m *MockClient) ModelName() string {
	return m.model
}
