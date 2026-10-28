// Package version contains the release version shown by the binary.
package version

// Version is overridable at build time with
// -ldflags "-X betterSSH/internal/version.Version=x.y.z".
var Version = "0.1.0"
