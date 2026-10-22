package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/local/dbbroker/internal/broker"
)

//go:embed static/*
var staticContent embed.FS

type Broker interface {
	Seed(ctx context.Context, repository, name string, backup io.Reader) (string, error)
	Unseed(ctx context.Context, repository, name string) error
	Acquire(ctx context.Context, repository, worktree, template string) (broker.Assignment, error)
	Heartbeat(ctx context.Context, repository, worktree string) error
	Release(ctx context.Context, repository, worktree string) error
	List(ctx context.Context) ([]broker.Assignment, error)
}

type Server struct {
	broker Broker
	token  string
	log    *slog.Logger
}

func New(b Broker, token string, log *slog.Logger) http.Handler {
	s := &Server{broker: b, token: token, log: log}
	mux := http.NewServeMux()
	mux.Handle("GET /", staticHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/templates/{name}", s.seed)
	mux.HandleFunc("DELETE /v1/templates/{name}", s.unseed)
	mux.HandleFunc("POST /v1/acquire", s.acquire)
	mux.HandleFunc("POST /v1/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /v1/release", s.release)
	mux.HandleFunc("GET /v1/assignments", s.list)
	return recoverer(log, auth(token, mux))
}

type request struct {
	Repository string `json:"repository"`
	Worktree   string `json:"worktree"`
	Template   string `json:"template"`
}

func (s *Server) seed(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	repository := strings.TrimSpace(r.Header.Get("X-DBBroker-Repository"))
	if strings.TrimSpace(name) == "" || repository == "" {
		problem(w, http.StatusBadRequest, "template name and X-DBBroker-Repository are required")
		return
	}
	db, err := s.broker.Seed(r.Context(), repository, name, r.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": name, "database_name": db})
}
func (s *Server) unseed(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	repository := strings.TrimSpace(r.Header.Get("X-DBBroker-Repository"))
	if strings.TrimSpace(name) == "" || repository == "" {
		problem(w, http.StatusBadRequest, "template name and X-DBBroker-Repository are required")
		return
	}
	if err := s.broker.Unseed(r.Context(), repository, name); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) acquire(w http.ResponseWriter, r *http.Request) {
	var q request
	if !decode(w, r, &q) {
		return
	}
	a, err := s.broker.Acquire(r.Context(), q.Repository, q.Worktree, q.Template)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var q request
	if !decode(w, r, &q) {
		return
	}
	if err := s.broker.Heartbeat(r.Context(), q.Repository, q.Worktree); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	var q request
	if !decode(w, r, &q) {
		return
	}
	if err := s.broker.Release(r.Context(), q.Repository, q.Worktree); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	xs, err := s.broker.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"assignments": xs})
}
func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "error", err)
	if errors.Is(err, broker.ErrNotFound) {
		problem(w, http.StatusNotFound, err.Error())
		return
	}
	problem(w, http.StatusBadRequest, err.Error())
}

func auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			problem(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic", "value", v)
				problem(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		problem(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "status": status})
}

func staticHandler() http.Handler {
	root, err := fs.Sub(staticContent, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(root))
}
