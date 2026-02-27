.PHONY: build test lint clean tidy

BINARY := brain
BUILD_DIR := ./bin
GO := /usr/local/go/bin/go

build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY) ./cmd/brain

test:
	$(GO) test ./... -v -timeout 30s

test-short:
	$(GO) test ./... -short -timeout 30s

bench:
	$(GO) test -bench=. -benchmem ./internal/...

lint:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILD_DIR)

# Run the brain sidecar server (requires Ollama running)
serve: build
	$(BUILD_DIR)/$(BINARY) serve

# Show status
status: build
	$(BUILD_DIR)/$(BINARY) status

# Download the recommended default model via Ollama
pull-model:
	ollama pull qwen2.5-coder:1.5b

# Download the recommended upgrade model
pull-model-qwen3:
	ollama pull qwen3:1.7b
