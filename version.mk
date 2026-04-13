# ============================================================
# arissa — version
# ============================================================
#
# Versioning scheme: semver (vX.Y.Z git tags).
#
#   Tag                         ARISSA_VERSION
#   ──────────                  ──────────────
#   v0.1.0                      0.1.0
#   v0.1.0 (dirty)              0.1.0-dirty
#   v0.1.0 + 3 commits          0.1.0-3-gabc1234
#   v0.1.0 + 3 commits + dirty  0.1.0-3-gabc1234-dirty
#   (no semver tag yet)         0.0.0-dev.abc1234
#
# Only semver-shaped tags (v[0-9]+.[0-9]+.[0-9]+) are considered.
#
# Injected into the binary at compile time via
#   go build -ldflags '-X arissa/internal/version.Version=$(ARISSA_VERSION)'
# ============================================================

_GIT_TAG := $(shell git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --dirty 2>/dev/null)
_GIT_SHA := $(shell git rev-parse --short=7 HEAD 2>/dev/null)

ifneq ($(_GIT_TAG),)
  # Strip leading v (v0.1.0 -> 0.1.0, v0.1.0-3-gabc1234 -> 0.1.0-3-gabc1234).
  ARISSA_VERSION := $(patsubst v%,%,$(_GIT_TAG))
else
  ARISSA_VERSION := 0.0.0-dev.$(_GIT_SHA)
endif
