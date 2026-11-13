// Package buildinfo reports the version of the running binary.
//
// The version is a build-time value: a release build sets it with
// -ldflags "-X tg-cli/internal/buildinfo.Version=1.2.3", a build from a working
// copy keeps "dev". The revision comes from the build information Go records
// when the binary is built inside a version control checkout.
package buildinfo

import "runtime/debug"

// Version is the release version of the tool.
var Version = "dev"

// Info describes the build the running binary was made from.
type Info struct {
	// Version is the release version, "dev" for a build from a working copy.
	Version string `json:"version"`
	// Revision is the version control revision the binary was built from.
	// It is empty when the binary carries no such information.
	Revision string `json:"revision,omitempty"`
}

// Get returns the build information of the running binary.
func Get() Info {
	info := Info{Version: Version}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				info.Revision = s.Value
				break
			}
		}
	}
	return info
}
