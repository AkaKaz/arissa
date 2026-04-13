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

.PHONY: all build rebuild clean vet test deb

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

# ---- .deb package ----
DEB := dist/arissa.deb

deb: $(DEB)

$(DEB): $(BIN) systemd/arissa.service debian/control debian/postinst defaults/config.toml.default defaults/system.prompt.md.default
	@command -v dpkg-deb >/dev/null 2>&1 || { \
		echo "dpkg-deb not found. Use the dev container or a Debian/Ubuntu host."; \
		exit 1; \
	}
	rm -rf dist/pkg
	mkdir -p dist/pkg/DEBIAN
	mkdir -p dist/pkg/usr/bin
	mkdir -p dist/pkg/usr/lib/systemd/system
	mkdir -p dist/pkg/usr/share/arissa
	cp $(BIN) dist/pkg/usr/bin/arissa
	cp systemd/arissa.service dist/pkg/usr/lib/systemd/system/arissa.service
	cp defaults/config.toml.default dist/pkg/usr/share/arissa/config.toml.default
	cp defaults/system.prompt.md.default dist/pkg/usr/share/arissa/system.prompt.md.default
	sed 's/$${VERSION}/$(VERSION)/' debian/control > dist/pkg/DEBIAN/control
	cp debian/postinst dist/pkg/DEBIAN/postinst
	chmod 755 dist/pkg/DEBIAN/postinst
	dpkg-deb --build --root-owner-group dist/pkg $(DEB)
