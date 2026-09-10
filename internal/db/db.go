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

// useWALCheckpoint is set by InitStorage: true only when WAL mode is both
// requested and verified to actually work in this environment. Some
// container/filesystem setups cannot mmap the WAL -shm file (error
// "out of memory"), which leaves the whole database unwritable. When false, the
// periodic checkpoint loop is skipped because rollback-journal mode needs no
// checkpoint.
var useWALCheckpoint bool

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
	// Open WITHOUT forcing WAL in the DSN. WAL needs a memory-mapped -shm file,
	// and some container/filesystem setups fail that mmap with "out of memory",
	// which leaves the whole database unwritable. We open in the safe default
	// (rollback journal, mmap disabled) and only opt into WAL after a successful
	// runtime probe (see probeWAL).
	dsn := dbPath + "?_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=mmap_size(0)"
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("[db] Failed to open: %v", err)
	}
	// Single writer connection: concurrent config writes serialize through
	// SQLite instead of contending. Read-heavy paths use the in-memory config
	// cache, so a small pool does not bottleneck traffic.
	db.SetMaxOpenConns(1)

	// Create schema and fail loudly if the DB file is not writable. A silent
	// "Initialized" with a dead database is far worse than a visible crash that
	// points at volume permissions / disk space.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (
		key      TEXT PRIMARY KEY,
		value    TEXT NOT NULL,
		expires_at INTEGER
	)`); err != nil {
		// A stale -wal/-shm left by a crashed WAL-mode run can block a clean
		// open on the constrained host. As a last resort, drop them and reopen
		// (loses only uncheckpointed WAL frames, which is acceptable after a
		// failure of this kind). This keeps redeploys from looping on a crash.
		log.Printf("[db] schema init failed (%v); attempting recovery by clearing stale WAL files", err)
		db.Close()
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
		db, err = sql.Open("sqlite", dsn)
		if err != nil {
			log.Fatalf("[db] Failed to reopen after WAL cleanup: %v", err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (
			key      TEXT PRIMARY KEY,
			value    TEXT NOT NULL,
			expires_at INTEGER
		)`); err != nil {
			log.Fatalf("[db] cannot initialise schema at %s (check volume permissions / disk space): %v", dbPath, err)
		}
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_kv_expires ON kv(expires_at)`)

	// WAL is nice-to-have, not mandatory. Probe it: if enabling WAL and running
	// a real write+checkpoint works, keep the periodic checkpoint loop. If the
	// -shm mmap fails (common on constrained containers), transparently fall
	// back to rollback-journal mode so the gateway stays fully functional.
	useWALCheckpoint = probeWAL(db)
	if useWALCheckpoint {
		startWALCheckpoint(db, walCheckpointInterval())
	}

	// Clean expired
	db.Exec(`DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at < ?`, time.Now().UnixMilli())

	log.Println("[db] Initialized with SQLite")
	return &Env{
		DB:             db,
		ALLOWED_ORIGIN: os.Getenv("ALLOWED_ORIGIN"),
	}
}

// probeWAL tries to enable WAL and verifies it actually works by writing a probe
// row and truncating the WAL. Returns true only if both succeed; on any failure
// it switches back to rollback-journal mode and returns false. This keeps the
// gateway functional on hosts where the WAL -shm file cannot be mmapped.
func probeWAL(db *sql.DB) bool {
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		log.Printf("[db] WAL unavailable (%v); using rollback journal", err)
		return false
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO kv(key,value) VALUES('__db_probe__','1')`); err != nil {
		log.Printf("[db] WAL write probe failed (%v); using rollback journal", err)
		db.Exec(`PRAGMA journal_mode=DELETE`)
		return false
	}
	defer db.Exec(`DELETE FROM kv WHERE key='__db_probe__'`)
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("[db] WAL checkpoint probe failed (%v); using rollback journal", err)
		db.Exec(`PRAGMA journal_mode=DELETE`)
		return false
	}
	log.Printf("[db] journal mode: WAL (checkpoint loop enabled)")
	return true
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
