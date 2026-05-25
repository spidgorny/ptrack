package tracker

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptrack/internal/model"
	"ptrack/internal/stream"
)

var ErrProcessNotFound = errors.New("tracked process not found")

type ProcessSpec struct {
	Command     string
	Argv        []string
	DisplayName string
	CWD         string
	Status      string
	Outcome     string
	StartedAt   time.Time
	FinishedAt  *time.Time
	ExitCode    *int
	Signal      *string
	PID         int
	PTY         model.PTYInfo
	CPU         model.CPUStats
	Source      model.ProcessSource
	Children    []model.ChildProcess
}

type ProcessRecord struct {
	ID          string
	Command     string
	Argv        []string
	DisplayName string
	CWD         string
	Status      string
	Outcome     string
	StartedAt   time.Time
	FinishedAt  *time.Time
	ExitCode    *int
	Signal      *string
	PID         int
	PTY         model.PTYInfo
	CPU         model.CPUStats
	Source      model.ProcessSource
	Children    []model.ChildProcess
	Logs        *stream.Ring
}

type Store struct {
	mu               sync.RWMutex
	startedAt        time.Time
	version          string
	socketPath       string
	httpAddress      string
	websocketPath    string
	web              model.WebUIInfo
	logBufferBytes   int
	nextID           int64
	nextPID          int
	nextSubscriberID int64
	order            []string
	processes        map[string]*ProcessRecord
	subscribers      map[int64]chan model.EventEnvelope
}

func NewStore(startedAt time.Time, version, socketPath, httpAddress, websocketPath string, web model.WebUIInfo, logBufferBytes int) *Store {
	if websocketPath == "" {
		websocketPath = "/api/v1/ws"
	}
	if logBufferBytes <= 0 {
		logBufferBytes = 64 * 1024
	}
	return &Store{
		startedAt:      startedAt.UTC(),
		version:        version,
		socketPath:     socketPath,
		httpAddress:    httpAddress,
		websocketPath:  websocketPath,
		web:            web,
		logBufferBytes: logBufferBytes,
		nextPID:        18000,
		order:          make([]string, 0, 16),
		processes:      make(map[string]*ProcessRecord),
		subscribers:    make(map[int64]chan model.EventEnvelope),
	}
}

func (s *Store) Subscribe(buffer int) (<-chan model.EventEnvelope, func()) {
	if buffer <= 0 {
		buffer = 32
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextSubscriberID++
	id := s.nextSubscriberID
	ch := make(chan model.EventEnvelope, buffer)
	s.subscribers[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(existing)
		}
	}
}

func (s *Store) AddProcess(spec ProcessSpec) *ProcessRecord {
	s.mu.Lock()
	s.nextID++
	if spec.PID == 0 {
		s.nextPID++
		spec.PID = s.nextPID
	}
	if spec.Status == "" {
		spec.Status = "starting"
	}
	if spec.Outcome == "" {
		spec.Outcome = "unknown"
	}
	if spec.StartedAt.IsZero() {
		spec.StartedAt = time.Now().UTC()
	}
	if spec.DisplayName == "" {
		spec.DisplayName = strings.Join(spec.Argv, " ")
	}

	id := fmt.Sprintf("proc_%06d", s.nextID)
	record := &ProcessRecord{
		ID:          id,
		Command:     spec.Command,
		Argv:        cloneStrings(spec.Argv),
		DisplayName: spec.DisplayName,
		CWD:         spec.CWD,
		Status:      spec.Status,
		Outcome:     spec.Outcome,
		StartedAt:   spec.StartedAt.UTC(),
		FinishedAt:  cloneTimePtr(spec.FinishedAt),
		ExitCode:    cloneIntPtr(spec.ExitCode),
		Signal:      cloneStringPtr(spec.Signal),
		PID:         spec.PID,
		PTY:         spec.PTY,
		CPU:         cloneCPU(spec.CPU),
		Source:      cloneSource(spec.Source),
		Children:    cloneChildren(spec.Children),
		Logs:        stream.NewRing(s.logBufferBytes),
	}

	s.order = append(s.order, id)
	s.processes[id] = record
	detail := record.detail(time.Now().UTC())
	s.mu.Unlock()

	s.publish(model.EventEnvelope{
		Type:      "process.created",
		Timestamp: time.Now().UTC(),
		Payload:   model.ProcessEventPayload{Process: detail},
	})
	return record
}

func (s *Store) ProcessCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.processes)
}

func (s *Store) AppendLog(id, streamName, text string, now time.Time) (model.LogEntry, error) {
	s.mu.RLock()
	record, ok := s.processes[id]
	s.mu.RUnlock()
	if !ok {
		return model.LogEntry{}, ErrProcessNotFound
	}
	entry := record.Logs.Append(streamName, text, now)
	s.publish(model.EventEnvelope{
		Type:      "process.log.append",
		Timestamp: now.UTC(),
		Payload: model.ProcessLogEventPayload{
			ProcessID: id,
			Entry:     entry,
		},
	})
	return entry, nil
}

func (s *Store) SetChildren(id string, children []model.ChildProcess, now time.Time) error {
	s.mu.Lock()
	record, ok := s.processes[id]
	if !ok {
		s.mu.Unlock()
		return ErrProcessNotFound
	}
	if childrenEqual(record.Children, children) {
		s.mu.Unlock()
		return nil
	}
	record.Children = cloneChildren(children)
	payload := model.ProcessChildrenEventPayload{ProcessID: id, Children: cloneChildren(record.Children)}
	s.mu.Unlock()

	s.publish(model.EventEnvelope{
		Type:      "process.children.updated",
		Timestamp: now.UTC(),
		Payload:   payload,
	})
	return nil
}

func (s *Store) ObserveCPU(id string, percent float64, sampledAt time.Time) error {
	if percent < 0 {
		percent = 0
	}

	s.mu.Lock()
	record, ok := s.processes[id]
	if !ok {
		s.mu.Unlock()
		return ErrProcessNotFound
	}

	previousSamples := record.CPU.SampleCount
	record.CPU.LatestPercent = percent
	record.CPU.SampleCount++
	if record.CPU.SampleCount == 1 {
		record.CPU.AveragePercent = percent
		record.CPU.PeakPercent = percent
	} else {
		total := record.CPU.AveragePercent*float64(previousSamples) + percent
		record.CPU.AveragePercent = total / float64(record.CPU.SampleCount)
		if percent > record.CPU.PeakPercent {
			record.CPU.PeakPercent = percent
		}
	}
	record.CPU.LastSampleAt = model.TimePtr(sampledAt)
	detail := record.detail(sampledAt)
	s.mu.Unlock()

	s.publish(model.EventEnvelope{
		Type:      "process.updated",
		Timestamp: sampledAt.UTC(),
		Payload:   model.ProcessEventPayload{Process: detail},
	})
	return nil
}

func (s *Store) SetStatus(id, status, outcome string, finishedAt *time.Time, exitCode *int, signal *string, now time.Time) error {
	s.mu.Lock()
	record, ok := s.processes[id]
	if !ok {
		s.mu.Unlock()
		return ErrProcessNotFound
	}
	record.Status = status
	record.Outcome = outcome
	record.FinishedAt = cloneTimePtr(finishedAt)
	record.ExitCode = cloneIntPtr(exitCode)
	record.Signal = cloneStringPtr(signal)
	detail := record.detail(now)
	s.mu.Unlock()

	eventType := "process.updated"
	if status == "exited" {
		eventType = "process.exited"
	}
	s.publish(model.EventEnvelope{
		Type:      eventType,
		Timestamp: now.UTC(),
		Payload:   model.ProcessEventPayload{Process: detail},
	})
	return nil
}

func (s *Store) DaemonInfo(now time.Time) model.DaemonInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tracked, running := s.countsLocked()
	return model.DaemonInfo{
		Version:             s.version,
		StartedAt:           s.startedAt,
		UptimeMS:            now.UTC().Sub(s.startedAt).Milliseconds(),
		SocketPath:          s.socketPath,
		HTTPAddress:         s.httpAddress,
		WebSocketPath:       s.websocketPath,
		TrackedProcessCount: tracked,
		RunningProcessCount: running,
		Web:                 s.web,
	}
}

func (s *Store) ListProcesses(status string, limit int, cursor string, now time.Time) ([]model.ProcessSummary, string, int, int, error) {
	if limit <= 0 {
		limit = 100
	}
	offset, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", 0, 0, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	filtered := make([]*ProcessRecord, 0, len(s.order))
	for idx := len(s.order) - 1; idx >= 0; idx-- {
		record := s.processes[s.order[idx]]
		if matchesStatus(record.Status, status) {
			filtered = append(filtered, record)
		}
	}

	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	items := make([]model.ProcessSummary, 0, end-offset)
	for _, record := range filtered[offset:end] {
		items = append(items, record.summary(now))
	}

	nextCursor := ""
	if end < len(filtered) {
		nextCursor = encodeCursor(end)
	}

	tracked, running := s.countsLocked()
	return items, nextCursor, tracked, running, nil
}

func (s *Store) ProcessDetail(id string, now time.Time) (model.ProcessDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.processes[id]
	if !ok {
		return model.ProcessDetail{}, ErrProcessNotFound
	}
	return record.detail(now), nil
}

func (s *Store) ProcessLogs(id string, after int64, limit int) ([]model.LogEntry, int64, error) {
	s.mu.RLock()
	record, ok := s.processes[id]
	s.mu.RUnlock()
	if !ok {
		return nil, 0, ErrProcessNotFound
	}

	items, nextAfter := record.Logs.Entries(after, limit)
	return items, nextAfter, nil
}

func (s *Store) ProcessChildren(id string) ([]model.ChildProcess, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.processes[id]
	if !ok {
		return nil, ErrProcessNotFound
	}
	return cloneChildren(record.Children), nil
}

func (s *Store) publish(event model.EventEnvelope) {
	s.mu.RLock()
	listeners := make([]chan model.EventEnvelope, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		listeners = append(listeners, ch)
	}
	s.mu.RUnlock()

	for _, ch := range listeners {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Store) countsLocked() (tracked int, running int) {
	tracked = len(s.processes)
	for _, record := range s.processes {
		if record.Status == "starting" || record.Status == "running" {
			running++
		}
	}
	return tracked, running
}

func (p *ProcessRecord) summary(now time.Time) model.ProcessSummary {
	return model.ProcessSummary{
		ID:                p.ID,
		Command:           p.Command,
		Argv:              cloneStrings(p.Argv),
		DisplayName:       p.DisplayName,
		CWD:               p.CWD,
		Status:            p.Status,
		Outcome:           p.Outcome,
		StartedAt:         p.StartedAt,
		FinishedAt:        cloneTimePtr(p.FinishedAt),
		RuntimeMS:         model.RuntimeMilliseconds(p.StartedAt, p.FinishedAt, now.UTC()),
		ExitCode:          cloneIntPtr(p.ExitCode),
		Signal:            cloneStringPtr(p.Signal),
		CPU:               cloneCPU(p.CPU),
		Logs:              p.Logs.Stats(),
		ChildProcessCount: len(p.Children),
	}
}

func (p *ProcessRecord) detail(now time.Time) model.ProcessDetail {
	return model.ProcessDetail{
		ID:          p.ID,
		Command:     p.Command,
		Argv:        cloneStrings(p.Argv),
		DisplayName: p.DisplayName,
		CWD:         p.CWD,
		Status:      p.Status,
		Outcome:     p.Outcome,
		StartedAt:   p.StartedAt,
		FinishedAt:  cloneTimePtr(p.FinishedAt),
		RuntimeMS:   model.RuntimeMilliseconds(p.StartedAt, p.FinishedAt, now.UTC()),
		ExitCode:    cloneIntPtr(p.ExitCode),
		Signal:      cloneStringPtr(p.Signal),
		PID:         p.PID,
		PTY:         p.PTY,
		CPU:         cloneCPU(p.CPU),
		Logs:        p.Logs.Stats(),
		Children:    cloneChildren(p.Children),
		Source:      cloneSource(p.Source),
	}
}

func matchesStatus(current, filter string) bool {
	switch filter {
	case "", "all":
		return true
	case "running":
		return current == "starting" || current == "running"
	case "exited":
		return current == "exited"
	default:
		return false
	}
}

func encodeCursor(offset int) string {
	return fmt.Sprintf("offset:%d", offset)
}

func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if !strings.HasPrefix(cursor, "offset:") {
		return 0, fmt.Errorf("unsupported cursor %q", cursor)
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(cursor, "offset:"))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("unsupported cursor %q", cursor)
	}
	return offset, nil
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func cloneChildren(children []model.ChildProcess) []model.ChildProcess {
	if len(children) == 0 {
		return []model.ChildProcess{}
	}
	cloned := make([]model.ChildProcess, len(children))
	for i, child := range children {
		cloned[i] = child
		cloned[i].Argv = cloneStrings(child.Argv)
		cloned[i].FinishedAt = cloneTimePtr(child.FinishedAt)
	}
	return cloned
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCPU(cpu model.CPUStats) model.CPUStats {
	cpu.LastSampleAt = cloneTimePtr(cpu.LastSampleAt)
	return cpu
}

func cloneSource(source model.ProcessSource) model.ProcessSource {
	cloned := source
	if source.Docker != nil {
		docker := *source.Docker
		cloned.Docker = &docker
	}
	return cloned
}

func childrenEqual(left, right []model.ChildProcess) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].PID != right[i].PID ||
			left[i].PPID != right[i].PPID ||
			left[i].Command != right[i].Command ||
			left[i].Status != right[i].Status ||
			left[i].CPUPercent != right[i].CPUPercent ||
			left[i].StartedAt != right[i].StartedAt {
			return false
		}
		if !timePointersEqual(left[i].FinishedAt, right[i].FinishedAt) {
			return false
		}
		if !stringSlicesEqual(left[i].Argv, right[i].Argv) {
			return false
		}
	}
	return true
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func timePointersEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}
