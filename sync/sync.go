// Package sync provides a persistent sync engine for Things Cloud.
// It stores state in SQLite and surfaces semantic change events.
package sync

import (
	"database/sql"
	"errors"
	"net"
	gosync "sync"
	"time"

	things "github.com/JamesHJung/things-cloud-mcp"
	_ "modernc.org/sqlite"
)

const (
	maxRetries    = 3
	retryBaseWait = 2 * time.Second
)

// dbExecutor is the interface for database operations (satisfied by *sql.DB and *sql.Tx)
type dbExecutor interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// Syncer manages persistent sync with Things Cloud
type Syncer struct {
	rawDB   *sql.DB    // underlying connection for Close() and Begin()
	db      dbExecutor // current executor (db or tx)
	client  *things.Client
	history *things.History
	mu      gosync.RWMutex

	// areaDeleteMode controls how area deletions cascade; see AreaDeleteMode.
	areaDeleteMode AreaDeleteMode
}

func (s *Syncer) withDB(db dbExecutor) *Syncer {
	return &Syncer{
		rawDB:          s.rawDB,
		db:             db,
		client:         s.client,
		history:        s.history,
		areaDeleteMode: s.areaDeleteMode,
	}
}

func (s *Syncer) readSyncer() *Syncer {
	return s.withDB(s.rawDB)
}

// Open creates or opens a sync database and connects to Things Cloud
func Open(dbPath string, client *things.Client) (*Syncer, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Enable WAL mode for better concurrent performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}

	s := &Syncer{
		rawDB:  db,
		db:     db,
		client: client,
	}

	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// Close closes the database connection
func (s *Syncer) Close() error {
	return s.rawDB.Close()
}

// isRetryableError reports whether an error is worth another attempt.
//
// This used to substring-match "500".."504" against the error text, which is
// both too loose and too tight: any message that happens to contain those
// digits — a task title, a byte count, an index — looked retryable, while a
// genuine 503 wrapped in different wording did not.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// A real upstream server error.
	var statusErr *things.HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode >= 500 && statusErr.StatusCode <= 504
	}

	// A stalled or dropped connection deserves another attempt; a malformed
	// request or a decode failure does not.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

// changeLogColumns is the column list every change_log read shares, so the
// queries cannot drift apart from scanChangeLog's expectations.
const changeLogColumns = `id, server_index, synced_at, change_type, entity_type,
	entity_uuid, payload, item_kind, item_action`

// Sync fetches new items from Things Cloud, updates local state,
// and returns the list of changes in order
func (s *Syncer) Sync() ([]Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get current sync state first
	storedHistoryID, startIndex, err := s.getSyncState()
	if err != nil {
		return nil, err
	}

	// Reuse stored history ID if available (avoids extra /account call)
	// Things.app does this - it goes straight to /items with the stored history ID
	if s.history == nil {
		if storedHistoryID != "" {
			// Use stored history ID directly - no network call needed
			s.history = s.client.HistoryWithID(storedHistoryID)
		} else {
			// First sync - need to fetch history ID from server
			var h *things.History
			var err error
			for attempt := 0; attempt < maxRetries; attempt++ {
				h, err = s.client.OwnHistory()
				if err == nil {
					break
				}
				if !isRetryableError(err) {
					return nil, err
				}
				time.Sleep(retryBaseWait * time.Duration(1<<attempt))
			}
			if err != nil {
				return nil, err
			}
			s.history = h
		}
	}

	// If history changed (shouldn't happen normally), start fresh
	if storedHistoryID != "" && storedHistoryID != s.history.ID {
		startIndex = 0
	}

	// Pre-check: Get latest server index to avoid out-of-bounds requests
	// A 500 error occurs when start-index > server's current-item-index
	serverIndex, err := s.getServerIndex()
	if err != nil {
		return nil, err
	}

	// If our cursor is already at or beyond the server's index, nothing to fetch
	if startIndex >= serverIndex {
		// Nothing to fetch. Still record the history id on a first sync, so
		// the next one can skip the extra /account lookup.
		if storedHistoryID == "" {
			if err := s.saveSyncState(s.history.ID, startIndex); err != nil {
				return nil, err
			}
		}
		if err := s.purgeDeleted(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Fetch items from server
	var allChanges []Change
	hasMore := true

	for hasMore {
		// Fetch with retry for transient errors
		var items []things.Item
		var more bool
		var fetchErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			items, more, fetchErr = s.history.Items(things.ItemsOptions{StartIndex: startIndex})
			if fetchErr == nil {
				break
			}
			if !isRetryableError(fetchErr) {
				return nil, fetchErr
			}
			time.Sleep(retryBaseWait * time.Duration(1<<attempt))
		}
		if fetchErr != nil {
			return nil, fetchErr
		}

		// No items returned means we're caught up
		if len(items) == 0 {
			break
		}

		// Process each item
		changes, err := s.processItems(items, startIndex)
		if err != nil {
			return nil, err
		}
		allChanges = append(allChanges, changes...)

		// Advance to the next unread index from this page.
		startIndex = s.history.LoadedServerIndex
		hasMore = more
	}

	// The cursor is persisted per batch, inside the same transaction as the
	// batch it covers (see processItems). Saving it here instead was wrong in
	// two ways: a failure part-way through a multi-page sync left the cursor
	// where it started, so already-committed batches were replayed — which
	// double-applies note delta patches and duplicates change_log rows — and
	// it recorded LatestServerIndex, the server's head, rather than what was
	// actually read, so a loop that ended early skipped the difference
	// permanently.
	if err := s.purgeDeleted(); err != nil {
		return nil, err
	}

	return allChanges, nil
}

// SchemaVersion returns the database's schema version, which tells a caller
// whether a migration this build expects has actually been applied.
func (s *Syncer) SchemaVersion() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var v int
	if err := s.rawDB.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// LastSyncedIndex returns the server index we've synced up to
func (s *Syncer) LastSyncedIndex() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, idx, _ := s.readSyncer().getSyncState()
	return idx
}

// ChangesSince returns changes that occurred after the given timestamp
func (s *Syncer) ChangesSince(timestamp time.Time) ([]Change, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.rawDB.Query(`
			SELECT `+changeLogColumns+`
			FROM change_log
			WHERE synced_at > ?
			ORDER BY id
	`, timestamp.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanChangeLog(rows)
}

// ChangesSinceIndex returns changes that occurred after the given server index
func (s *Syncer) ChangesSinceIndex(serverIndex int) ([]Change, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.rawDB.Query(`
			SELECT `+changeLogColumns+`
			FROM change_log
			WHERE server_index > ?
			ORDER BY id
	`, serverIndex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanChangeLog(rows)
}

// ChangesForEntity returns all changes for a specific entity
func (s *Syncer) ChangesForEntity(entityUUID string) ([]Change, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.rawDB.Query(`
			SELECT `+changeLogColumns+`
			FROM change_log
			WHERE entity_uuid = ?
			ORDER BY id
	`, entityUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanChangeLog(rows)
}

func (s *Syncer) scanChangeLog(rows *sql.Rows) ([]Change, error) {
	var changes []Change

	for rows.Next() {
		var (
			id          int
			serverIndex int
			syncedAt    int64
			changeType  string
			entityType  string
			entityUUID  string
			payload     sql.NullString
			itemKind    sql.NullString
			itemAction  sql.NullInt64
		)

		if err := rows.Scan(&id, &serverIndex, &syncedAt, &changeType, &entityType,
			&entityUUID, &payload, &itemKind, &itemAction); err != nil {
			return nil, err
		}

		changes = append(changes, LoggedChange{
			baseChange: baseChange{
				serverIndex: serverIndex,
				timestamp:   time.Unix(syncedAt, 0),
			},
			changeType:  changeType,
			entityType:  entityType,
			entityUUID:  entityUUID,
			payload:     payload.String,
			itemKind:    itemKind.String,
			itemAction:  int(itemAction.Int64),
			hasEnvelope: itemKind.Valid && itemAction.Valid,
		})
	}

	// Without this a truncated result set reads as a complete one.
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return changes, nil
}

// getServerIndex fetches the latest server index from Things Cloud.
// This is used to pre-check before fetching items to avoid 500 errors
// when our stored cursor is ahead of the server's current-item-index.
func (s *Syncer) getServerIndex() (int, error) {
	h, err := s.client.History(s.history.ID)
	if err != nil {
		return 0, err
	}
	return h.LatestServerIndex, nil
}
