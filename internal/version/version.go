// Package version exposes the compile-time version string.
//
// The value is injected by the build system via
//
//	go build -ldflags '-X arissa/internal/version.Version=<value>'
package version

// Version is the semver (or 0.0.0-dev.<sha>) string stamped at build time.
var Version = "dev"
