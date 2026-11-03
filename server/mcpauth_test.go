package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func TestMCPAuthDisabledWhenTokenUnset(t *testing.T) {
	t.Setenv("MCP_TOKEN", "")

	// An existing deployment that gates /mcp at the network edge sends no
	// Authorization header at all. It must keep working.
	w := httptest.NewRecorder()
	mcpAuthMiddleware(okHandler()).ServeHTTP(w, httptest.NewRequest("POST", "/mcp", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("unauthenticated request = %d, want 200 when MCP_TOKEN is unset", w.Code)
	}
}

func TestMCPAuthRequiresBearerWhenTokenSet(t *testing.T) {
	t.Setenv("MCP_TOKEN", "s3cret-token-value")

	cases := []struct {
		name string
		auth string
		want int
	}{
		{"correct token", "Bearer s3cret-token-value", http.StatusOK},
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"bare token, no scheme", "s3cret-token-value", http.StatusUnauthorized},
		{"wrong scheme", "Basic s3cret-token-value", http.StatusUnauthorized},
		{"scheme only", "Bearer ", http.StatusUnauthorized},
		{"case-wrong scheme", "bearer s3cret-token-value", http.StatusUnauthorized},
		{"token is a prefix of the real one", "Bearer s3cret", http.StatusUnauthorized},
		{"token has the real one as a prefix", "Bearer s3cret-token-value-extra", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/mcp", nil)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			mcpAuthMiddleware(okHandler()).ServeHTTP(w, r)

			if w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

// Clients discover how to authenticate from the challenge, so a rejection has
// to carry one.
func TestMCPAuthSendsBearerChallenge(t *testing.T) {
	t.Setenv("MCP_TOKEN", "tok")

	w := httptest.NewRecorder()
	mcpAuthMiddleware(okHandler()).ServeHTTP(w, httptest.NewRequest("POST", "/mcp", nil))

	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("401 must include a WWW-Authenticate challenge")
	} else if got[:7] != "Bearer " {
		t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", got)
	}
}

// MCP_TOKEN must be independent of API_KEY: reusing API_KEY would make every
// REST deployment suddenly require a header on /mcp too.
func TestMCPAuthIsIndependentOfAPIKey(t *testing.T) {
	t.Setenv("API_KEY", "rest-key")
	t.Setenv("MCP_TOKEN", "")

	w := httptest.NewRecorder()
	mcpAuthMiddleware(okHandler()).ServeHTTP(w, httptest.NewRequest("POST", "/mcp", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("API_KEY alone must not gate /mcp, got %d", w.Code)
	}

	// And the REST key must not be accepted as an MCP token.
	t.Setenv("MCP_TOKEN", "mcp-token")
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer rest-key")
	w = httptest.NewRecorder()
	mcpAuthMiddleware(okHandler()).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("API_KEY accepted on /mcp, got %d", w.Code)
	}
}

func TestTokenMatches(t *testing.T) {
	if !tokenMatches("abc", "abc") {
		t.Error("identical tokens must match")
	}
	for _, p := range []string{"", "ab", "abcd", "ABC", " abc"} {
		if tokenMatches(p, "abc") {
			t.Errorf("tokenMatches(%q, \"abc\") = true, want false", p)
		}
	}
}
