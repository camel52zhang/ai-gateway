package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"ai-gateway/internal/api"
	"ai-gateway/internal/auth"
	"ai-gateway/internal/config"
	"ai-gateway/internal/db"
	"ai-gateway/internal/providers"
	"ai-gateway/internal/proxy"
	"ai-gateway/internal/storage"
	"ai-gateway/internal/utils"
	"ai-gateway/internal/web"
)

func main() {
	env := db.InitStorage()
	storage.Init(env)

	if env.ALLOWED_ORIGIN != "" {
		utils.AllowedOrigin = env.ALLOWED_ORIGIN
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "7000"
	}

	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", proxy.HandleHealth)

	// Static files (Vue, Font Awesome)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("/webfonts/", http.StripPrefix("/webfonts/", http.FileServer(http.Dir("webfonts"))))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/favicon.ico")
	})

	// Auth endpoints (no login required)
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			auth.HandleLogin(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			auth.HandleLogout(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/auth/reset-password", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			auth.HandleResetPassword(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Login page
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if storage.IsAuthenticated(r) {
			http.Redirect(w, r, "/", 302)
			return
		}
		utils.HTML(w, web.RenderLogin())
	})

	// Dashboard (requires auth)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !storage.IsAuthenticated(r) {
			http.Redirect(w, r, "/login", 302)
			return
		}

		providerData := map[string]interface{}{
			"categories": providers.GetByCategory(),
			"providers":  buildProviderList(),
		}
		providerJSON, _ := json.Marshal(providerData)
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		utils.HTML(w, web.RenderDashboard(string(providerJSON)))
	})

	// API endpoints
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			api.HandleConfigGet(w, r)
		case "POST":
			api.HandleConfigPost(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/key/regenerate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			api.HandleKeyRegenerate(w, r)
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/providers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			api.HandleProviders(w, r)
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/providers/custom", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			api.HandleCustomProvidersGet(w, r)
		case "POST":
			api.HandleCustomProvidersPost(w, r)
		case "DELETE":
			api.HandleCustomProvidersDelete(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			api.HandleModels(w, r)
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			api.HandleStats(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// OpenAI-compatible API proxy — catch-all for /v1/*
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == "GET" {
			proxy.HandleListModels(w, r)
			return
		}
		if r.URL.Path == "/v1/responses" && r.Method == "POST" {
			api.HandleResponses(w, r)
			return
		}
		proxy.HandleProxy(w, r)
	})

	// CORS middleware
	handler := corsMiddleware(requestLogger(mux))

	log.Println(strings.Repeat("=", 33))
	log.Printf("  AI Gateway Go v1.0")
	log.Printf("  http://0.0.0.0:%s", port)

	log.Printf("  DB: data/gateway.db")
	log.Println(strings.Repeat("=", 33))

	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func buildProviderList() []map[string]interface{} {
	var list []map[string]interface{}
	for _, p := range providers.List() {
		list = append(list, map[string]interface{}{
			"id": p.ID, "label": p.Label, "category": p.Category,
			"models": p.Models, "baseUrl": p.BaseURL, "website": p.Website,
			"docs": p.Docs, "apiKeyUrl": p.APIKeyURL, "icon": p.Icon,
			"compatible": p.Compatible, "adapter": p.Adapter, "priority": p.Priority,
		})
	}
	return list
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := utils.AllowedOrigin
		if allowed == "" {
			allowed = "*"
		}
		if allowed == "*" {
			// Wildcard origin must not be combined with credentials (invalid per spec).
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			// A specific allowed origin is configured: echo the request origin and
			// allow credentials so the dashboard session cookie works cross-origin.
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = allowed
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie")

		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requestLogSkip lists paths that should not pollute the dashboard request log
// (health checks, static assets, the login page). Only real API/proxy traffic
// is recorded.
var requestLogSkip = map[string]bool{
	"/health":      true,
	"/login":       true,
	"/favicon.ico": true,
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !requestLogSkip[path] && !strings.HasPrefix(path, "/static/") && !strings.HasPrefix(path, "/webfonts/") {
			go storage.AppendRequestLog(config.RequestLogEntry{
				Timestamp: utils.NowISO(),
				Method:    r.Method,
				Path:      path,
				IP:        r.RemoteAddr,
				UserAgent: utils.Truncate(r.UserAgent(), 200),
			})
		}
		next.ServeHTTP(w, r)
	})
}
