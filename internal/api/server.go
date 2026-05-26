package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"ptrack/internal/daemon"
	"ptrack/internal/daemonctl"
	"ptrack/internal/model"
)

type Config struct {
	WebDir string
}

type Server struct {
	service *daemon.Service
	logger  *slog.Logger
	mux     *http.ServeMux
	handler http.Handler
	webFS   fs.FS
	web     http.Handler
}

func NewServer(service *daemon.Service, logger *slog.Logger, cfg Config) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		service: service,
		logger:  logger,
		mux:     http.NewServeMux(),
	}
	if cfg.WebDir != "" {
		server.webFS = os.DirFS(cfg.WebDir)
		server.web = http.FileServerFS(server.webFS)
	}
	server.routes()
	server.handler = http.HandlerFunc(server.serveHTTP)
	return server
}

func NewMux(service *daemon.Service, logger *slog.Logger, cfg Config) http.Handler {
	return NewServer(service, logger, cfg).Mux()
}

func (s *Server) Mux() http.Handler {
	return s.handler
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/processes", s.handleProcessList)
	s.mux.HandleFunc("GET /api/v1/processes/{id}", s.handleProcessDetail)
	s.mux.HandleFunc("GET /api/v1/processes/{id}/logs", s.handleProcessLogs)
	s.mux.HandleFunc("GET /api/v1/processes/{id}/children", s.handleProcessChildren)
	s.mux.HandleFunc("GET /api/v1/ws", s.handleWebSocket)
	s.mux.HandleFunc("POST /internal/processes", s.handleInternalRegister)
	s.mux.HandleFunc("POST /internal/processes/{id}/logs", s.handleInternalLogAppend)
	s.mux.HandleFunc("POST /internal/processes/{id}/complete", s.handleInternalComplete)
	if s.web != nil {
		s.mux.Handle("GET /", http.HandlerFunc(s.handleWeb))
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, s.normalizeRequest(r))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Health())
}

func (s *Server) handleProcessList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "all", "running", "exited":
	default:
		writeError(w, http.StatusBadRequest, "invalid_query", "status must be one of all, running, exited", map[string]any{"status": status})
		return
	}

	limit, err := parseBoundedInt(r, "limit", 100, 1, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), map[string]any{"parameter": "limit"})
		return
	}

	response, err := s.service.ListProcesses(status, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		s.logger.Error("list processes", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list tracked processes", nil)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleProcessDetail(w http.ResponseWriter, r *http.Request) {
	response, err := s.service.ProcessDetail(r.PathValue("id"))
	if err != nil {
		s.writeProcessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleProcessLogs(w http.ResponseWriter, r *http.Request) {
	after, err := parseBoundedInt64(r, "after", 0, 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), map[string]any{"parameter": "after"})
		return
	}
	limit, err := parseBoundedInt(r, "limit", 500, 1, 5000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), map[string]any{"parameter": "limit"})
		return
	}

	response, err := s.service.ProcessLogs(r.PathValue("id"), after, limit)
	if err != nil {
		s.writeProcessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleProcessChildren(w http.ResponseWriter, r *http.Request) {
	response, err := s.service.ProcessChildren(r.PathValue("id"))
	if err != nil {
		s.writeProcessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	requestPath := path.Clean("/" + r.URL.Path)
	if isReservedPath(requestPath) {
		http.NotFound(w, r)
		return
	}

	assetPath, found, err := s.resolveWebPath(requestPath)
	if err != nil {
		s.logger.Error("stat web asset", "path", requestPath, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if assetPath == "index.html" {
		s.serveIndexHTML(w, r, requestPath)
		return
	}
	if found {
		s.serveWebFile(w, r, "/"+assetPath)
		return
	}
	if path.Ext(strings.TrimPrefix(requestPath, "/")) != "" {
		http.NotFound(w, r)
		return
	}
	s.serveIndexHTML(w, r, requestPath)
}

func (s *Server) serveWebFile(w http.ResponseWriter, r *http.Request, requestPath string) {
	http.ServeFileFS(w, r, s.webFS, strings.TrimPrefix(requestPath, "/"))
}

func (s *Server) serveIndexHTML(w http.ResponseWriter, r *http.Request, requestPath string) {
	indexHTML, err := fs.ReadFile(s.webFS, "index.html")
	if err != nil {
		s.logger.Error("read index html", "path", requestPath, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, injectBaseHref(string(indexHTML), webBaseHref(requestPath)))
}

func (s *Server) writeProcessError(w http.ResponseWriter, err error) {
	var notFound daemon.ProcessNotFoundError
	if errors.As(err, &notFound) {
		writeError(w, http.StatusNotFound, "process_not_found", notFound.Error(), map[string]any{"id": notFound.ID})
		return
	}

	s.logger.Error("process request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "failed to handle process request", nil)
}

func (s *Server) handleInternalRegister(w http.ResponseWriter, r *http.Request) {
	var request daemonctl.RegisterRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	response, err := s.service.RegisterExternalInvocation(request.Wrapper, request.Args, request.CWD, request.PID, request.StartedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handleInternalLogAppend(w http.ResponseWriter, r *http.Request) {
	var request daemonctl.AppendLogRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if request.Stream == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "stream is required", nil)
		return
	}
	if err := s.service.AppendProcessLog(r.PathValue("id"), request.Stream, request.Text); err != nil {
		s.writeProcessError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleInternalComplete(w http.ResponseWriter, r *http.Request) {
	var request daemonctl.CompleteRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if err := s.service.CompleteExternalInvocation(r.PathValue("id"), request.FinishedAt, request.ExitCode, request.Signal, request.Outcome); err != nil {
		s.writeProcessError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func parseBoundedInt(r *http.Request, key string, defaultValue, minValue, maxValue int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minValue, maxValue)
	}
	return value, nil
}

func parseBoundedInt64(r *http.Request, key string, defaultValue, minValue int64) (int64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if value < minValue {
		return 0, fmt.Errorf("%s must be greater than or equal to %d", key, minValue)
	}
	return value, nil
}

func isReservedPath(requestPath string) bool {
	return requestPath == "/api" ||
		strings.HasPrefix(requestPath, "/api/") ||
		requestPath == "/internal" ||
		strings.HasPrefix(requestPath, "/internal/")
}

func (s *Server) normalizeRequest(r *http.Request) *http.Request {
	normalizedPath := normalizeReservedPath(path.Clean("/" + r.URL.Path))
	if normalizedPath == r.URL.Path {
		return r
	}

	clone := r.Clone(r.Context())
	cloneURL := *clone.URL
	clone.URL = &cloneURL
	clone.URL.Path = normalizedPath
	clone.RequestURI = normalizedPath
	return clone
}

func normalizeReservedPath(requestPath string) string {
	for _, marker := range []string{"/api/", "/internal/"} {
		if idx := strings.Index(requestPath, marker); idx > 0 {
			return requestPath[idx:]
		}
	}
	for _, marker := range []string{"/api", "/internal"} {
		if strings.HasSuffix(requestPath, marker) && len(requestPath) > len(marker) {
			return marker
		}
	}
	return requestPath
}

func (s *Server) resolveWebPath(requestPath string) (string, bool, error) {
	if s.webFS == nil {
		return "", false, nil
	}

	trimmedPath := strings.TrimPrefix(requestPath, "/")
	if trimmedPath == "" {
		return "index.html", true, nil
	}

	for _, candidate := range suffixCandidates(trimmedPath) {
		info, err := fs.Stat(s.webFS, candidate)
		switch {
		case err == nil && !info.IsDir():
			return candidate, true, nil
		case err == nil:
			continue
		case errors.Is(err, fs.ErrNotExist):
			continue
		default:
			return "", false, err
		}
	}

	return "", false, nil
}

func suffixCandidates(requestPath string) []string {
	candidates := []string{requestPath}
	for trimmed := requestPath; ; {
		slash := strings.Index(trimmed, "/")
		if slash < 0 {
			break
		}
		trimmed = trimmed[slash+1:]
		candidates = append(candidates, trimmed)
	}
	return candidates
}

func webBaseHref(requestPath string) string {
	cleanedPath := path.Clean("/" + requestPath)
	if cleanedPath == "/" || cleanedPath == "/index.html" {
		return "/"
	}
	if strings.HasSuffix(cleanedPath, "/index.html") {
		cleanedPath = path.Dir(cleanedPath)
	}
	return strings.TrimSuffix(cleanedPath, "/") + "/"
}

func injectBaseHref(indexHTML, baseHref string) string {
	lowerHTML := strings.ToLower(indexHTML)
	if strings.Contains(lowerHTML, "<base ") {
		return indexHTML
	}

	headIndex := strings.Index(lowerHTML, "<head>")
	if headIndex < 0 {
		return indexHTML
	}

	insertAt := headIndex + len("<head>")
	baseTag := "\n    <base href=\"" + html.EscapeString(baseHref) + "\">"
	return indexHTML[:insertAt] + baseTag + indexHTML[insertAt:]
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, model.ErrorResponse{
		Error: model.APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func decodeJSONBody(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	switch value := target.(type) {
	case *daemonctl.RegisterRequest:
		value.StartedAt = value.StartedAt.UTC()
	case *daemonctl.CompleteRequest:
		if !value.FinishedAt.IsZero() {
			value.FinishedAt = value.FinishedAt.UTC()
		}
	}
	return nil
}
