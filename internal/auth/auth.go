package auth

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"ai-gateway/internal/storage"
	"ai-gateway/internal/utils"
)

var (
	loginRateLimit   = make(map[string]*loginEntry)
	loginRateLimitMu sync.Mutex
)

type loginEntry struct {
	count   int
	resetAt time.Time
}

const (
	loginMaxAttempts = 10
	loginWindow      = 5 * time.Minute
)

// clientIP extracts the real client address used for rate limiting.
//
// Behind a reverse proxy every request arrives carrying the proxy's own
// address in RemoteAddr, which silently turns the per-IP login limiter into a
// global one: ten bad attempts by an attacker would lock the legitimate
// operator out for the whole window. Trusting the forwarding headers is only
// safe when a proxy really is in front and rewrites them, hence the explicit
// TRUST_PROXY opt-in — otherwise any client could forge its address and bypass
// the limiter entirely.
//
// When trusted we prefer X-Real-IP (set by the bundled nginx snippet) and fall
// back to the LAST entry of X-Forwarded-For: nginx appends the address it
// actually observed, so the final hop is trustworthy while a client-supplied
// prefix is attacker-controlled.
func clientIP(r *http.Request) string {
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

func checkLoginRateLimit(ip string) bool {
	loginRateLimitMu.Lock()
	defer loginRateLimitMu.Unlock()
	e, ok := loginRateLimit[ip]
	if !ok || time.Now().After(e.resetAt) {
		return false
	}
	return e.count >= loginMaxAttempts
}

func recordLoginFailure(ip string) {
	loginRateLimitMu.Lock()
	defer loginRateLimitMu.Unlock()
	e, ok := loginRateLimit[ip]
	if !ok || time.Now().After(e.resetAt) {
		loginRateLimit[ip] = &loginEntry{count: 0, resetAt: time.Now().Add(loginWindow)}
	}
	loginRateLimit[ip].count++
}

func resetLoginAttempts(ip string) {
	loginRateLimitMu.Lock()
	defer loginRateLimitMu.Unlock()
	delete(loginRateLimit, ip)
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := utils.ParseJSON(r, &body); err != nil || body.Username == "" || body.Password == "" {
		utils.JSON(w, 400, map[string]string{"error": "Invalid credentials format"})
		return
	}

	ip := clientIP(r)
	if checkLoginRateLimit(ip) {
		w.Header().Set("Retry-After", "300")
		utils.JSON(w, 429, map[string]string{"error": "Too many login attempts. Try again later."})
		return
	}

	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Server error"})
		return
	}

	// First run: no password stored yet.
	isFirstRun := cfg.PasswordHash == ""

	// Accepting an arbitrary password to claim an uninitialised instance is
	// strictly opt-in. Without this gate an instance that is merely reachable —
	// but not yet initialised — could be claimed by whoever finds it first.
	if isFirstRun && os.Getenv("ALLOW_FIRST_RUN_ANY_PASSWORD") != "1" {
		recordLoginFailure(ip)
		utils.JSON(w, 403, map[string]string{
			"error": "This gateway has no admin password configured. Set ADMIN_PASSWORD and restart, or run the binary with --reset-password.",
		})
		return
	}

	valid := isFirstRun
	if !isFirstRun {
		valid = utils.VerifyPassword(body.Password, cfg.PasswordHash)
	}

	if body.Username == cfg.Username && valid {
		resetLoginAttempts(ip)
		sid, err := storage.CreateSession(body.Username)
		if err != nil {
			log.Printf("[auth] Session creation failed: %v", err)
		}

		// Hash password on first login
		if isFirstRun {
			hash, err := utils.HashPassword(body.Password)
			if err == nil {
				cfg.PasswordHash = hash
				storage.SaveConfig(cfg)
			}
		}

		w.Header().Set("Set-Cookie", utils.BuildCookie("session_id", sid, storage.SessionTTL, true, r.TLS != nil))
		utils.JSON(w, 200, map[string]bool{"success": true})
		return
	}

	recordLoginFailure(ip)
	utils.JSON(w, 401, map[string]string{"error": "Invalid credentials"})
}

func HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err == nil && cookie.Value != "" {
		storage.DeleteSession(cookie.Value)
	}
	w.Header().Set("Set-Cookie", utils.BuildCookie("session_id", "", 0, true, r.TLS != nil))
	utils.JSON(w, 200, map[string]bool{"success": true})
}

func HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := utils.ParseJSON(r, &body); err != nil {
		utils.JSON(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}

	if body.NewPassword == "" || len(body.NewPassword) < 6 {
		utils.JSON(w, 400, map[string]string{"error": "Password must be at least 6 characters"})
		return
	}

	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Server error"})
		return
	}

	valid := false
	if cfg.PasswordHash != "" {
		valid = utils.VerifyPassword(body.CurrentPassword, cfg.PasswordHash)
	}
	if body.Username != cfg.Username || !valid {
		utils.JSON(w, 401, map[string]string{"error": "Current credentials are invalid"})
		return
	}

	hash, err := utils.HashPassword(body.NewPassword)
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Failed to hash password"})
		return
	}

	cfg.PasswordHash = hash
	storage.SaveConfig(cfg)
	utils.JSON(w, 200, map[string]bool{"success": true})
}

// HandleRecoveryReset consumes a one-time recovery code and installs a new
// password. Recovery codes are the offline fallback for a forgotten password:
// they are stored only as digests, each code works exactly once, and no
// network dependency (SMS, mail, OAuth) is involved — so this path still works
// when the host is offline or the mail provider is having a bad day.
func HandleRecoveryReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code        string `json:"code"`
		NewPassword string `json:"newPassword"`
	}
	if err := utils.ParseJSON(r, &body); err != nil || body.Code == "" || body.NewPassword == "" {
		utils.JSON(w, 400, map[string]string{"error": "Recovery code and new password are required"})
		return
	}
	if len(body.NewPassword) < 6 {
		utils.JSON(w, 400, map[string]string{"error": "Password must be at least 6 characters"})
		return
	}

	ip := clientIP(r)
	if checkLoginRateLimit(ip) {
		w.Header().Set("Retry-After", "300")
		utils.JSON(w, 429, map[string]string{"error": "Too many attempts. Try again later."})
		return
	}

	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Server error"})
		return
	}

	want := utils.HashRecoveryCode(body.Code)
	idx := -1
	for i, h := range cfg.RecoveryCodes {
		if utils.TimingSafeCompare(h, want) {
			idx = i
			break
		}
	}
	if idx < 0 {
		recordLoginFailure(ip)
		utils.JSON(w, 401, map[string]string{"error": "Invalid recovery code"})
		return
	}

	hash, err := utils.HashPassword(body.NewPassword)
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Failed to hash password"})
		return
	}

	// Burn the code and apply the new password in a single save.
	cfg.RecoveryCodes = append(cfg.RecoveryCodes[:idx], cfg.RecoveryCodes[idx+1:]...)
	cfg.PasswordHash = hash
	if err := storage.SaveConfig(cfg); err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Failed to save config"})
		return
	}

	// A reset is normally a response to being locked out or to a suspected
	// compromise, so no previously issued session should survive it.
	storage.DeleteAllSessions()

	resetLoginAttempts(ip)
	log.Printf("[auth] password reset via recovery code (%d code(s) left)", len(cfg.RecoveryCodes))
	utils.JSON(w, 200, map[string]interface{}{
		"success":        true,
		"codesRemaining": len(cfg.RecoveryCodes),
	})
}
