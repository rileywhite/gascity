package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func newSessionFakeState(t *testing.T) *fakeState {
	t.Helper()
	fs := newFakeState(t)
	fs.cityBeadStore = beads.NewMemStore()
	return fs
}

func createTestSession(t *testing.T, store beads.Store, sp *runtime.Fake, title string) session.Info {
	t.Helper()
	mgr := session.NewManager(store, sp)
	info, err := mgr.Create(context.Background(), "default", title, "echo test", "/tmp", "test", nil, session.ProviderResume{}, runtime.Config{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return info
}

func TestHandleSessionList(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	// Create two sessions.
	createTestSession(t, fs.cityBeadStore, fs.sp, "Session A")
	createTestSession(t, fs.cityBeadStore, fs.sp, "Session B")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/sessions", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var resp listResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("got total %d, want 2", resp.Total)
	}
}

func TestHandleSessionListFilterByState(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "To Suspend")
	createTestSession(t, fs.cityBeadStore, fs.sp, "Stay Active")

	// Suspend one.
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	// List only active.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/sessions?state=active", nil)
	srv.ServeHTTP(w, r)

	var resp listResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("got total %d, want 1 (only active)", resp.Total)
	}
}

func TestHandleSessionGet(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "My Session")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/session/"+info.ID, nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != info.ID {
		t.Errorf("got ID %q, want %q", resp.ID, info.ID)
	}
	if resp.Title != "My Session" {
		t.Errorf("got title %q, want %q", resp.Title, "My Session")
	}
	if resp.State != "active" {
		t.Errorf("got state %q, want %q", resp.State, "active")
	}
}

func TestHandleSessionGetNotFound(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/session/nonexistent", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionSuspend(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "To Suspend")

	w := httptest.NewRecorder()
	r := newPostRequest("/v0/session/"+info.ID+"/suspend", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the session is now suspended.
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	got, err := mgr.Get(info.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateSuspended {
		t.Errorf("got state %q, want %q", got.State, session.StateSuspended)
	}
}

func TestHandleSessionClose(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "To Close")

	w := httptest.NewRecorder()
	r := newPostRequest("/v0/session/"+info.ID+"/close", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Session should no longer appear in default listing (excludes closed).
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	sessions, err := mgr.List("", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("got %d sessions after close, want 0", len(sessions))
	}
}

func TestHandleSessionNoCityStore(t *testing.T) {
	fs := newFakeState(t) // no cityBeadStore set
	srv := New(fs)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/sessions", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleSessionWake(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Held Session")

	// Set hold metadata.
	_ = fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		"held_until":   "9999-12-31T23:59:59Z",
		"sleep_reason": "user-hold",
	})

	w := httptest.NewRecorder()
	r := newPostRequest("/v0/session/"+info.ID+"/wake", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify hold cleared.
	b, _ := fs.cityBeadStore.Get(info.ID)
	if b.Metadata["held_until"] != "" {
		t.Errorf("held_until should be cleared, got %q", b.Metadata["held_until"])
	}
	if b.Metadata["sleep_reason"] != "" {
		t.Errorf("sleep_reason should be cleared, got %q", b.Metadata["sleep_reason"])
	}
}

func TestHandleSessionWakeClosed(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Closed Session")
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	_ = mgr.Close(info.ID)

	w := httptest.NewRecorder()
	r := newPostRequest("/v0/session/"+info.ID+"/wake", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestHandleSessionGetByTemplateName(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Named Session")

	// Set template metadata on the bead so name resolution works.
	_ = fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		"template": "overseer",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/session/overseer", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != info.ID {
		t.Errorf("got ID %q, want %q", resp.ID, info.ID)
	}
}

func TestHandleSessionPatchTitle(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Original")

	body := `{"title":"Updated Title"}`
	req := httptest.NewRequest("PATCH", "/v0/session/"+info.ID, strings.NewReader(body))
	req.Header.Set("X-GC-Request", "true")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Title != "Updated Title" {
		t.Errorf("got title %q, want %q", resp.Title, "Updated Title")
	}
}

func TestHandleSessionPatchImmutableField(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Test")

	body := `{"template":"hacked"}`
	req := httptest.NewRequest("PATCH", "/v0/session/"+info.ID, strings.NewReader(body))
	req.Header.Set("X-GC-Request", "true")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestHandleSessionListIncludesReason(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Held")

	// Set sleep reason on bead.
	_ = fs.cityBeadStore.SetMetadataBatch(info.ID, map[string]string{
		"sleep_reason": "user-hold",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v0/sessions", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	// Parse into raw JSON to check for reason field.
	var raw struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(raw.Items))
	}
	var item sessionResponse
	if err := json.Unmarshal(raw.Items[0], &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	if item.Reason != "user-hold" {
		t.Errorf("got reason %q, want %q", item.Reason, "user-hold")
	}
}
