package daemon

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	ptrackexec "ptrack/internal/exec"
	"ptrack/internal/model"
	"ptrack/internal/observer"
	"ptrack/internal/tracker"
)

type Config struct {
	Version        string
	SocketPath     string
	HTTPAddress    string
	WebSocketPath  string
	LogBufferBytes int
}

type Service struct {
	cfg      Config
	store    *tracker.Store
	runner   ptrackexec.Runner
	observer observer.Snapshotter

	mu       sync.Mutex
	runtimes map[string]*runtimeHandle
}

type runtimeHandle struct {
	done     chan struct{}
	complete chan completionUpdate
}

type completionUpdate struct {
	finishedAt time.Time
	exitCode   *int
	signal     *string
	outcome    string
}

type ProcessNotFoundError struct {
	ID string
}

func (e ProcessNotFoundError) Error() string {
	return fmt.Sprintf("no tracked process exists for id %s", e.ID)
}

func NewService(cfg Config) *Service {
	if cfg.Version == "" {
		cfg.Version = "0.1.0-prototype"
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = ".ptrack/daemon.sock"
	}
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = "127.0.0.1:7777"
	}
	if cfg.WebSocketPath == "" {
		cfg.WebSocketPath = "/api/v1/ws"
	}
	if cfg.LogBufferBytes <= 0 {
		cfg.LogBufferBytes = 64 * 1024
	}

	return &Service{
		cfg:      cfg,
		store:    tracker.NewStore(time.Now().UTC(), cfg.Version, cfg.SocketPath, cfg.HTTPAddress, cfg.WebSocketPath, cfg.LogBufferBytes),
		runner:   ptrackexec.Runner{},
		observer: observer.PSSnapshotter{},
		runtimes: make(map[string]*runtimeHandle),
	}
}

func (s *Service) HTTPAddress() string {
	return s.cfg.HTTPAddress
}

func (s *Service) Health() model.DaemonInfo {
	return s.store.DaemonInfo(time.Now().UTC())
}

func (s *Service) SubscribeEvents(buffer int) (<-chan model.EventEnvelope, func()) {
	return s.store.Subscribe(buffer)
}

func (s *Service) ListProcesses(status string, limit int, cursor string) (model.ProcessListResponse, error) {
	items, nextCursor, trackedCount, runningCount, err := s.store.ListProcesses(status, limit, cursor, time.Now().UTC())
	if err != nil {
		return model.ProcessListResponse{}, err
	}
	return model.ProcessListResponse{
		Items:               items,
		Page:                model.Page{NextCursor: nextCursor},
		TrackedProcessCount: trackedCount,
		RunningProcessCount: runningCount,
	}, nil
}

func (s *Service) ProcessDetail(id string) (model.ProcessDetail, error) {
	detail, err := s.store.ProcessDetail(id, time.Now().UTC())
	if err != nil {
		return model.ProcessDetail{}, translateNotFound(id, err)
	}
	return detail, nil
}

func (s *Service) ProcessLogs(id string, after int64, limit int) (model.ProcessLogsResponse, error) {
	items, nextAfter, err := s.store.ProcessLogs(id, after, limit)
	if err != nil {
		return model.ProcessLogsResponse{}, translateNotFound(id, err)
	}
	return model.ProcessLogsResponse{
		Items: items,
		Page:  model.Page{NextAfter: nextAfter},
	}, nil
}

func (s *Service) ProcessChildren(id string) (model.ProcessChildrenResponse, error) {
	items, err := s.store.ProcessChildren(id)
	if err != nil {
		return model.ProcessChildrenResponse{}, translateNotFound(id, err)
	}
	return model.ProcessChildrenResponse{Items: items}, nil
}

func (s *Service) TrackInvocation(wrapper string, args []string, cwd string, stdout, stderr io.Writer) (model.ProcessDetail, error) {
	invocation, err := s.runner.PlanTrackedInvocation(wrapper, args)
	if err != nil {
		return model.ProcessDetail{}, err
	}

	cmd := exec.Command(invocation.Command, invocation.Argv[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return model.ProcessDetail{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return model.ProcessDetail{}, err
	}
	if err := cmd.Start(); err != nil {
		return model.ProcessDetail{}, err
	}

	now := time.Now().UTC()
	snapshot, err := s.observer.Snapshot(cmd.Process.Pid, nil, now)
	if err != nil {
		snapshot = observer.Snapshot{Children: []model.ChildProcess{}}
	}

	record := s.store.AddProcess(tracker.ProcessSpec{
		Command:     invocation.Command,
		Argv:        invocation.Argv,
		DisplayName: invocation.DisplayName,
		CWD:         cwd,
		Status:      "running",
		Outcome:     "unknown",
		StartedAt:   now,
		PID:         cmd.Process.Pid,
		PTY:         invocation.PTY,
		CPU:         initialCPU(snapshot.RootCPU, now),
		Source:      invocation.Source,
		Children:    snapshot.Children,
	})
	_, _ = s.store.AppendLog(record.ID, "system", s.runner.PrototypeNotice(invocation), now)

	handle := &runtimeHandle{done: make(chan struct{})}
	s.mu.Lock()
	s.runtimes[record.ID] = handle
	s.mu.Unlock()

	var copies sync.WaitGroup
	copies.Add(2)
	go s.captureOutput(record.ID, "stdout", stdoutPipe, stdout, &copies)
	go s.captureOutput(record.ID, "stderr", stderrPipe, stderr, &copies)
	go s.monitorProcess(record.ID, cmd, handle, &copies, snapshot.Children)

	return s.store.ProcessDetail(record.ID, time.Now().UTC())
}

func (s *Service) RegisterExternalInvocation(wrapper string, args []string, cwd string, pid int, startedAt time.Time) (model.ProcessDetail, error) {
	invocation, err := s.runner.PlanTrackedInvocation(wrapper, args)
	if err != nil {
		return model.ProcessDetail{}, err
	}
	if pid <= 0 {
		return model.ProcessDetail{}, errors.New("tracked process requires a pid")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	now := time.Now().UTC()
	snapshot, err := s.observer.Snapshot(pid, nil, now)
	if err != nil {
		snapshot = observer.Snapshot{Children: []model.ChildProcess{}}
	}

	record := s.store.AddProcess(tracker.ProcessSpec{
		Command:     invocation.Command,
		Argv:        invocation.Argv,
		DisplayName: invocation.DisplayName,
		CWD:         cwd,
		Status:      "running",
		Outcome:     "unknown",
		StartedAt:   startedAt,
		PID:         pid,
		PTY:         invocation.PTY,
		CPU:         initialCPU(snapshot.RootCPU, now),
		Source:      invocation.Source,
		Children:    snapshot.Children,
	})
	_, _ = s.store.AppendLog(record.ID, "system", s.runner.PrototypeNotice(invocation), now)

	handle := &runtimeHandle{
		done:     make(chan struct{}),
		complete: make(chan completionUpdate, 1),
	}
	s.mu.Lock()
	s.runtimes[record.ID] = handle
	s.mu.Unlock()

	go s.monitorExternalProcess(record.ID, pid, handle, snapshot.Children)
	return s.store.ProcessDetail(record.ID, time.Now().UTC())
}

func (s *Service) AppendProcessLog(id, streamName, text string) error {
	if _, err := s.store.AppendLog(id, streamName, text, time.Now().UTC()); err != nil {
		return translateNotFound(id, err)
	}
	return nil
}

func (s *Service) CompleteExternalInvocation(id string, finishedAt time.Time, exitCode *int, signal *string, outcome string) error {
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	if outcome == "" {
		outcome = "unknown"
	}

	s.mu.Lock()
	handle := s.runtimes[id]
	s.mu.Unlock()
	if handle == nil || handle.complete == nil {
		if err := s.store.SetStatus(id, "exited", outcome, &finishedAt, exitCode, signal, finishedAt); err != nil {
			return translateNotFound(id, err)
		}
		return nil
	}

	select {
	case handle.complete <- completionUpdate{finishedAt: finishedAt, exitCode: exitCode, signal: signal, outcome: outcome}:
	default:
		if err := s.store.SetStatus(id, "exited", outcome, &finishedAt, exitCode, signal, finishedAt); err != nil {
			return translateNotFound(id, err)
		}
	}
	return nil
}

func (s *Service) WaitForExit(id string) (model.ProcessDetail, error) {
	s.mu.Lock()
	handle := s.runtimes[id]
	s.mu.Unlock()
	if handle != nil {
		<-handle.done
	}
	return s.ProcessDetail(id)
}

func (s *Service) SeedPrototypeState(cwd string) error {
	if s.store.ProcessCount() > 0 {
		return nil
	}

	now := time.Now().UTC()
	running := s.store.AddProcess(tracker.ProcessSpec{
		Command:     "npm",
		Argv:        []string{"npm", "run", "dev"},
		DisplayName: "npm run dev",
		CWD:         cwd,
		Status:      "running",
		Outcome:     "unknown",
		StartedAt:   now.Add(-2 * time.Minute),
		PID:         18412,
		PTY: model.PTYInfo{
			Enabled: true,
			Cols:    120,
			Rows:    30,
		},
		CPU: model.CPUStats{
			LatestPercent:  18.5,
			AveragePercent: 12.4,
			PeakPercent:    31.2,
			SampleCount:    18,
			LastSampleAt:   model.TimePtr(now.Add(-29 * time.Second)),
		},
		Source: model.ProcessSource{
			Kind: "ptrack",
			Docker: &model.DockerInfo{
				InContainer: true,
				ContainerID: "9f3c2a4f1e7d",
			},
		},
		Children: []model.ChildProcess{{
			PID:        18421,
			PPID:       18412,
			Command:    "node",
			Argv:       []string{"node", "vite"},
			Status:     "running",
			StartedAt:  now.Add(-118 * time.Second),
			CPUPercent: 7.1,
		}},
	})
	_, _ = s.store.AppendLog(running.ID, "pty", "\u001b[32mready in 320ms\u001b[0m\n", now.Add(-28*time.Second))
	_, _ = s.store.AppendLog(running.ID, "pty", "Local: http://127.0.0.1:5173/\n", now.Add(-24*time.Second))

	finished := now.Add(-35 * time.Second)
	exitCode := 0
	exited := s.store.AddProcess(tracker.ProcessSpec{
		Command:     "go",
		Argv:        []string{"go", "test", "./..."},
		DisplayName: "go test ./...",
		CWD:         cwd,
		Status:      "exited",
		Outcome:     "succeeded",
		StartedAt:   now.Add(-4 * time.Minute),
		FinishedAt:  &finished,
		ExitCode:    &exitCode,
		PID:         18350,
		PTY:         model.PTYInfo{Enabled: false},
		CPU: model.CPUStats{
			LatestPercent:  0,
			AveragePercent: 5.2,
			PeakPercent:    11.4,
			SampleCount:    9,
			LastSampleAt:   model.TimePtr(finished),
		},
		Source: model.ProcessSource{Kind: "ptrack"},
	})
	_, _ = s.store.AppendLog(exited.ID, "stdout", "ok  \tptrack/internal/api\t0.021s\n", finished.Add(-4*time.Second))
	_, _ = s.store.AppendLog(exited.ID, "stdout", "ok  \tptrack/internal/tracker\t0.018s\n", finished.Add(-2*time.Second))
	return nil
}

func (s *Service) captureOutput(id, streamName string, src io.Reader, dst io.Writer, copies *sync.WaitGroup) {
	defer copies.Done()
	_, _ = io.Copy(logSink{service: s, processID: id, stream: streamName, dst: dst}, src)
}

func (s *Service) monitorProcess(id string, cmd *exec.Cmd, handle *runtimeHandle, copies *sync.WaitGroup, children []model.ChildProcess) {
	defer close(handle.done)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	currentChildren := cloneChildren(children)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-waitCh:
			copies.Wait()
			now := time.Now().UTC()
			exitCode, signal, outcome := exitMetadata(err)
			s.finishRuntime(id, outcome, &now, exitCode, signal, currentChildren)
			return
		case <-ticker.C:
			now := time.Now().UTC()
			snapshot, err := s.observer.Snapshot(cmd.Process.Pid, currentChildren, now)
			if err != nil {
				continue
			}
			currentChildren = snapshot.Children
			_ = s.store.SetChildren(id, snapshot.Children, now)
			_ = s.store.ObserveCPU(id, snapshot.RootCPU, now)
		}
	}
}

func (s *Service) monitorExternalProcess(id string, pid int, handle *runtimeHandle, children []model.ChildProcess) {
	defer close(handle.done)

	currentChildren := cloneChildren(children)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case update := <-handle.complete:
			s.finishRuntime(id, update.outcome, &update.finishedAt, update.exitCode, update.signal, currentChildren)
			return
		case <-ticker.C:
			now := time.Now().UTC()
			snapshot, err := s.observer.Snapshot(pid, currentChildren, now)
			if err == nil {
				currentChildren = snapshot.Children
				_ = s.store.SetChildren(id, snapshot.Children, now)
				_ = s.store.ObserveCPU(id, snapshot.RootCPU, now)
				continue
			}
			if !processExists(pid) {
				s.finishRuntime(id, "unknown", &now, nil, nil, currentChildren)
				return
			}
		}
	}
}

func (s *Service) finishRuntime(id, outcome string, finishedAt *time.Time, exitCode *int, signal *string, children []model.ChildProcess) {
	now := time.Now().UTC()
	if finishedAt == nil {
		finishedAt = &now
	}
	if len(children) > 0 {
		_ = s.store.SetChildren(id, markChildrenExited(children, *finishedAt), *finishedAt)
	}
	_ = s.store.SetStatus(id, "exited", outcome, finishedAt, exitCode, signal, *finishedAt)
	s.mu.Lock()
	delete(s.runtimes, id)
	s.mu.Unlock()
}

type logSink struct {
	service   *Service
	processID string
	stream    string
	dst       io.Writer
}

func (w logSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.dst != nil {
		if _, err := w.dst.Write(p); err != nil {
			return 0, err
		}
	}
	_, _ = w.service.store.AppendLog(w.processID, w.stream, string(p), time.Now().UTC())
	return len(p), nil
}

func initialCPU(percent float64, now time.Time) model.CPUStats {
	if percent <= 0 {
		return model.CPUStats{}
	}
	return model.CPUStats{
		LatestPercent:  percent,
		AveragePercent: percent,
		PeakPercent:    percent,
		SampleCount:    1,
		LastSampleAt:   model.TimePtr(now),
	}
}

func markChildrenExited(children []model.ChildProcess, now time.Time) []model.ChildProcess {
	if len(children) == 0 {
		return []model.ChildProcess{}
	}
	marked := cloneChildren(children)
	for i := range marked {
		marked[i].Status = "exited"
		marked[i].FinishedAt = model.TimePtr(now)
		marked[i].CPUPercent = 0
	}
	return marked
}

func cloneChildren(children []model.ChildProcess) []model.ChildProcess {
	if len(children) == 0 {
		return []model.ChildProcess{}
	}
	cloned := make([]model.ChildProcess, len(children))
	copy(cloned, children)
	for i := range cloned {
		cloned[i].Argv = append([]string(nil), children[i].Argv...)
		if children[i].FinishedAt != nil {
			finishedAt := children[i].FinishedAt.UTC()
			cloned[i].FinishedAt = &finishedAt
		}
	}
	return cloned
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

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func translateNotFound(id string, err error) error {
	if errors.Is(err, tracker.ErrProcessNotFound) {
		return ProcessNotFoundError{ID: id}
	}
	return err
}
