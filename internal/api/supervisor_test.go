package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeCityResolver implements CityResolver for testing.
type fakeCityResolver struct {
	cities map[string]*fakeState // keyed by city name
}

func (f *fakeCityResolver) ListCities() []CityInfo {
	var out []CityInfo
	for name := range f.cities {
		s := f.cities[name]
		out = append(out, CityInfo{
			Name:    name,
			Path:    s.CityPath(),
			Running: true,
		})
	}
	return out
}

func (f *fakeCityResolver) CityState(name string) State {
	if s, ok := f.cities[name]; ok {
		return s
	}
	return nil
}

func newTestSupervisorMux(t *testing.T, cities map[string]*fakeState) *SupervisorMux {
	t.Helper()
	resolver := &fakeCityResolver{cities: cities}
	return NewSupervisorMux(resolver, false, "test", time.Now())
}

func TestSupervisorCitiesList(t *testing.T) {
	s1 := newFakeState(t)
	s1.cityName = "alpha"
	s2 := newFakeState(t)
	s2.cityName = "beta"

	sm := newTestSupervisorMux(t, map[string]*fakeState{
		"alpha": s1,
		"beta":  s2,
	})

	req := httptest.NewRequest("GET", "/v0/cities", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Items []CityInfo `json:"items"`
		Total int        `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Total)
	}
	// Sorted by name.
	if resp.Items[0].Name != "alpha" || resp.Items[1].Name != "beta" {
		t.Errorf("items = %v, want alpha then beta", resp.Items)
	}
}

func TestSupervisorCityNamespacedRoute(t *testing.T) {
	s := newFakeState(t)
	s.cityName = "bright-lights"

	sm := newTestSupervisorMux(t, map[string]*fakeState{
		"bright-lights": s,
	})

	req := httptest.NewRequest("GET", "/v0/city/bright-lights/agents", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Should return the agent list from the city's state.
	var resp struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1 (one agent in fakeState)", resp.Total)
	}
}

func TestSupervisorCityDetail(t *testing.T) {
	s := newFakeState(t)
	s.cityName = "bright-lights"

	sm := newTestSupervisorMux(t, map[string]*fakeState{
		"bright-lights": s,
	})

	// /v0/city/{name} with no suffix should return status.
	req := httptest.NewRequest("GET", "/v0/city/bright-lights", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "bright-lights" {
		t.Errorf("Name = %q, want %q", resp.Name, "bright-lights")
	}
}

func TestSupervisorCityNotFound(t *testing.T) {
	sm := newTestSupervisorMux(t, map[string]*fakeState{})

	req := httptest.NewRequest("GET", "/v0/city/unknown/agents", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSupervisorBarePathSingleCity(t *testing.T) {
	s := newFakeState(t)
	s.cityName = "sole-city"

	sm := newTestSupervisorMux(t, map[string]*fakeState{
		"sole-city": s,
	})

	// Bare /v0/status should route to the sole running city.
	req := httptest.NewRequest("GET", "/v0/status", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "sole-city" {
		t.Errorf("Name = %q, want %q", resp.Name, "sole-city")
	}
}

func TestSupervisorBarePathNoCities(t *testing.T) {
	sm := newTestSupervisorMux(t, map[string]*fakeState{})

	req := httptest.NewRequest("GET", "/v0/status", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSupervisorBarePathMultipleCities(t *testing.T) {
	s1 := newFakeState(t)
	s1.cityName = "alpha"
	s2 := newFakeState(t)
	s2.cityName = "beta"

	sm := newTestSupervisorMux(t, map[string]*fakeState{
		"alpha": s1,
		"beta":  s2,
	})

	// Bare /v0/status with multiple cities should route to first alphabetically.
	req := httptest.NewRequest("GET", "/v0/status", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "alpha" {
		t.Errorf("Name = %q, want %q (first alphabetically)", resp.Name, "alpha")
	}
}

func TestSupervisorHealth(t *testing.T) {
	s := newFakeState(t)
	sm := newTestSupervisorMux(t, map[string]*fakeState{
		"test-city": s,
	})

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want %q", resp["status"], "ok")
	}
	if resp["cities_total"] != float64(1) {
		t.Errorf("cities_total = %v, want 1", resp["cities_total"])
	}
	if resp["cities_running"] != float64(1) {
		t.Errorf("cities_running = %v, want 1", resp["cities_running"])
	}
}

func TestSupervisorEmptyCityName(t *testing.T) {
	sm := newTestSupervisorMux(t, map[string]*fakeState{})

	req := httptest.NewRequest("GET", "/v0/city/", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
