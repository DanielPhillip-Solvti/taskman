// Package api exposes config/repos/work over a small localhost-only HTTP
// API for the Chrome extension.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/solvti/taskman/internal/config"
	"github.com/solvti/taskman/internal/repos"
	"github.com/solvti/taskman/internal/work"
)

// Server wires the three internal packages to HTTP handlers.
type Server struct {
	cfg   *config.Store
	regis *repos.Registry
	mgr   *work.Manager
}

// New builds a Server.
func New(cfg *config.Store, regis *repos.Registry, mgr *work.Manager) *Server {
	return &Server{cfg: cfg, regis: regis, mgr: mgr}
}

// Routes returns the configured mux, ready to be served.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /config", s.handleGetConfig)
	mux.HandleFunc("POST /config/harness", s.handleSetHarness)
	mux.HandleFunc("POST /config/model", s.handleSetModel)
	mux.HandleFunc("GET /repos", s.handleListRepos)
	mux.HandleFunc("POST /repos/fetch", s.handleFetchRepo)
	mux.HandleFunc("POST /tasks/{number}/refine", s.handleRefine)
	mux.HandleFunc("POST /tasks/{number}/implement", s.handleImplement)
	mux.HandleFunc("GET /tasks/{number}/output", s.handleOutput)
	mux.HandleFunc("POST /tasks/{number}/interrupt", s.handleInterrupt)
	return withCORS(withLogging(mux))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single-operator localhost tool: the extension's service worker is
		// the only intended caller, but browsers still enforce CORS on
		// cross-origin fetches from a page's extension context, so we
		// answer permissively rather than trying to enumerate every
		// possible ticketing-Odoo origin.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	settings, err := s.cfg.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	models, err := config.ModelList(settings.Harness)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"harness":      settings.Harness,
		"model":        settings.Model,
		"harness_list": config.HarnessList(),
		"model_list":   models,
	})
}

type harnessReq struct {
	Harness string `json:"harness"`
}

func (s *Server) handleSetHarness(w http.ResponseWriter, r *http.Request) {
	var req harnessReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings, err := s.cfg.SetHarness(req.Harness)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type modelReq struct {
	Model string `json:"model"`
}

func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	var req modelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings, err := s.cfg.SetModel(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	list, err := s.regis.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type fetchRepoReq struct {
	URL         string `json:"url"`
	OdooVersion string `json:"odoo_version"`
}

func (s *Server) handleFetchRepo(w http.ResponseWriter, r *http.Request) {
	var req fetchRepoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.URL == "" || req.OdooVersion == "" {
		writeError(w, http.StatusBadRequest, errors.New("api: url and odoo_version are required"))
		return
	}
	result, err := s.regis.FetchRepo(req.URL, req.OdooVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type taskReq struct {
	RepoName    string `json:"repo_name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func pathNumber(r *http.Request) (int, error) {
	raw := r.PathValue("number")
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("api: task number in path must be an integer, got " + raw)
	}
	return n, nil
}

func (s *Server) handleRefine(w http.ResponseWriter, r *http.Request) {
	s.queue(w, r, s.mgr.QueueTaskRefinement)
}

func (s *Server) handleImplement(w http.ResponseWriter, r *http.Request) {
	s.queue(w, r, s.mgr.QueueTask)
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request, fn func(int, string, string, string) (*work.Task, error)) {
	number, err := pathNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req taskReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := fn(number, req.RepoName, req.Title, req.Description)
	if err != nil {
		var busy work.ErrRepoBusy
		if errors.As(err, &busy) {
			writeError(w, http.StatusConflict, err)
			return
		}
		var unknown work.ErrUnknownRepo
		if errors.As(err, &unknown) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) handleOutput(w http.ResponseWriter, r *http.Request) {
	number, err := pathNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.mgr.GetTaskOutput(number)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	number, err := pathNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.mgr.InterruptTask(number); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
