package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	things "github.com/JamesHJung/things-cloud-mcp"
	"github.com/JamesHJung/things-cloud-mcp/sync"
)

var (
	client  *things.Client
	syncer  *sync.Syncer
	history *things.History
)

type initialSyncer interface {
	Sync() ([]sync.Change, error)
}

type shutdownServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

const shutdownTimeout = 10 * time.Second

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// The header is already written, so this can only be logged.
		log.Printf("[HTTP] encoding response: %v", err)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		// The header is already written, so this can only be logged.
		log.Printf("[HTTP] encoding response: %v", err)
	}
}

func handleVerify(w http.ResponseWriter, r *http.Request) {
	resp, err := client.Verify()
	if err != nil {
		jsonError(w, fmt.Sprintf("verification failed: %v", err), 401)
		return
	}
	jsonResponse(w, resp)
}

func paginationQueryOpts(r *http.Request) (sync.QueryOpts, error) {
	opts := sync.QueryOpts{}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return opts, fmt.Errorf("limit must be a non-negative integer")
		}
		opts.Limit = limit
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return opts, fmt.Errorf("offset must be a non-negative integer")
		}
		opts.Offset = offset
	}

	return opts, nil
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	changes, err := syncer.Sync()
	if err != nil {
		jsonError(w, fmt.Sprintf("sync failed: %v", err), 500)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"changes_count": len(changes),
	})
}

func handleInbox(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	tasks, err := state.TasksInInbox(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get inbox: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleToday(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	tasks, err := state.TasksInToday(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get today: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleAnytime(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	tasks, err := state.TasksInAnytime(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get anytime: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleSomeday(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	tasks, err := state.TasksInSomeday(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get someday: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleUpcoming(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	tasks, err := state.TasksInUpcoming(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get upcoming: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleProjects(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	projects, err := state.AllProjects(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get projects: %v", err), 500)
		return
	}
	if projects == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, projects)
}

func handleAreas(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	areas, err := state.AllAreasWithOpts(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get areas: %v", err), 500)
		return
	}
	if areas == nil {
		jsonResponse(w, []*things.Area{})
		return
	}
	jsonResponse(w, areas)
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := syncer.State()
	tags, err := state.AllTagsWithOpts(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get tags: %v", err), 500)
		return
	}
	if tags == nil {
		jsonResponse(w, []*things.Tag{})
		return
	}
	jsonResponse(w, tags)
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := os.Getenv("API_KEY")
		if apiKey == "" {
			next(w, r)
			return
		}
		presented, ok := bearerToken(r)
		if !ok || !tokenMatches(presented, apiKey) {
			jsonError(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}

func debugAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("DEBUG") != "true" {
			jsonError(w, "not found", 404)
			return
		}

		apiKey := os.Getenv("API_KEY")
		if apiKey == "" {
			jsonError(w, "debug endpoints require API_KEY", 503)
			return
		}

		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			jsonError(w, "unauthorized", 401)
			return
		}

		next(w, r)
	}
}

func runInitialSync(s initialSyncer) error {
	log.Println("Performing initial sync...")
	changes, err := s.Sync()
	if err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}
	log.Printf("Initial sync complete: %d changes", len(changes))
	return nil
}

func serveWithGracefulShutdown(ctx context.Context, srv shutdownServer, timeout time.Duration) error {
	serverStopped := make(chan struct{})
	shutdownErrCh := make(chan error, 1)

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			shutdownErrCh <- srv.Shutdown(shutdownCtx)
		case <-serverStopped:
		}
	}()

	err := srv.ListenAndServe()
	close(serverStopped)

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	select {
	case shutdownErr := <-shutdownErrCh:
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			log.Printf("graceful shutdown failed: %v", shutdownErr)
		}
	case <-ctx.Done():
		shutdownErr := <-shutdownErrCh
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			log.Printf("graceful shutdown failed: %v", shutdownErr)
		}
	default:
	}

	return nil
}

// resolveDBPath picks where the sync database lives: THINGS_DB_PATH, else the
// container path when it is usable, else a per-user directory.
//
// The container path used to be hardcoded, which is fine inside the image and
// wrong everywhere else — running locally meant creating /data by hand.
func resolveDBPath() (string, error) {
	if p := os.Getenv("THINGS_DB_PATH"); p != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		return p, nil
	}

	const containerDir = "/data"
	if st, err := os.Stat(containerDir); err == nil && st.IsDir() {
		if f, err := os.CreateTemp(containerDir, ".writable-*"); err == nil {
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return filepath.Join(containerDir, "things.db"), nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".things-cloud-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "things.db"), nil
}

// serveStdio runs the MCP server over stdin/stdout for a local client.
//
// stdout carries the protocol, so nothing may be written to it. The log
// package writes to stderr, which is where the client shows server output, so
// the sync logging stays visible and harmless.
//
// There is no HTTP listener, no port and no MCP_TOKEN in this mode: the
// transport is a pipe from the client that launched the process, so there is
// nothing to authenticate and nothing exposed to a network.
func serveStdio() error {
	log.SetOutput(os.Stderr)
	log.Println("Serving MCP over stdio; protocol on stdout, logs on stderr")
	return mcpserver.ServeStdio(newMCPServer())
}

func main() {
	username := os.Getenv("THINGS_USERNAME")
	password := os.Getenv("THINGS_PASSWORD")
	if username == "" || password == "" {
		log.Fatal("THINGS_USERNAME and THINGS_PASSWORD must be set")
	}

	stdio := flag.Bool("stdio", false,
		"Serve MCP over stdin/stdout instead of HTTP, for a local MCP client "+
			"that launches this process. No port is opened.")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// THINGS_API_ENDPOINT exists so the server can be pointed at a stub for
	// testing — the stdio transport in particular cannot be exercised at all
	// without starting the process, and startup is fail-fast on a real sync.
	// Unset, it is the real Things Cloud endpoint.
	endpoint := os.Getenv("THINGS_API_ENDPOINT")
	if endpoint == "" {
		endpoint = things.APIEndpoint
	}
	client = things.New(endpoint, username, password)
	client.Debug = os.Getenv("DEBUG") == "true"

	dbPath, err := resolveDBPath()
	if err != nil {
		log.Fatalf("failed to resolve database path: %v", err)
	}
	syncer, err = sync.Open(dbPath, client)
	if err != nil {
		log.Fatalf("failed to open sync database %s: %v", dbPath, err)
	}
	defer syncer.Close()

	if err := runInitialSync(syncer); err != nil {
		log.Fatal(err)
	}

	// Get history for write operations
	history, err = client.OwnHistory()
	if err != nil {
		log.Fatalf("failed to get history: %v", err)
	}
	if err := history.Sync(); err != nil {
		log.Fatalf("failed to sync history: %v", err)
	}
	log.Println("History ready for writes")

	// stdio mode stops here: same MCP server, different transport, no listener.
	if *stdio {
		if err := serveStdio(); err != nil {
			log.Fatalf("stdio server: %v", err)
		}
		return
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			jsonError(w, "not found", 404)
			return
		}
		jsonResponse(w, map[string]string{"service": "things-cloud-api", "status": "ok"})
	})

	http.HandleFunc("/api/verify", authMiddleware(handleVerify))
	http.HandleFunc("/api/sync", authMiddleware(handleSync))
	http.HandleFunc("/api/tasks/inbox", authMiddleware(handleInbox))
	http.HandleFunc("/api/tasks/today", authMiddleware(handleToday))
	http.HandleFunc("/api/tasks/anytime", authMiddleware(handleAnytime))
	http.HandleFunc("/api/tasks/someday", authMiddleware(handleSomeday))
	http.HandleFunc("/api/tasks/upcoming", authMiddleware(handleUpcoming))
	http.HandleFunc("/api/projects", authMiddleware(handleProjects))
	http.HandleFunc("/api/areas", authMiddleware(handleAreas))
	http.HandleFunc("/api/tags", authMiddleware(handleTags))

	// Write endpoints
	http.HandleFunc("/api/tasks/create", authMiddleware(handleCreateTask))
	http.HandleFunc("/api/tasks/complete", authMiddleware(handleCompleteTask))
	http.HandleFunc("/api/tasks/trash", authMiddleware(handleTrashTask))
	http.HandleFunc("/api/tasks/edit", authMiddleware(handleEditTask))

	// Debug endpoint — dump raw write history items
	http.HandleFunc("/api/debug/history", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		items, err := history.RawItems()
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(items); err != nil {
			// The header is already written, so this can only be logged.
			log.Printf("[HTTP] encoding response: %v", err)
		}
	}))

	// Debug endpoint — list all history keys (uses SDK method with auth)
	http.HandleFunc("/api/debug/histories", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		histories, err := client.Histories()
		if err != nil {
			jsonError(w, fmt.Sprintf("list histories failed: %v", err), 500)
			return
		}
		keys := make([]string, len(histories))
		for i, h := range histories {
			keys[i] = h.ID
		}
		jsonResponse(w, map[string]any{
			"own_history": history.ID,
			"all_keys":    keys,
			"total":       len(keys),
		})
	}))

	// Debug endpoint — delete the current history key (uses SDK method with auth)
	http.HandleFunc("/api/debug/delete-history", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", 405)
			return
		}
		keyToDelete := history.ID
		var body struct {
			Key string `json:"key"`
		}
		ok, err := decodeOptionalJSONBody(w, r, &body)
		if err != nil {
			if isRequestBodyTooLarge(err) {
				jsonError(w, errRequestBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
				return
			}
		} else if ok && body.Key != "" {
			keyToDelete = body.Key
		}
		h := client.HistoryWithID(keyToDelete)
		if err := h.Delete(); err != nil {
			jsonError(w, fmt.Sprintf("delete failed: %v", err), 500)
			return
		}
		log.Printf("[DELETE-HISTORY] key=%s deleted successfully", keyToDelete)
		jsonResponse(w, map[string]any{
			"deleted": keyToDelete,
			"status":  "accepted",
		})
	}))

	// Debug endpoint — delete account and recreate it
	http.HandleFunc("/api/debug/nuke-account", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", 405)
			return
		}
		email := client.EMail
		password := os.Getenv("THINGS_PASSWORD")

		log.Printf("[NUKE] Deleting account %s...", email)
		if err := client.Accounts.Delete(); err != nil {
			jsonError(w, fmt.Sprintf("delete failed: %v", err), 500)
			return
		}
		log.Printf("[NUKE] Account deleted. Recreating...")

		newClient, err := client.Accounts.SignUp(email, password)
		if err != nil {
			jsonError(w, fmt.Sprintf("signup failed: %v", err), 500)
			return
		}
		log.Printf("[NUKE] Signup complete. Account needs email confirmation.")

		_ = newClient
		jsonResponse(w, map[string]any{
			"status":  "account deleted and re-created",
			"email":   email,
			"message": "Check email for confirmation code, then POST to /api/debug/confirm-account",
		})
	}))

	// Debug endpoint — confirm account with email code
	http.HandleFunc("/api/debug/confirm-account", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", 405)
			return
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := decodeJSONBody(w, r, &body); err != nil {
			if isRequestBodyTooLarge(err) {
				jsonError(w, errRequestBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			jsonError(w, "JSON body with 'code' required", 400)
			return
		}
		if body.Code == "" {
			jsonError(w, "JSON body with 'code' required", 400)
			return
		}
		if err := client.Accounts.Confirm(body.Code); err != nil {
			jsonError(w, fmt.Sprintf("confirm failed: %v", err), 500)
			return
		}
		if err := client.Accounts.AcceptSLA(); err != nil {
			log.Printf("[CONFIRM] SLA accept failed (non-fatal): %v", err)
		}
		log.Printf("[CONFIRM] Account confirmed and SLA accepted")
		jsonResponse(w, map[string]any{
			"status": "confirmed",
		})
	}))

	// MCP endpoint (no bearer auth — claude.ai connectors use OAuth which we don't implement)
	// MCP_TOKEN is opt-in; unset leaves /mcp unauthenticated as before.
	http.Handle("/mcp", mcpAuthMiddleware(newMCPHandler()))

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: ":" + port}

	log.Printf("Starting server on :%s", port)
	if err := serveWithGracefulShutdown(shutdownCtx, server, shutdownTimeout); err != nil {
		log.Fatal(err)
	}
}
