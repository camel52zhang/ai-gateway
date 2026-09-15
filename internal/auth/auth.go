package auth

import (
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"ai-gateway/internal/storage"
	"ai-gateway/internal/utils"
)

var (
	loginRateLimit   = make(map[string]*loginEntry)
	loginRateLimitMu sync.Mutex
	// lastSweep throttles pruning of expired limiter entries (see sweepLocked).
	lastSweep time.Time
)

type loginEntry struct {
	count   int
	resetAt time.Time
}

const (
	loginMaxAttempts = 10
	loginWindow      = 5 * time.Minute
)

// sweepLocked discards entries whose window has already expired.
//
// Without this the map keeps one entry per source IP that ever failed a login,
// forever: /auth/login and /auth/recovery are unauthenticated, so a scanner
// cycling through addresses would grow the map without bound (a slow memory
// leak that only a restart clears). The sweep is amortised rather than run on
// every request so the hot path stays O(1); at most one full pass per window.
// Callers must hold loginRateLimitMu.
func sweepLocked(now time.Time) {
	if len(loginRateLimit) == 0 || now.Sub(lastSweep) < loginWindow {
		return
	}
	lastSweep = now
	for ip, e := range loginRateLimit {
		if now.After(e.resetAt) {
			delete(loginRateLimit, ip)
		}
	}
}

func checkLoginRateLimit(ip string) bool {
	loginRateLimitMu.Lock()
	defer loginRateLimitMu.Unlock()
	now := time.Now()
	sweepLocked(now)
	e, ok := loginRateLimit[ip]
	if !ok || now.After(e.resetAt) {
		return false
	}
	return e.count >= loginMaxAttempts
}

func recordLoginFailure(ip string) {
	loginRateLimitMu.Lock()
	defer loginRateLimitMu.Unlock()
	now := time.Now()
	sweepLocked(now)
	e, ok := loginRateLimit[ip]
	if !ok || now.After(e.resetAt) {
		loginRateLimit[ip] = &loginEntry{count: 0, resetAt: now.Add(loginWindow)}
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

	ip := utils.ClientIP(r)
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
	if err := storage.SaveConfig(cfg); err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Failed to save new password"})
		return
	}

	// Changing a password is how you evict whoever holds the old one, so every
	// existing session dies here — the same reasoning as HandleRecoveryReset.
	// The caller gets a fresh session in exchange, so they are not thrown out of
	// the tab they are typing in while every other device is signed out.
	storage.DeleteAllSessions()
	if sid, err := storage.CreateSession(cfg.Username); err != nil {
		log.Printf("[auth] could not re-issue session after password change: %v", err)
	} else {
		w.Header().Set("Set-Cookie", utils.BuildCookie("session_id", sid, storage.SessionTTL, true, r.TLS != nil))
	}

	utils.JSON(w, 200, map[string]bool{"success": true})
}

// HandleRecoveryReset consumes a one-time recovery code — or the permanent
// master recovery key — and installs a new password. Recovery credentials are
// the offline fallback for a forgotten password: they are stored only as
// digests, and no network dependency (SMS, mail, OAuth) is involved — so this
// path still works when the host is offline or the mail provider is having a
// bad day. The one-time codes burn on use; the master key does not, so a
// locked-out operator is never down to "codes exhausted + key lost" with
// nothing left but server-side CLI access.
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

	ip := utils.ClientIP(r)
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

	// Match against the master key first, then the one-time codes. Both are
	// high-entropy random values, so the same fast-digest + constant-time
	// comparison applies.
	want := utils.HashRecoveryCode(body.Code)
	usedKey := cfg.RecoveryKeyHash != "" && utils.TimingSafeCompare(cfg.RecoveryKeyHash, want)

	idx := -1
	if !usedKey {
		for i, h := range cfg.RecoveryCodes {
			if utils.TimingSafeCompare(h, want) {
				idx = i
				break
			}
		}
	}
	if !usedKey && idx < 0 {
		recordLoginFailure(ip)
		// Distinguish "nothing left to try" from "this one was wrong" so the
		// operator can tell whether the fault is in the code they typed or in
		// the situation itself.
		if len(cfg.RecoveryCodes) == 0 && cfg.RecoveryKeyHash == "" {
			utils.JSON(w, 401, map[string]interface{}{
				"error":          "没有可用的恢复凭据：恢复码已用尽且未设置主恢复密钥。请在服务器上运行 --reset-password 重置，或用 ADMIN_PASSWORD + RESET_PASSWORD=1 重启。",
				"codesRemaining": 0,
				"recoveryKeySet": false,
			})
			return
		}
		utils.JSON(w, 401, map[string]interface{}{
			"error":          "恢复凭据无效或已被使用",
			"codesRemaining": len(cfg.RecoveryCodes),
			"recoveryKeySet": cfg.RecoveryKeyHash != "",
		})
		return
	}

	hash, err := utils.HashPassword(body.NewPassword)
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Failed to hash password"})
		return
	}

	// Burn the code (one-time path only) and apply the new password in a
	// single save. The master key is NOT consumed: it stays valid until the
	// operator explicitly regenerates it.
	if idx >= 0 {
		cfg.RecoveryCodes = append(cfg.RecoveryCodes[:idx], cfg.RecoveryCodes[idx+1:]...)
	}
	cfg.PasswordHash = hash
	if err := storage.SaveConfig(cfg); err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Failed to save config"})
		return
	}

	// A reset is normally a response to being locked out or to a suspected
	// compromise, so no previously issued session should survive it.
	storage.DeleteAllSessions()

	resetLoginAttempts(ip)
	if usedKey {
		log.Printf("[auth] password reset via master recovery key")
	} else {
		log.Printf("[auth] password reset via recovery code (%d code(s) left)", len(cfg.RecoveryCodes))
	}
	utils.JSON(w, 200, map[string]interface{}{
		"success":        true,
		"codesRemaining": len(cfg.RecoveryCodes),
		"usedRecoveryKey": usedKey,
	})
}
