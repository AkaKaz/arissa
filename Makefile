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

.PHONY: all build rebuild clean vet test deb arch

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

# ---- Arch Linux package ----
# Produces dist/arissa-$(VERSION)-1-x86_64.pkg.tar.zst via makepkg.
# Must run on an Arch host (or archlinux:base-devel container) as a
# non-root user with base-devel installed.
PKGVER := $(shell echo '$(VERSION)' | sed 's/^v//; s/-/./g')
ARCH_PKG := dist/arissa-$(PKGVER)-1-x86_64.pkg.tar.zst

arch: $(ARCH_PKG)

$(ARCH_PKG): $(BIN) systemd/arissa.service arch/PKGBUILD arch/arissa.install defaults/config.toml.default defaults/system.prompt.md.default LICENSE
	@command -v makepkg >/dev/null 2>&1 || { \
		echo "makepkg not found. Run on an Arch host or archlinux:base-devel container."; \
		exit 1; \
	}
	@mkdir -p dist
	rm -rf dist/arch-build
	mkdir -p dist/arch-build
	sed 's/$${ARISSA_VERSION}/$(PKGVER)/' arch/PKGBUILD > dist/arch-build/PKGBUILD
	cp arch/arissa.install dist/arch-build/arissa.install
	cd dist/arch-build && \
		env SRCDEST="$(CURDIR)" PKGDEST="$(CURDIR)/dist" \
		makepkg --nodeps --noextract --skipinteg --force

