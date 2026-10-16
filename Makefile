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

# Release artefacts in build/dist/, with their SHA256SUMS. VERSION defaults
# to the extension's version in extension/manifest.json, the one place it is
# written, and must equal it: raise it there to release. Every archive entry
# is dated SOURCE_DATE_EPOCH, by default the HEAD commit's time, so building
# one commit twice gives the same bytes.
MANIFEST_VERSION := $(shell sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' extension/manifest.json)
VERSION ?= $(MANIFEST_VERSION)
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct)
DIST := build/dist
ARCHES := amd64 arm64
DIST_FILES := $(foreach a,$(ARCHES),mirrormark_$(VERSION)_linux_$(a).tar.gz) \
	mirrormark-extension_$(VERSION).zip mirrormark_$(VERSION).xpi

dist:
	@[ "$(VERSION)" = "$(MANIFEST_VERSION)" ] || { echo "VERSION $(VERSION) is not $(MANIFEST_VERSION), the version in extension/manifest.json: raise it there first"; exit 1; }
	rm -rf $(DIST) build/stage
	mkdir -p $(DIST)
	set -e; for a in $(ARCHES); do \
		d=mirrormark_$(VERSION)_linux_$$a; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$a $(GO) build -trimpath -buildvcs=false \
			-ldflags '-X main.version=$(VERSION)' -o build/stage/$$d/mirrormark ./cmd/mirrormark; \
		SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) $(GO) run ./cmd/dist tar $(DIST)/$$d.tar.gz $$d \
			build/stage/$$d/mirrormark LICENSE README.md; \
	done
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) $(GO) run ./cmd/dist zip $(DIST)/mirrormark-extension_$(VERSION).zip
	$(GO) run ./cmd/dist xpi $(DIST)/mirrormark_$(VERSION).xpi
	cd $(DIST) && sha256sum $(DIST_FILES) > SHA256SUMS
	cat $(DIST)/SHA256SUMS
.PHONY: dist

clean:
	rm -rf build
.PHONY: clean
