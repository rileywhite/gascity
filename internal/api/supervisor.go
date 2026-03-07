package api

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// CityInfo describes a managed city for the /v0/cities endpoint.
type CityInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Running bool   `json:"running"`
}

// CityResolver provides city lookup for the supervisor API router.
type CityResolver interface {
	// ListCities returns all managed cities with status info.
	ListCities() []CityInfo
	// CityState returns the State for a named city, or nil if not found/not running.
	CityState(name string) State
}

// SupervisorMux routes API requests to per-city handlers with
// city-namespaced URL paths. It handles:
//   - GET /v0/cities — list managed cities
//   - GET /v0/city/{name} — city detail (status)
//   - /v0/city/{name}/... — route to a specific city's API
//   - GET /health — supervisor health
//   - /v0/... (bare) — backward compat, routes to first running city
type SupervisorMux struct {
	resolver  CityResolver
	readOnly  bool
	version   string
	startedAt time.Time
	server    *http.Server
}

// NewSupervisorMux creates a SupervisorMux that routes requests to cities
// resolved by the given CityResolver.
func NewSupervisorMux(resolver CityResolver, readOnly bool, version string, startedAt time.Time) *SupervisorMux {
	return &SupervisorMux{
		resolver:  resolver,
		readOnly:  readOnly,
		version:   version,
		startedAt: startedAt,
	}
}

// Handler returns an http.Handler with the standard middleware chain applied.
func (sm *SupervisorMux) Handler() http.Handler {
	inner := withCSRFCheck(http.HandlerFunc(sm.ServeHTTP))
	if sm.readOnly {
		inner = withReadOnly(inner)
	}
	return withLogging(withRecovery(withCORS(inner)))
}

// Serve accepts connections on lis. Blocks until stopped.
func (sm *SupervisorMux) Serve(lis net.Listener) error {
	sm.server = &http.Server{Handler: sm.Handler()}
	return sm.server.Serve(lis)
}

// Shutdown gracefully shuts down the server.
func (sm *SupervisorMux) Shutdown(ctx context.Context) error {
	if sm.server == nil {
		return nil
	}
	return sm.server.Shutdown(ctx)
}

// ServeHTTP dispatches requests to the appropriate city or supervisor-level handler.
func (sm *SupervisorMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Supervisor-level endpoints.
	if path == "/v0/cities" && r.Method == http.MethodGet {
		sm.handleCities(w, r)
		return
	}
	if path == "/health" && r.Method == http.MethodGet {
		sm.handleHealth(w, r)
		return
	}

	// City-namespaced: /v0/city/{name} or /v0/city/{name}/...
	if strings.HasPrefix(path, "/v0/city/") {
		rest := strings.TrimPrefix(path, "/v0/city/")
		idx := strings.IndexByte(rest, '/')
		var cityName, suffix string
		if idx < 0 {
			cityName = rest
			suffix = ""
		} else {
			cityName = rest[:idx]
			suffix = rest[idx:] // e.g. "/agents"
		}
		if cityName == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "city name required in URL")
			return
		}
		// /v0/city/{name} with no suffix → city detail (status)
		if suffix == "" {
			suffix = "/status"
		}
		sm.serveCityRequest(w, r, cityName, "/v0"+suffix)
		return
	}

	// Bare /v0/... — backward compat, route to first running city.
	if strings.HasPrefix(path, "/v0/") || path == "/v0" {
		cities := sm.resolver.ListCities()
		sort.Slice(cities, func(i, j int) bool { return cities[i].Name < cities[j].Name })
		for _, c := range cities {
			if c.Running {
				sm.serveCityRequest(w, r, c.Name, path)
				return
			}
		}
		writeError(w, http.StatusServiceUnavailable, "no_cities", "no cities running")
		return
	}

	http.NotFound(w, r)
}

// serveCityRequest resolves a city's State and dispatches to a per-city Server.
func (sm *SupervisorMux) serveCityRequest(w http.ResponseWriter, r *http.Request, cityName, path string) {
	state := sm.resolver.CityState(cityName)
	if state == nil {
		writeError(w, http.StatusNotFound, "not_found", "city not found or not running: "+cityName)
		return
	}

	// Create a per-city Server and dispatch through its mux directly.
	// Middleware is already applied at the SupervisorMux level.
	srv := &Server{state: state, mux: http.NewServeMux()}
	srv.registerRoutes()

	// Rewrite the request path to the per-city route.
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	r2.URL.RawPath = ""
	srv.mux.ServeHTTP(w, r2)
}

func (sm *SupervisorMux) handleCities(w http.ResponseWriter, _ *http.Request) {
	cities := sm.resolver.ListCities()
	sort.Slice(cities, func(i, j int) bool { return cities[i].Name < cities[j].Name })
	writeJSON(w, http.StatusOK, listResponse{Items: cities, Total: len(cities)})
}

func (sm *SupervisorMux) handleHealth(w http.ResponseWriter, _ *http.Request) {
	cities := sm.resolver.ListCities()
	var running int
	for _, c := range cities {
		if c.Running {
			running++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        sm.version,
		"uptime_sec":     int(time.Since(sm.startedAt).Seconds()),
		"cities_total":   len(cities),
		"cities_running": running,
	})
}
