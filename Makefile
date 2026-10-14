# The one build system. Run targets in the dev image: bin/dev run make <target>
GO  ?= go
BIN := build/mirrormark

all: check build
.PHONY: all

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/mirrormark
.PHONY: build

check: check-fmt check-vet check-tests
.PHONY: check

check-fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt -w needed:"; echo "$$out"; exit 1; fi
.PHONY: check-fmt

check-vet:
	$(GO) vet ./...
.PHONY: check-vet

check-tests:
	$(GO) test -count=1 ./...
.PHONY: check-tests

# Not part of check: the race detector makes the fuzz tests about four times
# slower. Run it before sending a change that touches goroutines.
check-race:
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...
.PHONY: check-race

clean:
	rm -rf build
.PHONY: clean
