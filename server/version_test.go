package main

import (
	"strings"
	"testing"
)

// The binary under test is built by `go test`, which stamps VCS information
// the same way `go build` does, so this exercises the real path.
func TestCurrentBuildReadsVCSStamp(t *testing.T) {
	b := currentBuild()

	if b.Revision == "unknown" {
		t.Skip("no VCS stamp in this build (an exported tarball, or -buildvcs=false)")
	}
	if len(b.Revision) != 40 {
		t.Errorf("revision = %q, want a 40-character sha", b.Revision)
	}
	if b.Short != b.Revision[:9] {
		t.Errorf("short = %q, want %q", b.Short, b.Revision[:9])
	}
	if b.Time == "" {
		t.Error("no build time recorded")
	}
}

func TestBuildInfoString(t *testing.T) {
	cases := []struct {
		name string
		in   buildInfo
		want string
	}{
		{"clean build", buildInfo{Short: "abc123def", Time: "2026-11-03T06:00:26Z"},
			"abc123def (2026-11-03T06:00:26Z)"},
		{"dirty build is marked", buildInfo{Short: "abc123def", Modified: true},
			"abc123def-dirty"},
		{"falls back to an ldflags version", buildInfo{Version: "v1.2.3"}, "v1.2.3"},
		{"says unknown rather than going blank", buildInfo{}, "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A dirty build must be visibly dirty wherever the version is shown, so a
// hand-built deployment is never mistaken for a tagged commit.
func TestDirtyIsAlwaysVisible(t *testing.T) {
	for _, b := range []buildInfo{
		{Short: "abc123def", Modified: true},
		{Version: "v1.2.3", Modified: true},
		{Modified: true},
	} {
		if !strings.Contains(b.String(), "-dirty") {
			t.Errorf("%+v rendered as %q, which hides that it is dirty", b, b.String())
		}
	}
}
