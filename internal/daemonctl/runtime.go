package daemonctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	runtimeDirName  = ".ptrack"
	stateFileName   = "daemon.json"
	lockFileName    = "daemon.lock"
	logFileName     = "daemon.log"
	defaultHTTPAddr = "127.0.0.1:7777"
	autoHTTPAddr    = "127.0.0.1:0"
)

type Paths struct {
	WorkspaceRoot string
	RuntimeDir    string
	StateFile     string
	LockFile      string
	LogFile       string
	SocketPath    string
}

type State struct {
	PID         int       `json:"pid"`
	HTTPAddress string    `json:"http_address"`
	StartedAt   time.Time `json:"started_at"`
}

func ResolvePaths(startDir string) (Paths, error) {
	root, err := discoverWorkspaceRoot(startDir)
	if err != nil {
		return Paths{}, err
	}
	runtimeDir := filepath.Join(root, runtimeDirName)
	return Paths{
		WorkspaceRoot: root,
		RuntimeDir:    runtimeDir,
		StateFile:     filepath.Join(runtimeDir, stateFileName),
		LockFile:      filepath.Join(runtimeDir, lockFileName),
		LogFile:       filepath.Join(runtimeDir, logFileName),
		SocketPath:    filepath.Join(runtimeDir, "daemon.sock"),
	}, nil
}

func PathsFromRuntimeDir(runtimeDir string) Paths {
	root := filepath.Dir(runtimeDir)
	return Paths{
		WorkspaceRoot: root,
		RuntimeDir:    runtimeDir,
		StateFile:     filepath.Join(runtimeDir, stateFileName),
		LockFile:      filepath.Join(runtimeDir, lockFileName),
		LogFile:       filepath.Join(runtimeDir, logFileName),
		SocketPath:    filepath.Join(runtimeDir, "daemon.sock"),
	}
}

func (p Paths) EnsureRuntimeDir() error {
	return os.MkdirAll(p.RuntimeDir, 0o755)
}

func (p Paths) ReadState() (State, error) {
	data, err := os.ReadFile(p.StateFile)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (p Paths) WriteState(state State) error {
	if err := p.EnsureRuntimeDir(); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(p.StateFile, data, 0o644)
}

func (p Paths) RemoveState(pid int) error {
	state, err := p.ReadState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if pid != 0 && state.PID != pid {
		return nil
	}
	if err := os.Remove(p.StateFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func EnsureDaemon(ctx context.Context, paths Paths, executable string, logger *slog.Logger) (State, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := paths.EnsureRuntimeDir(); err != nil {
		return State{}, err
	}
	if state, err := healthyState(paths); err == nil {
		return state, nil
	}

	release, err := acquireLock(paths.LockFile, 10*time.Second)
	if err != nil {
		return State{}, err
	}
	defer release()

	if state, err := healthyState(paths); err == nil {
		return state, nil
	}

	if err := startDaemon(executable, paths, logger); err != nil {
		return State{}, err
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if state, err := healthyState(paths); err == nil {
			return state, nil
		}
		if ctx.Err() != nil {
			return State{}, ctx.Err()
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	return State{}, fmt.Errorf("timed out waiting for daemon startup")
}

func BaseURL(state State) string {
	return "http://" + state.HTTPAddress
}

func Healthy(state State) bool {
	return state.PID > 0 && state.HTTPAddress != ""
}

func Ping(state State, timeout time.Duration) error {
	if !Healthy(state) {
		return errors.New("daemon state is incomplete")
	}
	if !processExists(state.PID) {
		return errors.New("daemon pid is not running")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(BaseURL(state) + "/api/v1/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected daemon health status %d", resp.StatusCode)
	}
	return nil
}

func healthyState(paths Paths) (State, error) {
	state, err := paths.ReadState()
	if err != nil {
		return State{}, err
	}
	if err := Ping(state, 750*time.Millisecond); err != nil {
		return State{}, err
	}
	return state, nil
}

func startDaemon(executable string, paths Paths, logger *slog.Logger) error {
	logFile, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(executable, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"PTRACK_RUNTIME_DIR="+paths.RuntimeDir,
		"PTRACK_HTTP_ADDRESS="+autoHTTPAddr,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	logger.Info("started ptrack daemon", "pid", cmd.Process.Pid, "runtime_dir", paths.RuntimeDir)
	return cmd.Process.Release()
}

func acquireLock(path string, staleAfter time.Duration) (func(), error) {
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() {
				_ = os.Remove(path)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, statErr := os.Stat(path)
		if statErr == nil && time.Since(info.ModTime()) > staleAfter {
			_ = os.Remove(path)
			continue
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func discoverWorkspaceRoot(startDir string) (string, error) {
	if startDir == "" {
		startDir = "."
	}
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		if hasWorkspaceMarker(current) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current, nil
		}
		current = parent
	}
}

func hasWorkspaceMarker(dir string) bool {
	for _, name := range []string{runtimeDirName, ".git", "go.mod", "package.json", "pnpm-workspace.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
