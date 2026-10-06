package service

import (
	"runtime/debug"
	"strings"
)

// Version is the build identity of the CADS binaries. Release builds stamp it with
// -ldflags "-X github.com/norceresearch/cads-fmi-demo/orchestrator/service.Version=$(git describe --always --dirty)".
var Version = "dev"

// ResolvedVersion returns Version, or the VCS revision recorded by the Go toolchain when the
// binary was built without ldflags stamping.
func ResolvedVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	revision := ""
	dirty := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return "dev"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		revision += "-dirty"
	}
	return "dev-" + revision
}
