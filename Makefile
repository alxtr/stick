BINARY := stickd
GOENV := GOWORK=off
GOVULNCHECK ?= govulncheck
TEST_FLAGS ?=
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= unknown
LDFLAGS := -s -w -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
GO_FILES := $(shell find . -type d \( -name .git -o -name data -o -name node_modules -o -name vendor \) -prune -o -type f -name '*.go' -print)

.PHONY: build fmt verify-format test test-integration test-e2e test-race vet lint govulncheck image check run clean

build:
	$(GOENV) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/stickd

fmt:
	gofmt -s -w $(GO_FILES)

verify-format:
	@files="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$files" ]; then \
		printf '%s\n' 'Go sources are not formatted:' "$$files"; \
		printf '%s\n' 'Run make fmt to format Go sources.'; \
		exit 1; \
	fi

test:
	$(GOENV) CGO_ENABLED=0 go test $(TEST_FLAGS) ./...

test-integration:
	$(GOENV) CGO_ENABLED=0 go test -count=1 ./test/integration

test-e2e:
	$(GOENV) CGO_ENABLED=0 go test -count=1 ./test/e2e

test-race:
	$(GOENV) go test -race $(TEST_FLAGS) ./...

vet:
	$(GOENV) go vet ./...

lint:
	golangci-lint run -c ./golangci.yaml ./...

govulncheck:
	$(GOENV) $(GOVULNCHECK) ./...

image:
	docker build --build-arg COMMIT="$(COMMIT)" --build-arg BUILD_DATE="$(BUILD_DATE)" -t stick:local .

check: verify-format lint test

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
