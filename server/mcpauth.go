package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"os"
)

// MCP endpoint authentication
//
// Every MCP client that is not a hosted connector authenticates with a bearer
// header: Claude Code (`--header "Authorization: Bearer $TOKEN"`), Codex
// (`bearer_token_env_var`), Cursor / VS Code / Windsurf (`headers` plus env
// interpolation). Without one, those clients can only reach a server that is
// either unauthenticated or behind a network gate they cannot traverse.
//
// MCP_TOKEN is therefore opt-in, and deliberately separate from API_KEY:
//
//   - MCP_TOKEN unset — /mcp is served without authentication, exactly as
//     before. Deployments that gate the endpoint at the network edge (an
//     identity-aware proxy such as Cloudflare Access, a private network, or a
//     local-only bind) keep working untouched, as do hosted connectors that
//     cannot attach a static header.
//   - MCP_TOKEN set — /mcp requires `Authorization: Bearer <MCP_TOKEN>`.
//
// API_KEY is not reused for this: it guards the REST surface, and turning it
// into an MCP requirement would silently break any already-working connector
// that relies on an edge gate instead of a header.
//
// This is a single shared secret, appropriate for the single-user deployment
// this server is built for. It identifies nobody. A multi-tenant deployment
// needs per-user identity — OAuth 2.1 with authorization code + PKCE, as the
// MCP specification describes — not a shared token handed to several people.

// tokenMatches compares two secrets without leaking their contents through
// timing. Both sides are hashed first so the comparison is over fixed-length
// input and the length of the expected token is not observable either.
func tokenMatches(presented, expected string) bool {
	p := sha256.Sum256([]byte(presented))
	e := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(p[:], e[:]) == 1
}

// bearerToken extracts the credential from an Authorization header, requiring
// the "Bearer " scheme prefix exactly as the MCP specification sends it.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
		return "", false
	}
	return auth[len(prefix):], true
}

// mcpAuthMiddleware guards the MCP endpoint with MCP_TOKEN when it is set.
func mcpAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("MCP_TOKEN")
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		presented, ok := bearerToken(r)
		if !ok || !tokenMatches(presented, token) {
			// Clients look for this challenge to discover how to authenticate.
			w.Header().Set("WWW-Authenticate", `Bearer realm="things-cloud-mcp"`)
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
