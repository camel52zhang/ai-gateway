package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"ai-gateway/internal/auth"
	"ai-gateway/internal/storage"
	"ai-gateway/internal/utils"
)

func issueRecoveryCodes(t *testing.T, sid string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/recovery/generate", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sid})
	rec := httptest.NewRecorder()
	HandleRecoveryGenerate(rec, req)
	if rec.Code != 200 {
		t.Fatalf("HandleRecoveryGenerate status = %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Codes []string `json:"codes"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode recovery codes: %v", err)
	}
	if resp.Count != 10 || len(resp.Codes) != 10 {
		t.Fatalf("expected 10 codes, got count=%d len=%d", resp.Count, len(resp.Codes))
	}
	return resp.Codes
}

func recoveryPostReset(t *testing.T, code, newPassword string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"code": code, "newPassword": newPassword})
	req := httptest.NewRequest(http.MethodPost, "/auth/recovery", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	auth.HandleRecoveryReset(rec, req)
	return rec
}

func recoveryPostLogin(t *testing.T, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	auth.HandleLogin(rec, req)
	return rec
}

// TestRecoveryGenerateRequiresAuth makes sure an anonymous caller cannot mint
// itself a set of reset codes.
func TestRecoveryGenerateRequiresAuth(t *testing.T) {
	setupModelEnabledTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/recovery/generate", nil)
	rec := httptest.NewRecorder()
	HandleRecoveryGenerate(rec, req)

	if rec.Code != 401 {
		t.Fatalf("expected 401 without a session, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRecoveryCodesAreStoredHashed verifies the plaintext codes never reach the
// database — only their digests do.
func TestRecoveryCodesAreStoredHashed(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	codes := issueRecoveryCodes(t, sid)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.RecoveryCodes) != 10 {
		t.Fatalf("expected 10 stored digests, got %d", len(cfg.RecoveryCodes))
	}
	for _, c := range codes {
		if utils.TimingSafeCompare(cfg.RecoveryCodes[0], c) {
			t.Fatalf("recovery code was stored in plaintext")
		}
	}
	if cfg.RecoveryCodes[0] != utils.HashRecoveryCode(codes[0]) {
		t.Fatalf("stored digest does not match the issued code")
	}
}

// TestRecoveryResetFlow walks the whole forgotten-password path: an unknown code
// is rejected, a valid code rotates the password, the new password works, and the
// used code is burned.
func TestRecoveryResetFlow(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	codes := issueRecoveryCodes(t, sid)

	// Give the account a known password first so the reset is a real change.
	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := utils.HashPassword("original-pass")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PasswordHash = hash
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if rec := recoveryPostReset(t, "AAAA-BBBB-CCCC-DDDD", "brand-new-pass"); rec.Code != 401 {
		t.Fatalf("expected 401 for an unknown code, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec := recoveryPostReset(t, codes[0], "brand-new-pass")
	if rec.Code != 200 {
		t.Fatalf("expected 200 for a valid code, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success        bool `json:"success"`
		CodesRemaining int  `json:"codesRemaining"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if !resp.Success || resp.CodesRemaining != 9 {
		t.Fatalf("expected success with 9 codes left, got %+v", resp)
	}

	cfg, err = storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !utils.VerifyPassword("brand-new-pass", cfg.PasswordHash) {
		t.Fatalf("the new password does not verify")
	}
	if utils.VerifyPassword("original-pass", cfg.PasswordHash) {
		t.Fatalf("the old password still verifies after a reset")
	}
	if len(cfg.RecoveryCodes) != 9 {
		t.Fatalf("expected 9 codes remaining, got %d", len(cfg.RecoveryCodes))
	}

	// The login endpoint must now accept the new password.
	if rec := recoveryPostLogin(t, "admin", "brand-new-pass"); rec.Code != 200 {
		t.Fatalf("expected to log in with the new password, got %d body=%s", rec.Code, rec.Body.String())
	}

	// A burned code is single-use.
	if rec := recoveryPostReset(t, codes[0], "another-pass"); rec.Code != 401 {
		t.Fatalf("expected 401 when reusing a burnt code, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRecoveryCodeAcceptsLooseFormatting covers the normalisation path, so a
// user typing the code without dashes or in lower case still gets in.
func TestRecoveryCodeAcceptsLooseFormatting(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	codes := issueRecoveryCodes(t, sid)

	loose := strings.ToLower(strings.ReplaceAll(codes[1], "-", ""))
	if rec := recoveryPostReset(t, loose, "loose-pass-123"); rec.Code != 200 {
		t.Fatalf("expected 200 for a normalised code, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRecoveryResetRejectsShortPassword keeps the six-character floor enforced
// on the recovery path too, not just on the settings page.
func TestRecoveryResetRejectsShortPassword(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	codes := issueRecoveryCodes(t, sid)

	if rec := recoveryPostReset(t, codes[2], "12345"); rec.Code != 400 {
		t.Fatalf("expected 400 for a short password, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestFirstRunGateRejectsArbitraryPassword closes the original design hole: an
// uninitialised gateway must not hand itself to whoever logs in first.
func TestFirstRunGateRejectsArbitraryPassword(t *testing.T) {
	setupModelEnabledTest(t)
	os.Unsetenv("ALLOW_FIRST_RUN_ANY_PASSWORD")

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PasswordHash = "" // simulate a freshly created database
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if rec := recoveryPostLogin(t, "admin", "anything-goes"); rec.Code != 403 {
		t.Fatalf("expected 403 on an uninitialised gateway, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestFirstRunOptInAllowsClaim keeps the legacy "any password claims it" flow
// available, but only behind an explicit opt-in.
func TestFirstRunOptInAllowsClaim(t *testing.T) {
	setupModelEnabledTest(t)
	t.Setenv("ALLOW_FIRST_RUN_ANY_PASSWORD", "1")

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PasswordHash = ""
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if rec := recoveryPostLogin(t, "admin", "anything-goes"); rec.Code != 200 {
		t.Fatalf("expected 200 with the opt-in set, got %d body=%s", rec.Code, rec.Body.String())
	}

	// The claim must actually persist a hashed password.
	cfg, err = storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PasswordHash == "" {
		t.Fatalf("claiming the instance did not store a password hash")
	}
}

// --- Master recovery key (permanent, not consumed on use) ---

func issueRecoveryKey(t *testing.T, sid string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/recovery/key/generate", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sid})
	rec := httptest.NewRecorder()
	HandleRecoveryKeyGenerate(rec, req)
	if rec.Code != 200 {
		t.Fatalf("HandleRecoveryKeyGenerate status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode recovery key: %v", err)
	}
	if resp.Key == "" {
		t.Fatalf("issued recovery key is empty")
	}
	return resp.Key
}

// TestRecoveryKeyGenerateRequiresAuth makes sure an anonymous caller cannot
// mint or replace the master recovery key.
func TestRecoveryKeyGenerateRequiresAuth(t *testing.T) {
	setupModelEnabledTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/recovery/key/generate", nil)
	rec := httptest.NewRecorder()
	HandleRecoveryKeyGenerate(rec, req)

	if rec.Code != 401 {
		t.Fatalf("expected 401 without a session, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRecoveryKeyIsStoredHashed verifies only the digest of the master key
// reaches the database, never the plaintext.
func TestRecoveryKeyIsStoredHashed(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	key := issueRecoveryKey(t, sid)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecoveryKeyHash == "" {
		t.Fatalf("no digest stored for the master recovery key")
	}
	if utils.TimingSafeCompare(cfg.RecoveryKeyHash, key) {
		t.Fatalf("master recovery key was stored in plaintext")
	}
	if cfg.RecoveryKeyHash != utils.HashRecoveryCode(key) {
		t.Fatalf("stored digest does not match the issued key")
	}
}

// TestRecoveryKeyResetFlow walks the master-key path: the key resets the
// password, keeps working on the next reset (it is permanent, unlike the
// one-time codes), and leaves the code inventory untouched.
func TestRecoveryKeyResetFlow(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	codes := issueRecoveryCodes(t, sid)
	key := issueRecoveryKey(t, sid)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := utils.HashPassword("original-pass")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PasswordHash = hash
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	rec := recoveryPostReset(t, key, "key-pass-123")
	if rec.Code != 200 {
		t.Fatalf("expected 200 for a valid master key, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success        bool `json:"success"`
		UsedRecoveryKey bool `json:"usedRecoveryKey"`
		CodesRemaining int  `json:"codesRemaining"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if !resp.Success || !resp.UsedRecoveryKey {
		t.Fatalf("expected success via master key, got %+v", resp)
	}
	if resp.CodesRemaining != 10 {
		t.Fatalf("master key reset must not consume codes, got %d left", resp.CodesRemaining)
	}

	cfg, err = storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !utils.VerifyPassword("key-pass-123", cfg.PasswordHash) {
		t.Fatalf("the new password does not verify")
	}
	if len(cfg.RecoveryCodes) != 10 {
		t.Fatalf("expected the 10 codes to survive, got %d", len(cfg.RecoveryCodes))
	}

	// The same key works again — that is the whole point of it.
	rec = recoveryPostReset(t, key, "key-pass-456")
	if rec.Code != 200 {
		t.Fatalf("expected the master key to keep working, got %d body=%s", rec.Code, rec.Body.String())
	}
	cfg, err = storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !utils.VerifyPassword("key-pass-456", cfg.PasswordHash) {
		t.Fatalf("second reset did not install the new password")
	}

	// The key is loose-format tolerant, same as the codes.
	loose := strings.ToLower(strings.ReplaceAll(key, "-", ""))
	if rec := recoveryPostReset(t, loose, "key-pass-789"); rec.Code != 200 {
		t.Fatalf("expected 200 for a normalised master key, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Regenerating invalidates the old key. Every successful reset above
	// deliberately killed all sessions (that is the product behaviour), so the
	// authenticated key-generation call needs a fresh session first.
	freshSid, err := storage.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	_ = issueRecoveryKey(t, freshSid)
	if rec := recoveryPostReset(t, key, "key-pass-old"); rec.Code != 401 {
		t.Fatalf("expected 401 for the replaced key, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Sanity: the codes are still usable after all the key churn.
	if rec := recoveryPostReset(t, codes[0], "code-pass-123"); rec.Code != 200 {
		t.Fatalf("expected 200 for a recovery code after key resets, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRecoveryResetExhaustedMessage pins the diagnostic distinction: with no
// codes left and no master key, an invalid submission must say so explicitly
// instead of a bare "invalid code".
func TestRecoveryResetExhaustedMessage(t *testing.T) {
	setupModelEnabledTest(t)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.RecoveryCodes = nil
	cfg.RecoveryKeyHash = ""
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	rec := recoveryPostReset(t, "AAAA-BBBB-CCCC-DDDD", "whatever-pass")
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error          string `json:"error"`
		CodesRemaining int    `json:"codesRemaining"`
		RecoveryKeySet bool   `json:"recoveryKeySet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(resp.Error, "没有可用的恢复凭据") {
		t.Fatalf("expected the exhausted-credentials message, got %q", resp.Error)
	}
	if resp.CodesRemaining != 0 || resp.RecoveryKeySet {
		t.Fatalf("expected codesRemaining=0 and recoveryKeySet=false, got %+v", resp)
	}
}
