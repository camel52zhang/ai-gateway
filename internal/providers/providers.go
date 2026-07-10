package providers

import (
	"sort"
	"strings"

	"ai-gateway/internal/config"
)

var builtinProviders = []config.Provider{
	{ID: "openai", Label: "OpenAI", Category: "official", Website: "https://openai.com", Docs: "https://platform.openai.com/docs", APIKeyURL: "https://platform.openai.com/api-keys", BaseURL: "https://api.openai.com/v1", Compatible: true, Icon: "openai", Adapter: "openai", Priority: 100},
	{ID: "google", Label: "Google Gemini", Category: "official", Website: "https://ai.google.dev", Docs: "https://ai.google.dev/docs", APIKeyURL: "https://aistudio.google.com/app/apikey", BaseURL: "https://generativelanguage.googleapis.com", Compatible: false, Icon: "google", Adapter: "google", Priority: 90},
	{ID: "groq", Label: "Groq", Category: "official", Website: "https://groq.com", Docs: "https://console.groq.com/docs", APIKeyURL: "https://console.groq.com/keys", BaseURL: "https://api.groq.com/openai/v1", Compatible: true, Icon: "groq", Adapter: "openai", Priority: 95},
	{ID: "cohere", Label: "Cohere", Category: "official", Website: "https://cohere.com", Docs: "https://docs.cohere.com", APIKeyURL: "https://dashboard.cohere.com/api-keys", BaseURL: "https://api.cohere.ai", Compatible: false, Icon: "cohere", Models: []string{"command-r-plus", "command-r", "command-r-plus-08-2024", "command-r-08-2024", "command-r7b-12-2024", "command", "command-light"}, Adapter: "cohere", Priority: 85},
	{ID: "mistral", Label: "Mistral", Category: "official", Website: "https://mistral.ai", Docs: "https://docs.mistral.ai", APIKeyURL: "https://console.mistral.ai/api-keys", BaseURL: "https://api.mistral.ai/v1", Compatible: true, Icon: "mistral", Adapter: "openai", Priority: 88},
	{ID: "xai", Label: "xAI", Category: "official", Website: "https://x.ai", Docs: "https://docs.x.ai", APIKeyURL: "https://console.x.ai/", BaseURL: "https://api.x.ai/v1", Compatible: true, Icon: "xai", Adapter: "openai", Priority: 87},
	{ID: "cerebras", Label: "Cerebras", Category: "enterprise", Website: "https://cloud.cerebras.ai", Docs: "https://inference-docs.cerebras.ai", APIKeyURL: "https://cloud.cerebras.ai/", BaseURL: "https://api.cerebras.ai/v1", Compatible: true, Icon: "cerebras", Adapter: "openai", Priority: 70},
	{ID: "nvidia", Label: "NVIDIA", Category: "enterprise", Website: "https://build.nvidia.com", Docs: "https://docs.api.nvidia.com", APIKeyURL: "https://build.nvidia.com/settings/api-keys", BaseURL: "https://integrate.api.nvidia.com/v1", Compatible: true, Icon: "nvidia", Adapter: "openai", Priority: 75},
	{ID: "openrouter", Label: "OpenRouter", Category: "aggregator", Website: "https://openrouter.ai", Docs: "https://openrouter.ai/docs", APIKeyURL: "https://openrouter.ai/keys", BaseURL: "https://openrouter.ai/api/v1", Compatible: true, Icon: "openrouter", Adapter: "openai", Priority: 92},
	{ID: "routeway", Label: "Routeway", Category: "aggregator", Website: "https://routeway.ai", Docs: "https://routeway.ai/docs", APIKeyURL: "https://routeway.ai/", BaseURL: "https://api.routeway.ai/v1", Compatible: true, Icon: "routeway", Adapter: "openai", Priority: 60},
	{ID: "bazaarlink", Label: "BazaarLink", Category: "aggregator", Website: "https://bazaarlink.ai", Docs: "https://bazaarlink.ai/docs", APIKeyURL: "https://bazaarlink.ai/", BaseURL: "https://bazaarlink.ai/api/v1", Compatible: true, Icon: "bazaarlink", Adapter: "openai", Priority: 55},
	{ID: "ollama", Label: "Ollama", Category: "local", Website: "https://ollama.com", Docs: "https://github.com/ollama/ollama", APIKeyURL: "https://ollama.com/settings/keys", BaseURL: "http://localhost:11434/v1", Compatible: true, Icon: "ollama", Adapter: "openai", Priority: 40},
	{ID: "pollinations", Label: "Pollinations", Category: "community", Website: "https://pollinations.ai", Docs: "", APIKeyURL: "https://pollinations.ai/", BaseURL: "https://text.pollinations.ai/openai", Compatible: true, Icon: "pollinations", Adapter: "openai", Priority: 30},
	{ID: "kilo", Label: "Kilo AI", Category: "community", Website: "https://kilo.ai", Docs: "", APIKeyURL: "https://app.kilo.ai/", BaseURL: "https://api.kilo.ai/api/gateway", Compatible: true, Icon: "kilo", Adapter: "openai", Priority: 35},
	{ID: "agnes", Label: "Agnes AI", Category: "community", Website: "https://platform.agnes-ai.com", Docs: "", APIKeyURL: "https://platform.agnes-ai.com/", BaseURL: "https://apihub.agnes-ai.com/v1", Compatible: true, Icon: "agnes", Adapter: "openai", Priority: 25},
	{ID: "ainative", Label: "AI Native", Category: "community", Website: "https://ainative.studio", Docs: "", APIKeyURL: "https://ainative.studio/", BaseURL: "https://api.ainative.studio", Compatible: true, Icon: "ainative", Adapter: "openai", Priority: 20},
}

var providerMap = make(map[string]config.Provider)

func init() {
	for _, p := range builtinProviders {
		providerMap[p.ID] = p
	}
}

func Get(id string) (config.Provider, bool) {
	p, ok := providerMap[id]
	return p, ok
}

func List() []config.Provider {
	return builtinProviders
}

func GetByCategory() map[string][]config.Provider {
	cat := make(map[string][]config.Provider)
	for _, p := range builtinProviders {
		c := p.Category
		if c == "" {
			c = "other"
		}
		cat[c] = append(cat[c], p)
	}
	return cat
}

func Search(keyword string) []config.Provider {
	lower := strings.ToLower(keyword)
	var results []config.Provider
	for _, p := range builtinProviders {
		if strings.Contains(strings.ToLower(p.Label), lower) || strings.Contains(strings.ToLower(p.ID), lower) {
			results = append(results, p)
		}
	}
	return results
}

func GetTargetURL(providerType string) string {
	p, ok := providerMap[providerType]
	if !ok || p.BaseURL == "" {
		return "https://api.openai.com/v1/chat/completions"
	}
	return utilsBuildChatURL(p.BaseURL)
}

func GetBaseURL(providerType string) string {
	p, ok := providerMap[providerType]
	if !ok {
		return ""
	}
	return p.BaseURL
}

func GetAdapter(providerType string) string {
	p, ok := providerMap[providerType]
	if !ok {
		return "openai"
	}
	return p.Adapter
}

// getMerged: merge builtin + custom by id
func GetMerged(customProviders []config.CustomProvider) map[string]config.Provider {
	merged := make(map[string]config.Provider)
	for _, p := range builtinProviders {
		merged[p.ID] = p
	}
	for _, cp := range customProviders {
		merged[cp.ID] = config.Provider{
			ID: cp.ID, Label: cp.Label, Category: cp.Category,
			Website: cp.Website, Docs: cp.Docs, APIKeyURL: cp.APIKeyURL,
			BaseURL: cp.BaseURL, Compatible: cp.Compatible, Icon: cp.Icon,
			Adapter: cp.Adapter, Priority: cp.Priority,
		}
	}
	return merged
}

// ResolveDefinition returns the merged provider definition (builtin or custom) for the
// given type. Unlike Get(), this also resolves custom providers registered in cfg.CustomProviders,
// so the BaseURL/Adapter of a custom provider are available to the proxy layer.
func ResolveDefinition(providerType string, customProviders []config.CustomProvider) (config.Provider, bool) {
	merged := GetMerged(customProviders)
	p, ok := merged[providerType]
	return p, ok
}

// ResolveProvider: find provider for a given model
func ResolveProvider(model string, userProviders []config.UserProvider, customProviders []config.CustomProvider) *config.UserProvider {
	merged := GetMerged(customProviders)
	// 1) Try exact model match against provider models
	for i, up := range userProviders {
		p, ok := merged[up.Type]
		if !ok {
			continue
		}
		for _, m := range p.Models {
			if m == model {
				return &userProviders[i]
			}
		}
	}
	// 2) Try well-known model prefix matching
	prefixMap := map[string]string{
		"gpt": "openai", "o1": "openai", "o3": "openai",
		"gemini":   "google",
		"command":  "cohere",
		"grok":     "xai",
		"mistral":  "mistral", "codestral": "mistral",
	}
	for prefix, providerType := range prefixMap {
		if strings.HasPrefix(model, prefix) {
			for i, up := range userProviders {
				if up.Type == providerType {
					return &userProviders[i]
				}
			}
		}
	}

	// 3) Try openrouter/ prefix
	if strings.HasPrefix(model, "openrouter/") {
		for i, up := range userProviders {
			if up.Type == "openrouter" {
				return &userProviders[i]
			}
		}
	}

	// 4) Try explicit provider prefix (e.g. "provider/model")
	parts := strings.SplitN(model, "/", 2)
	if len(parts) == 2 {
		for i, up := range userProviders {
			if up.Type == parts[0] {
				return &userProviders[i]
			}
		}
	}
	// 5) Return first configured as fallback
	if len(userProviders) > 0 {
		return &userProviders[0]
	}
	return nil
}

func GetFallbackProvider(model string, userProviders []config.UserProvider, excludeType string, customProviders []config.CustomProvider) *config.UserProvider {
	var candidates []config.UserProvider
	for _, up := range userProviders {
		if up.Type != excludeType {
			candidates = append(candidates, up)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return ResolveProvider(model, candidates, customProviders)
}

// SortedByPriority: return userProviders sorted by merged priority descending
func SortedByPriority(userProviders []config.UserProvider, customProviders []config.CustomProvider) []config.UserProvider {
	merged := GetMerged(customProviders)
	sorted := make([]config.UserProvider, len(userProviders))
	copy(sorted, userProviders)
	sort.Slice(sorted, func(i, j int) bool {
		pi := 0
		if p, ok := merged[sorted[i].Type]; ok {
			pi = p.Priority
		}
		pj := 0
		if p, ok := merged[sorted[j].Type]; ok {
			pj = p.Priority
		}
		return pi > pj
	})
	return sorted
}

// Import from utils to avoid circular dependency
func utilsBuildChatURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}
