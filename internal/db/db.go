package db

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

type Env struct {
	DB             *sql.DB
	ALLOWED_ORIGIN string
}

func GetDB() *sql.DB { return db }

// walCheckpointInterval returns how often the WAL is explicitly truncated.
// Override via WAL_CHECKPOINT_SECONDS (default 30s). A short interval keeps
// gateway.db-wal near-empty without adding meaningful write overhead.
func walCheckpointInterval() time.Duration {
	if v := os.Getenv("WAL_CHECKPOINT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Second
}

// startWALCheckpoint folds WAL frames back into the main db on a ticker so the
// WAL file does not grow unbounded. TRUNCATE resets the WAL to empty after a
// successful checkpoint; if SQLite cannot acquire the lock (active readers),
// the checkpoint is skipped until the next tick — never fatal.
func startWALCheckpoint(db *sql.DB, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				log.Printf("[db] wal_checkpoint failed: %v", err)
			}
		}
	}()
}

func InitStorage() *Env {
	dataDir := filepath.Join("data")
	os.MkdirAll(dataDir, 0755)
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "gateway.db")
	}

	var err error
	db, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=wal_autocheckpoint(100)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("[db] Failed to open: %v", err)
	}
	// Give the pool a single writer connection so concurrent config writes
	// serialize through SQLite instead of contending. Read-heavy paths use the
	// in-memory config cache, so a small pool does not bottleneck traffic.
	db.SetMaxOpenConns(1)

	// Lower autocheckpoint threshold (100 pages ≈ 400KB) so SQLite folds WAL
	// frames back into the main db passively, in addition to the explicit
	// TRUNCATE checkpoint launched below. The default of 1000 pages lets
	// gateway.db-wal grow unboundedly on a long-running server because the
	// per-request append to the request log never triggers a checkpoint.

	db.Exec(`CREATE TABLE IF NOT EXISTS kv (
		key      TEXT PRIMARY KEY,
		value    TEXT NOT NULL,
		expires_at INTEGER
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_kv_expires ON kv(expires_at)`)

	// Periodic WAL checkpoint (TRUNCATE) keeps gateway.db-wal small. Without
	// this, every write transaction — including the per-request append to the
	// request log — accumulates in the WAL file and is only folded back on the
	// default 1000-page autocheckpoint, so the file grows without bound while
	// the server runs. TRUNCATE copies WAL frames into the main db and resets
	// the WAL file to empty; it is safe under concurrent readers/writers and
	// simply no-ops until the next tick if the lock is busy.
	startWALCheckpoint(db, walCheckpointInterval())

	// Immediate checkpoint on startup so any pre-existing WAL (e.g. left over
	// from a previous long-running session) is folded back and truncated now
	// instead of waiting up to one interval for the first tick.
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("[db] initial wal_checkpoint failed: %v", err)
	}

	// Clean expired
	db.Exec(`DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at < ?`, time.Now().UnixMilli())

	log.Println("[db] Initialized with SQLite")
	return &Env{
		DB:             db,
		ALLOWED_ORIGIN: os.Getenv("ALLOWED_ORIGIN"),
	}
}

// --- KV Adapter ---

func KVGet(key string, dest interface{}) error {
	var value string
	var expiresAt sql.NullInt64
	err := db.QueryRow(`SELECT value, expires_at FROM kv WHERE key = ?`, key).Scan(&value, &expiresAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if expiresAt.Valid && time.Now().UnixMilli() > expiresAt.Int64 {
		db.Exec(`DELETE FROM kv WHERE key = ?`, key)
		return nil
	}
	switch v := dest.(type) {
	case *string:
		*v = value
	case *json.RawMessage:
		*v = json.RawMessage(value)
	default:
		return json.Unmarshal([]byte(value), dest)
	}
	return nil
}

func KVPut(key string, value interface{}, ttlSeconds ...int) error {
	var strVal string
	switch v := value.(type) {
	case string:
		strVal = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		strVal = string(b)
	}

	var expiresAt interface{} = nil
	if len(ttlSeconds) > 0 && ttlSeconds[0] > 0 {
		expiresAt = time.Now().UnixMilli() + int64(ttlSeconds[0])*1000
	}

	// Retry on SQLITE_BUSY / locked. Under WAL, a write that cannot acquire the
	// lock returns a busy error instead of blocking; busy_timeout mitigates
	// most contention, but we still retry a few times so a rapid sequence of
	// writes (e.g. first-run config generation immediately followed by a
	// provider add) never silently drops a config update.
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		_, lastErr = db.Exec(`INSERT OR REPLACE INTO kv (key, value, expires_at) VALUES (?, ?, ?)`, key, strVal, expiresAt)
		if lastErr == nil {
			return nil
		}
		if isSQLiteBusy(lastErr) {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
			continue
		}
		return lastErr
	}
	return lastErr
}

// isSQLiteBusy reports whether err is a SQLITE_BUSY / locked condition that is
// worth retrying rather than surfacing to the caller.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "table is locked")
}

func KVDelete(key string) error {
	_, err := db.Exec(`DELETE FROM kv WHERE key = ?`, key)
	return err
}

func KVList(prefix string) ([]string, error) {
	rows, err := db.Query(`SELECT key FROM kv WHERE key LIKE ? AND (expires_at IS NULL OR expires_at >= ?)`,
		prefix+"%", time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		keys = append(keys, k)
	}
	return keys, nil
}
