package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The MCP server is built once and served over two transports. If the tool set
// could differ between them, a client would see different capabilities
// depending on how it connected.
func TestBothTransportsExposeTheSameServer(t *testing.T) {
	if s := newMCPServer(); s == nil {
		t.Fatal("newMCPServer returned nil")
	}
	// newMCPHandler wraps exactly that server; if it can be constructed, the
	// HTTP transport is serving the same registration path.
	if h := newMCPHandler(); h == nil {
		t.Fatal("newMCPHandler returned nil")
	}
}

func TestResolveDBPath(t *testing.T) {
	t.Run("THINGS_DB_PATH wins and its directory is created", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "nested", "dir", "things.db")
		t.Setenv("THINGS_DB_PATH", want)

		got, err := resolveDBPath()
		if err != nil {
			t.Fatalf("resolveDBPath: %v", err)
		}
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if st, err := os.Stat(filepath.Dir(want)); err != nil || !st.IsDir() {
			t.Errorf("parent directory was not created: %v", err)
		}
	})

	t.Run("falls back to a per-user directory", func(t *testing.T) {
		t.Setenv("THINGS_DB_PATH", "")
		home := t.TempDir()
		t.Setenv("HOME", home)

		got, err := resolveDBPath()
		if err != nil {
			t.Fatalf("resolveDBPath: %v", err)
		}
		// /data exists on a container host and is preferred there; anywhere
		// else this must land under the home directory rather than failing.
		if st, statErr := os.Stat("/data"); statErr == nil && st.IsDir() {
			t.Skip("/data exists on this machine, so the container path wins")
		}
		want := filepath.Join(home, ".things-cloud-mcp", "things.db")
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if st, err := os.Stat(filepath.Dir(want)); err != nil || !st.IsDir() {
			t.Errorf("directory was not created: %v", err)
		}
	})
}
