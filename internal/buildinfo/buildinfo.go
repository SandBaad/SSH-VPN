// Package buildinfo holds the version, set at build time:
//
//	go build -ldflags "-X sshfortress/internal/buildinfo.Version=2.0.0"
package buildinfo

// Version of this build ("dev" for local builds).
var Version = "dev"
