package main

import (
	"runtime/debug"
	"strings"
)

// Build identity
//
// "Is the deployment running current code?" used to be answerable only by
// indirect means — counting the changes a from-zero sync reported, or checking
// whether a binary existed in the image — and every one of them was specific to
// a particular change and went stale as soon as the next one landed. The server
// reported a hardcoded "1.1.0" that nothing ever bumped.
//
// The Go toolchain already stamps the commit, build time and whether the tree
// was dirty into any binary built from a checkout, so the honest answer is to
// read that and report it.

// version can be set at link time (-ldflags "-X main.version=...") for builds
// that have no VCS information, such as from an exported tarball. The stamped
// revision is preferred when both are present.
var version string

type buildInfo struct {
	Revision string `json:"revision"`           // full commit sha, or "unknown"
	Short    string `json:"revision_short"`     // first 9 characters
	Time     string `json:"built_at,omitempty"` // RFC3339, from the commit
	Modified bool   `json:"dirty"`              // built from a dirty tree
	Version  string `json:"version,omitempty"`  // -ldflags override, if given
}

func currentBuild() buildInfo {
	b := buildInfo{Revision: "unknown", Version: version}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.time":
			b.Time = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	if b.Revision != "unknown" && len(b.Revision) >= 9 {
		b.Short = b.Revision[:9]
	}
	return b
}

// String is what goes in the startup log and the MCP server version, so it has
// to stay short while still being unambiguous.
func (b buildInfo) String() string {
	var sb strings.Builder
	switch {
	case b.Short != "":
		sb.WriteString(b.Short)
	case b.Version != "":
		sb.WriteString(b.Version)
	default:
		sb.WriteString("unknown")
	}
	if b.Modified {
		sb.WriteString("-dirty")
	}
	if b.Time != "" {
		sb.WriteString(" (" + b.Time + ")")
	}
	return sb.String()
}
