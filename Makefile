BINARY  := swarmlens
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Keep the module cache inside the project so the build works in sandboxes
# where $HOME is read-only.
export GOPATH ?= $(CURDIR)/.gopath
export GOCACHE ?= $(CURDIR)/.gocache

# gofmt is pointed at our own code: the module cache above lives in the tree.
SRC := main.go internal

# The linter is built with the Go version of go.mod: a prebuilt one compiled
# with an older Go refuses to analyse the module. Bump the version by hand.
GOLANGCI_LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GOVERSION     := $(shell go list -m -f '{{.GoVersion}}' 2>/dev/null)

# What the releases are built for.
TARGETS := linux/amd64 linux/arm64 linux/arm linux/386 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64

.PHONY: all build test test-race test-32bit vet fmt fmt-check tidy tidy-check lint cross check ci \
        run demo render smoke live install licenses clean

all: check build

build: ## build the binary
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) .

test: ## run the tests
	go test ./... -count=1

test-race: ## run the tests with the race detector, in random order
	go test ./... -race -shuffle=on -count=1

test-32bit: ## run the tests where int is 32 bits wide (the race detector does not exist there)
	GOARCH=386 CGO_ENABLED=0 go test ./... -count=1

vet: ## run go vet
	go vet ./...

fmt: ## format the code
	gofmt -w $(SRC)

fmt-check: ## fail if anything needs gofmt
	@unformatted=$$(gofmt -l $(SRC)); \
	if [ -n "$$unformatted" ]; then echo "these files need gofmt:"; echo "$$unformatted"; exit 1; fi

tidy: ## tidy go.mod / go.sum
	go mod tidy

tidy-check: ## fail if go.mod / go.sum are not tidy
	go mod tidy -diff

lint: ## golangci-lint (see .golangci.yml)
	GOTOOLCHAIN=go$(GOVERSION) go run $(GOLANGCI_LINT) run ./...

cross: ## build and vet for every platform the releases cover
	@for t in $(TARGETS); do \
		echo "== $$t"; \
		GOOS=$${t%/*} GOARCH=$${t#*/} CGO_ENABLED=0 go build -o /dev/null . && \
		GOOS=$${t%/*} GOARCH=$${t#*/} CGO_ENABLED=0 go vet ./... || exit 1; \
	done

check: vet test ## the quick gate: vet + tests

ci: fmt-check tidy-check vet lint test-race test-32bit cross ## what CI runs (the real-swarm job aside)

run: build ## build and run against the local daemon
	./$(BINARY)

demo: build ## build and run against the in-memory demo swarm
	./$(BINARY) --demo

render: build ## print one frame of the demo as text
	./$(BINARY) --demo --render

smoke: build ## read-only self-test against the local daemon
	./$(BINARY) --smoke

live: ## read-only API tests against the local daemon
	SWARMLENS_LIVE=1 go test ./internal/api -run 'TestLive$$' -v -count=1

install: ## install into GOBIN
	go install -ldflags '$(LDFLAGS)' .

licenses: ## write THIRD_PARTY_LICENSES.txt, which binary releases carry
	sh scripts/third-party-licenses.sh THIRD_PARTY_LICENSES.txt

clean: ## remove build artifacts
	rm -f $(BINARY) THIRD_PARTY_LICENSES.txt
	rm -rf .gocache dist
