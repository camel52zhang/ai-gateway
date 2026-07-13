package config

// Provider definition
type Provider struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Category   string   `json:"category"`
	Website    string   `json:"website"`
	Docs       string   `json:"docs"`
	APIKeyURL  string   `json:"apiKeyUrl"`
	BaseURL    string   `json:"baseUrl"`
	Compatible bool     `json:"compatible"`
	Icon       string   `json:"icon"`
	Models     []string `json:"models"`
	Adapter    string   `json:"adapter"`
	Priority   int      `json:"priority"`
}

// User-configured provider (with api key)
type UserProvider struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

// Custom provider definition
type CustomProvider struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Category   string   `json:"category"`
	Website    string   `json:"website"`
	Docs       string   `json:"docs"`
	APIKeyURL  string   `json:"apiKeyUrl"`
	BaseURL    string   `json:"baseUrl"`
	Compatible bool     `json:"compatible"`
	Icon       string   `json:"icon"`
	Models     []string `json:"models"`
	Adapter    string   `json:"adapter"`
	Priority   int      `json:"priority"`
	Key        string   `json:"key"`
	Source     string   `json:"source,omitempty"`
}

// Gateway config stored in SQLite
type Config struct {
	Username        string                    `json:"username"`
	PasswordHash    string                    `json:"passwordHash"`
	Providers       []UserProvider            `json:"providers"`
	UnifiedKey      string                    `json:"unifiedKey"`
	CustomProviders []CustomProvider          `json:"customProviders"`
	Models          map[string][]string       `json:"models"`
	// ModelEnabled maps "provider/model" -> whether the model is user-enabled.
	// Default (absent key) is enabled; only an explicit false disables it. This
	// lets operators hide models they don't want to surface in the Playground.
	ModelEnabled    map[string]bool         `json:"modelEnabled"`
	Stats           Stats                     `json:"stats"`
	RequestLog      []RequestLogEntry         `json:"requestLog"`
	ErrorLog        []ErrorLogEntry           `json:"errorLog"`
	ProviderLatency map[string]int            `json:"providerLatency"`
	ProviderHealth  map[string]string         `json:"providerHealth"`
	FailureMetrics  map[string]FailureMetrics `json:"failureMetrics"`
}

type Stats struct {
	Requests         int64 `json:"requests"`
	Tokens           int64 `json:"tokens"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
}

type RequestLogEntry struct {
	Timestamp string `json:"timestamp"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	RequestID string `json:"requestId"`
}

type ErrorLogEntry struct {
	Timestamp string `json:"timestamp"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Status    int    `json:"status"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Body      string `json:"body,omitempty"`
	RequestID string `json:"requestId"`
}

type FailureMetrics struct {
	Total      int            `json:"total"`
	Categories map[string]int `json:"categories"`
}

func DefaultConfig() *Config {
	return &Config{
		Username:        "admin",
		Providers:       []UserProvider{},
		CustomProviders: []CustomProvider{},
		Models:          map[string][]string{},
		ModelEnabled:    map[string]bool{},
		Stats:           Stats{},
		RequestLog:      []RequestLogEntry{},
		ErrorLog:        []ErrorLogEntry{},
		ProviderLatency: map[string]int{},
		ProviderHealth:  map[string]string{},
		FailureMetrics:  map[string]FailureMetrics{},
	}
}
