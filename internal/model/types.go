package model

import "time"

type DaemonInfo struct {
	Version             string    `json:"version"`
	StartedAt           time.Time `json:"started_at"`
	UptimeMS            int64     `json:"uptime_ms"`
	SocketPath          string    `json:"socket_path"`
	HTTPAddress         string    `json:"http_address"`
	WebSocketPath       string    `json:"websocket_path"`
	TrackedProcessCount int       `json:"tracked_process_count"`
	RunningProcessCount int       `json:"running_process_count"`
	Web                 WebUIInfo `json:"web"`
}

type WebUIInfo struct {
	Enabled     bool     `json:"enabled"`
	Source      string   `json:"source,omitempty"`
	ResolvedDir string   `json:"resolved_dir,omitempty"`
	IndexHTML   string   `json:"index_html,omitempty"`
	ViteFiles   []string `json:"vite_files,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

type CPUStats struct {
	LatestPercent  float64    `json:"latest_percent"`
	AveragePercent float64    `json:"average_percent"`
	PeakPercent    float64    `json:"peak_percent"`
	SampleCount    int        `json:"sample_count"`
	LastSampleAt   *time.Time `json:"last_sample_at,omitempty"`
}

type LogRetention struct {
	RetainedBytes int   `json:"retained_bytes"`
	Truncated     bool  `json:"truncated"`
	LastSeq       int64 `json:"last_seq"`
}

type PTYInfo struct {
	Enabled bool `json:"enabled"`
	Cols    int  `json:"cols"`
	Rows    int  `json:"rows"`
}

type ChildProcess struct {
	PID        int        `json:"pid"`
	PPID       int        `json:"ppid"`
	Command    string     `json:"command"`
	Argv       []string   `json:"argv"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CPUPercent float64    `json:"cpu_percent"`
}

type DockerInfo struct {
	InContainer bool   `json:"in_container"`
	ContainerID string `json:"container_id,omitempty"`
}

type ProcessSource struct {
	Kind   string      `json:"kind"`
	Docker *DockerInfo `json:"docker,omitempty"`
}

type ProcessSummary struct {
	ID                string       `json:"id"`
	Command           string       `json:"command"`
	Argv              []string     `json:"argv"`
	DisplayName       string       `json:"display_name"`
	CWD               string       `json:"cwd"`
	Status            string       `json:"status"`
	Outcome           string       `json:"outcome"`
	StartedAt         time.Time    `json:"started_at"`
	FinishedAt        *time.Time   `json:"finished_at"`
	RuntimeMS         int64        `json:"runtime_ms"`
	ExitCode          *int         `json:"exit_code"`
	Signal            *string      `json:"signal"`
	CPU               CPUStats     `json:"cpu"`
	Logs              LogRetention `json:"logs"`
	ChildProcessCount int          `json:"child_process_count"`
}

type ProcessDetail struct {
	ID          string         `json:"id"`
	Command     string         `json:"command"`
	Argv        []string       `json:"argv"`
	DisplayName string         `json:"display_name"`
	CWD         string         `json:"cwd"`
	Status      string         `json:"status"`
	Outcome     string         `json:"outcome"`
	StartedAt   time.Time      `json:"started_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
	RuntimeMS   int64          `json:"runtime_ms"`
	ExitCode    *int           `json:"exit_code"`
	Signal      *string        `json:"signal"`
	PID         int            `json:"pid"`
	PTY         PTYInfo        `json:"pty"`
	CPU         CPUStats       `json:"cpu"`
	Logs        LogRetention   `json:"logs"`
	Children    []ChildProcess `json:"children"`
	Source      ProcessSource  `json:"source"`
}

type LogEntry struct {
	Seq       int64     `json:"seq"`
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
}

type Page struct {
	NextCursor string `json:"next_cursor,omitempty"`
	NextAfter  int64  `json:"next_after,omitempty"`
}

type ProcessListResponse struct {
	Items               []ProcessSummary `json:"items"`
	Page                Page             `json:"page"`
	TrackedProcessCount int              `json:"tracked_process_count"`
	RunningProcessCount int              `json:"running_process_count"`
}

type ProcessLogsResponse struct {
	Items []LogEntry `json:"items"`
	Page  Page       `json:"page"`
}

type ProcessChildrenResponse struct {
	Items []ChildProcess `json:"items"`
}

type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type SubscriptionMessage struct {
	Type      string `json:"type"`
	Topic     string `json:"topic"`
	ProcessID string `json:"process_id,omitempty"`
	LogsAfter int64  `json:"logs_after,omitempty"`
}

type EventEnvelope struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
}

type HelloPayload struct {
	Daemon DaemonInfo `json:"daemon"`
}

type ProcessEventPayload struct {
	Process ProcessDetail `json:"process"`
}

type ProcessLogEventPayload struct {
	ProcessID string   `json:"process_id"`
	Entry     LogEntry `json:"entry"`
}

type ProcessChildrenEventPayload struct {
	ProcessID string         `json:"process_id"`
	Children  []ChildProcess `json:"children"`
}

func RuntimeMilliseconds(startedAt time.Time, finishedAt *time.Time, now time.Time) int64 {
	end := now
	if finishedAt != nil {
		end = *finishedAt
	}
	if end.Before(startedAt) {
		return 0
	}
	return end.Sub(startedAt).Milliseconds()
}

func TimePtr(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}
