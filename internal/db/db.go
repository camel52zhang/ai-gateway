package db

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

type Env struct {
	DB             *sql.DB
	ALLOWED_ORIGIN string
}

func GetDB() *sql.DB { return db }

func InitStorage() *Env {
	dataDir := filepath.Join("data")
	os.MkdirAll(dataDir, 0755)
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "gateway.db")
	}

	var err error
	db, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		log.Fatalf("[db] Failed to open: %v", err)
	}

	db.Exec(`CREATE TABLE IF NOT EXISTS kv (
		key      TEXT PRIMARY KEY,
		value    TEXT NOT NULL,
		expires_at INTEGER
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_kv_expires ON kv(expires_at)`)

	// Clean expired
	db.Exec(`DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at < ?`, time.Now().UnixMilli())

	log.Println("[db] Initialized with SQLite")
	return &Env{
		DB: db,
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

	_, err := db.Exec(`INSERT OR REPLACE INTO kv (key, value, expires_at) VALUES (?, ?, ?)`, key, strVal, expiresAt)
	return err
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
