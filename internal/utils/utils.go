package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
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

// writeCORS mirrors the headers set by main.corsMiddleware for handlers that
// write the response directly. Credentials are intentionally not advertised —
// see the comment on corsMiddleware for why.
func writeCORS(w http.ResponseWriter) {
	origin := AllowedOrigin
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie")
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

// --- Random string / password / recovery-code generation ---

// passwordAlphabet omits visually ambiguous characters (0/O, 1/l/I) so a
// generated password can be transcribed from a terminal or log without error.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// recoveryAlphabet is upper-case only for the same reason: recovery codes get
// written down and typed back in later.
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomString draws n characters uniformly from alphabet using rejection
// sampling, so every symbol is equally likely. A naive modulo would bias the
// first 256%len(alphabet) symbols; irrelevant for a password, sloppy
// nonetheless.
func randomString(alphabet string, n int) string {
	if len(alphabet) == 0 || n <= 0 {
		return ""
	}
	limit := 256 - (256 % len(alphabet))
	out := make([]byte, 0, n)
	buf := make([]byte, 1)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return ""
		}
		if int(buf[0]) >= limit {
			continue
		}
		out = append(out, alphabet[int(buf[0])%len(alphabet)])
	}
	return string(out)
}

// GeneratePassword returns a random password of n characters.
func GeneratePassword(n int) string {
	return randomString(passwordAlphabet, n)
}

// GenerateRecoveryCode returns a high-entropy one-time code formatted as
// XXXX-XXXX-XXXX-XXXX-XXXX (20 symbols from a 32-symbol alphabet, ~100 bits).
func GenerateRecoveryCode() string {
	raw := randomString(recoveryAlphabet, 20)
	if raw == "" {
		return ""
	}
	parts := make([]string, 0, 5)
	for i := 0; i < len(raw); i += 4 {
		parts = append(parts, raw[i:i+4])
	}
	return strings.Join(parts, "-")
}

// GenerateRecoveryKey returns the permanent master recovery key, formatted as
// XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX (32 symbols, ~160 bits). Longer than
// a one-time code because this key never rotates automatically: it must stay
// off brute-force radars for the lifetime of the installation. It is hashed
// with the same normalisation as recovery codes (dashes / case optional).
func GenerateRecoveryKey() string {
	raw := randomString(recoveryAlphabet, 32)
	if raw == "" {
		return ""
	}
	parts := make([]string, 0, 8)
	for i := 0; i < len(raw); i += 4 {
		parts = append(parts, raw[i:i+4])
	}
	return strings.Join(parts, "-")
}

// NormalizeRecoveryCode drops separators and case so a code typed without its
// dashes, or in lower case, still verifies.
func NormalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch r {
		case '-', ' ', '\t', '\r', '\n':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// HashRecoveryCode returns a digest of the normalised code. Recovery codes are
// already high-entropy random values, so a fast digest is the right primitive
// here: a slow KDF would only make legitimate resets sluggish and turn the
// unauthenticated recovery endpoint into a cheap CPU-exhaustion target.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeRecoveryCode(code)))
	return base64.StdEncoding.EncodeToString(sum[:])
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

// BuildCookie renders a Set-Cookie value. ttl is in seconds; Max-Age is omitted
// for non-positive values, which produces a session-ending (delete) cookie.
//
// SameSite=Strict is deliberate and load-bearing: the dashboard session cookie
// must never travel on a cross-site request. Anything that needs credentialed
// cross-origin access would have to relax this, which would in turn require
// real CSRF protection — so see the note in corsMiddleware before changing it.
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
		parts = append(parts, fmt.Sprintf("Max-Age=%d", ttl))
	}
	return strings.Join(parts, "; ")
}

// --- Client address ---

// ClientIP extracts the real client address from a request.
//
// Behind a reverse proxy every request arrives carrying the proxy's own
// address in RemoteAddr, which silently turns per-IP decisions into global
// ones: ten bad logins by an attacker would lock the legitimate operator out of
// the whole window, and every request-log entry would name the proxy instead of
// the client. Trusting the forwarding headers is only safe when a proxy really
// is in front and rewrites them, hence the explicit TRUST_PROXY opt-in —
// otherwise any client could forge its address and dodge the limiter entirely.
//
// When trusted we prefer X-Real-IP (set by the bundled nginx snippet) and fall
// back to the LAST entry of X-Forwarded-For: nginx appends the address it
// actually observed, so the final hop is trustworthy while a client-supplied
// prefix is attacker-controlled.
func ClientIP(r *http.Request) string {
	if os.Getenv("TRUST_PROXY") == "1" {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
