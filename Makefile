BINARY  := octidy
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

# Build targets are produced by the CI workflow; keep names aligned with dist/.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all build build-all build-linux build-darwin build-windows \
	test test-race lint vet clean

all: lint test build

build:
	mkdir -p dist
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BINARY) .

build-all:
	mkdir -p dist
	@$(foreach p,$(PLATFORMS), \
		echo ">> building $(p)"; \
		GOOS=$(word 1,$(subst /, ,$(p))) GOARCH=$(word 2,$(subst /, ,$(p))) \
		CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
			-o dist/$(BINARY)-$(subst /,-,$(p))$(if $(findstring windows,$(p)),.exe) .; \
	)

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
		-o dist/$(BINARY)-linux-amd64 .

build-darwin:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
		-o dist/$(BINARY)-darwin-amd64 .

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
		-o dist/$(BINARY)-windows-amd64.exe .

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, falling back to go vet"; \
		go vet ./...; \
	fi

clean:
	rm -rf dist
