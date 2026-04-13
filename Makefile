# ============================================================
# arissa — Go binary for linux/amd64
# ============================================================
#
# Assumes a Linux toolchain (go, make, dpkg-dev). Use the dev
# container (.devcontainer/) or a Debian/Ubuntu host.
# ============================================================

include version.mk

VERSION := $(ARISSA_VERSION)

BIN     := bin/arissa
PKG     := arissa/cmd/arissa
LDFLAGS := -s -w -X arissa/internal/version.Version=$(VERSION)

GO_SOURCES := $(shell find cmd internal -type f -name '*.go' 2>/dev/null)

.PHONY: all build rebuild clean vet test

all: build

build: $(BIN)

$(BIN): $(GO_SOURCES) go.mod
	@mkdir -p $(dir $(BIN))
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags='$(LDFLAGS)' -o $(BIN) $(PKG)
	@echo "Built $(BIN) ($$(du -h $(BIN) | cut -f1))"

vet:
	go vet ./...

test:
	go test ./...

rebuild:
	@rm -f $(BIN)
	@$(MAKE) --no-print-directory build

clean:
	rm -rf bin/ dist/
