package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

var AllowedOrigin = "*"

// --- Response helpers ---

func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	writeCORS(w)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func HTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeCORS(w)
	w.WriteHeader(200)
	w.Write([]byte(html))
}

func NoContent(w http.ResponseWriter) {
	writeCORS(w)
	w.WriteHeader(204)
}

func writeCORS(w http.ResponseWriter) {
	origin := AllowedOrigin
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie")
	if origin != "*" {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

// --- Password hashing (PBKDF2-SHA256, 100k iter, 16-byte salt) ---
const (
	pbkdf2Iterations = 100000
	saltBytes        = 16
	keyBytes         = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, keyBytes, sha256.New)
	return base64.StdEncoding.EncodeToString(salt) + ":" + base64.StdEncoding.EncodeToString(hash), nil
}

func VerifyPassword(password, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	expectedHash, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	hash := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, keyBytes, sha256.New)
	return subtle.ConstantTimeCompare(hash, expectedHash) == 1
}

// TimingSafeCompare performs a constant-time comparison of two strings.
// Use this for API key and token comparisons to prevent timing attacks.
func TimingSafeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// --- Token / Session ID generation ---

func GenerateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func GenerateToken(prefix string) string {
	b := make([]byte, 24)
	rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// --- Cookie helpers ---

func BuildCookie(name, value string, ttl int, httpOnly, secure bool) string {
	var parts []string
	parts = append(parts, name+"="+value)
	parts = append(parts, "Path=/")
	if httpOnly {
		parts = append(parts, "HttpOnly")
	}
	parts = append(parts, "SameSite=Strict")
	if secure {
		parts = append(parts, "Secure")
	}
	if ttl > 0 {
		maxAge := int(time.Duration(ttl) * time.Second / time.Second)
		parts = append(parts, fmt.Sprintf("Max-Age=%d", maxAge))
	}
	return strings.Join(parts, "; ")
}

// --- JSON body parsing ---

func ParseJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// --- Mask key ---
func MaskKey(key string) string {
	if len(key) < 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

// --- Truncate ---
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- Time helpers ---
func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// --- URL helpers ---
func BuildModelsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return strings.Replace(base, "/chat/completions", "/models", 1)
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	// No /v1 segment: honor the user-provided base as-is (e.g. Longcat base
	// https://api.longcat.chat/openai -> .../openai/models). Never inject /v1.
	return base + "/models"
}

func BuildChatURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	// No /v1 segment: honor the user-provided base as-is. Never inject /v1.
	return base + "/chat/completions"
}
