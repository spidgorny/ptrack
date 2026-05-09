package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"ptrack/internal/api"
	"ptrack/internal/daemon"
	"ptrack/internal/daemonctl"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cwd, err := os.Getwd()
	if err != nil {
		logger.Error("resolve working directory", "err", err)
		os.Exit(1)
	}
	executable, err := os.Executable()
	if err != nil {
		logger.Error("resolve executable", "err", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] != "serve" {
		exitCode, err := runTrackedInvocation(logger, executable, cwd, os.Args[0], os.Args[1:])
		if err != nil {
			logger.Error("track invocation", "err", err)
			os.Exit(1)
		}
		os.Exit(exitCode)
	}

	if err := serve(logger, cwd); err != nil {
		logger.Error("serve daemon", "err", err)
		os.Exit(1)
	}
}

func runTrackedInvocation(logger *slog.Logger, executable, cwd, wrapper string, args []string) (int, error) {
	paths, err := daemonctl.ResolvePaths(cwd)
	if err != nil {
		return 0, err
	}
	state, err := daemonctl.EnsureDaemon(context.Background(), paths, executable, logger)
	if err != nil {
		return 0, err
	}
	client := daemonctl.NewClient(state)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	startedAt := time.Now().UTC()
	stdoutWriter := &remoteLogWriter{client: client, terminal: os.Stdout, stream: "stdout"}
	stderrWriter := &remoteLogWriter{client: client, terminal: os.Stderr, stream: "stderr"}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	detail, err := client.RegisterInvocation(context.Background(), daemonctl.RegisterRequest{
		Wrapper:   wrapper,
		Args:      args,
		CWD:       cwd,
		PID:       cmd.Process.Pid,
		StartedAt: startedAt,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, err
	}
	stdoutWriter.Bind(detail.ID)
	stderrWriter.Bind(detail.ID)

	waitErr := cmd.Wait()
	exitCode, signal, outcome := exitMetadata(waitErr)
	finishedAt := time.Now().UTC()
	if err := client.CompleteInvocation(context.Background(), detail.ID, daemonctl.CompleteRequest{
		FinishedAt: finishedAt,
		ExitCode:   exitCode,
		Signal:     signal,
		Outcome:    outcome,
	}); err != nil {
		logger.Warn("failed to finalize daemon process state", "process_id", detail.ID, "err", err)
	}

	switch {
	case exitCode != nil:
		return *exitCode, nil
	case signal != nil:
		logger.Error("tracked invocation terminated by signal", "signal", *signal)
		return 1, nil
	default:
		return 0, nil
	}
}

func serve(logger *slog.Logger, cwd string) error {
	paths, err := resolveServePaths(cwd)
	if err != nil {
		return err
	}

	httpAddress := os.Getenv("PTRACK_HTTP_ADDRESS")
	if httpAddress == "" {
		httpAddress = "127.0.0.1:7777"
	}
	listener, err := net.Listen("tcp", httpAddress)
	if err != nil {
		return err
	}
	defer listener.Close()

	service := daemon.NewService(daemon.Config{
		Version:        "0.1.0-prototype",
		SocketPath:     paths.SocketPath,
		HTTPAddress:    listener.Addr().String(),
		WebSocketPath:  "/api/v1/ws",
		LogBufferBytes: 64 * 1024,
	})
	server := api.NewServer(service, logger)
	httpServer := &http.Server{
		Handler:           server.Mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := paths.WriteState(daemonctl.State{
		PID:         os.Getpid(),
		HTTPAddress: listener.Addr().String(),
		StartedAt:   time.Now().UTC(),
	}); err != nil {
		return err
	}
	defer func() {
		_ = paths.RemoveState(os.Getpid())
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("ptrack daemon serving", "addr", listener.Addr().String(), "runtime_dir", paths.RuntimeDir)
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func resolveServePaths(cwd string) (daemonctl.Paths, error) {
	if runtimeDir := os.Getenv("PTRACK_RUNTIME_DIR"); runtimeDir != "" {
		return daemonctl.PathsFromRuntimeDir(runtimeDir), nil
	}
	return daemonctl.ResolvePaths(cwd)
}

type remoteLogWriter struct {
	mu        sync.RWMutex
	client    *daemonctl.Client
	terminal  *os.File
	pending   []string
	processID string
	stream    string
}

func (w *remoteLogWriter) Bind(processID string) {
	w.mu.Lock()
	w.processID = processID
	pending := append([]string(nil), w.pending...)
	w.pending = nil
	w.mu.Unlock()

	for _, chunk := range pending {
		w.append(chunk)
	}
}

func (w *remoteLogWriter) append(text string) {
	if text == "" {
		return
	}
	w.mu.RLock()
	processID := w.processID
	w.mu.RUnlock()
	if processID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = w.client.AppendLog(ctx, processID, daemonctl.AppendLogRequest{
		Stream: w.stream,
		Text:   text,
	})
}

func (w *remoteLogWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := w.terminal.Write(p); err != nil {
		return 0, err
	}
	text := string(p)
	w.mu.Lock()
	if w.processID == "" {
		w.pending = append(w.pending, text)
		w.mu.Unlock()
		return len(p), nil
	}
	w.mu.Unlock()
	w.append(text)
	return len(p), nil
}

func exitMetadata(err error) (*int, *string, string) {
	if err == nil {
		exitCode := 0
		return &exitCode, nil, "succeeded"
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				signal := status.Signal().String()
				return nil, &signal, "signaled"
			}
			exitCode := status.ExitStatus()
			if exitCode == 0 {
				return &exitCode, nil, "succeeded"
			}
			return &exitCode, nil, "failed"
		}
	}
	return nil, nil, "failed"
}
