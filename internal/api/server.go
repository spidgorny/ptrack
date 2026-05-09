package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"ptrack/internal/daemon"
	"ptrack/internal/daemonctl"
	"ptrack/internal/model"
)

type Server struct {
	service *daemon.Service
	logger  *slog.Logger
	mux     *http.ServeMux
}

func NewServer(service *daemon.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		service: service,
		logger:  logger,
		mux:     http.NewServeMux(),
	}
	server.routes()
	return server
}

func NewMux(service *daemon.Service, logger *slog.Logger) *http.ServeMux {
	return NewServer(service, logger).Mux()
}

func (s *Server) Mux() *http.ServeMux {
	return s.mux
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
