package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/local/dbbroker/internal/broker"
)

type fakeBroker struct {
	acquired broker.Assignment
	err      error
}

func (f *fakeBroker) Seed(context.Context, string, string, io.Reader) (string, error) {
	return "dbb_tpl_main", f.err
}
func (f *fakeBroker) Unseed(context.Context, string, string) error { return f.err }
func (f *fakeBroker) Acquire(context.Context, string, string, string) (broker.Assignment, error) {
	return f.acquired, f.err
}
func (f *fakeBroker) Heartbeat(context.Context, string, string) error { return f.err }
func (f *fakeBroker) Release(context.Context, string, string) error   { return f.err }
func (f *fakeBroker) List(context.Context) ([]broker.Assignment, error) {
	return []broker.Assignment{f.acquired}, f.err
}

func handler(f *fakeBroker) http.Handler {
	return New(f, "secret", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestProtectedRouteRequiresAuthentication(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/assignments", nil)
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAcquireReturnsAssignment(t *testing.T) {
	f := &fakeBroker{acquired: broker.Assignment{DatabaseName: "dbb_wt_123", DatabaseURL: "postgres://localhost/dbb_wt_123"}}
	r := httptest.NewRequest(http.MethodPost, "/v1/acquire", strings.NewReader(`{"repository":"repo","worktree":"wt","template":"main"}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler(f).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"database_name":"dbb_wt_123"`)) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/acquire", strings.NewReader(`{"repository":"repo","worktree":"wt","template":"main","extra":true}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestNotFoundIsMapped(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/release", strings.NewReader(`{"repository":"repo","worktree":"wt"}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler(&fakeBroker{err: broker.ErrNotFound}).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestSeedRequiresRepositoryHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/templates/main", strings.NewReader("SELECT 1;"))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestSeedAcceptsRepositoryHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/templates/main", strings.NewReader("SELECT 1;"))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-DBBroker-Repository", "/repo/.git")
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestDashboardIsEmbedded(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	New(&fakeBroker{}, "", slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Bases activas")) {
		t.Fatalf("dashboard HTML missing: %s", w.Body.String())
	}
}

func TestDashboardAssetsAreEmbedded(t *testing.T) {
	for _, path := range []string{"/styles.css", "/app.js"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		New(&fakeBroker{}, "", slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, w.Code)
		}
	}
}

func unseedRequest(repository string) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/v1/templates/main", nil)
	r.Header.Set("Authorization", "Bearer secret")
	if repository != "" {
		r.Header.Set("X-DBBroker-Repository", repository)
	}
	return r
}

func TestUnseedRequiresRepositoryHeader(t *testing.T) {
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, unseedRequest(""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestUnseedReturnsNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	handler(&fakeBroker{}).ServeHTTP(w, unseedRequest("/repo/.git"))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestUnseedMapsMissingTemplate(t *testing.T) {
	w := httptest.NewRecorder()
	handler(&fakeBroker{err: broker.ErrNotFound}).ServeHTTP(w, unseedRequest("/repo/.git"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}
