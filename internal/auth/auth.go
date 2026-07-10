package auth

import (
	"log"
	"net/http"
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

	ip := r.RemoteAddr
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

	// First run: no password set, accept anything and set it
	isFirstRun := cfg.PasswordHash == ""

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
