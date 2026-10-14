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
	$(GO) vet -tags e2e ./e2e/
.PHONY: check-vet

check-tests:
	$(GO) test -count=1 ./...
.PHONY: check-tests

# Not part of check: the race detector makes the fuzz tests about four times
# slower. Run it before sending a change that touches goroutines.
check-race:
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...
.PHONY: check-race

# Not part of check: needs Chrome, Brave, Firefox Developer Edition,
# LibreWolf, Xvfb and D-Bus from the dev image and takes minutes. Each test
# starts two browser instances, and the tests share Chrome's system
# directories, so none may call t.Parallel.
# ulimit -c 0: a crashing browser must not leave a core file in the tree.
check-e2e: build
	ulimit -c 0; MIRRORMARK_BIN=$(abspath $(BIN)) dbus-run-session -- xvfb-run -a -s "-screen 0 1280x800x24" \
		$(GO) test -tags e2e -count=1 -timeout 60m -v ./e2e/ $(if $(RUN),-run '$(RUN)')
.PHONY: check-e2e

clean:
	rm -rf build
.PHONY: clean
