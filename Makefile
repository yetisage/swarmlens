BINARY  := swarmlens
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Keep the module cache inside the project so the build works in sandboxes
# where $HOME is read-only.
export GOPATH ?= $(CURDIR)/.gopath
export GOCACHE ?= $(CURDIR)/.gocache

.PHONY: all build test vet fmt lint check run render smoke clean tidy install ci

all: check build

build: ## build the binary
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) .

test: ## run the tests
	go test ./... -count=1

vet: ## run go vet
	go vet ./...

fmt: ## format the code
	gofmt -w .

lint: fmt vet ## format and vet

check: vet test ## vet + test

tidy: ## tidy go.mod / go.sum
	go mod tidy

run: build ## build and run
	./$(BINARY)

render: build ## render one static frame
	./$(BINARY) --render

smoke: build ## run the non-interactive self-test
	./$(BINARY) --smoke

install: ## install into GOBIN
	go install -ldflags '$(LDFLAGS)' .

ci: vet test ## what CI runs

clean: ## remove build artifacts
	rm -f $(BINARY)
	rm -rf .gocache
