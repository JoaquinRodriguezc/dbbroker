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
