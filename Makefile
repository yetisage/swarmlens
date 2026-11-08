BINARY  := swarmlens
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Keep the module cache inside the project so the build works in sandboxes
# where $HOME is read-only.
export GOPATH ?= $(CURDIR)/.gopath
export GOCACHE ?= $(CURDIR)/.gocache

# gofmt is pointed at our own code: the module cache above lives in the tree.
SRC := main.go internal

.PHONY: all build test vet fmt lint check run demo render smoke live clean tidy install ci

all: check build

build: ## build the binary
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) .

test: ## run the tests
	go test ./... -count=1

vet: ## run go vet
	go vet ./...

fmt: ## format the code
	gofmt -w $(SRC)

lint: fmt vet ## format and vet

check: vet test ## vet + test

tidy: ## tidy go.mod / go.sum
	go mod tidy

run: build ## build and run against the local daemon
	./$(BINARY)

demo: build ## build and run against the in-memory demo swarm
	./$(BINARY) --demo

render: build ## print one frame of the demo as text
	./$(BINARY) --demo --render

smoke: build ## read-only self-test against the local daemon
	./$(BINARY) --smoke

live: ## read-only API test against the local daemon
	SWARMLENS_LIVE=1 go test ./internal/api -run Live -v -count=1

install: ## install into GOBIN
	go install -ldflags '$(LDFLAGS)' .

ci: vet test ## what CI runs

clean: ## remove build artifacts
	rm -f $(BINARY)
	rm -rf .gocache
