package api

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ptrack/internal/daemon"
	"ptrack/internal/daemonctl"
	"ptrack/internal/model"
)

func TestHealthEndpoint(t *testing.T) {
	service := daemon.NewService(daemon.Config{})
	if err := service.SeedPrototypeState(t.TempDir()); err != nil {
		t.Fatalf("seed prototype state: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	recorder := httptest.NewRecorder()
	NewServer(service, nil).Mux().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}

	var payload model.DaemonInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.TrackedProcessCount != 2 {
		t.Fatalf("expected seeded processes, got %d", payload.TrackedProcessCount)
	}
}

func TestProcessListAndDetailEndpoints(t *testing.T) {
	service := daemon.NewService(daemon.Config{})
	if err := service.SeedPrototypeState(t.TempDir()); err != nil {
		t.Fatalf("seed prototype state: %v", err)
	}

	server := NewServer(service, nil)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes?status=running&limit=10", nil)
	listRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d", listRecorder.Code)
	}

	var listPayload model.ProcessListResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listPayload.Items) != 1 {
		t.Fatalf("expected 1 running process, got %d", len(listPayload.Items))
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes/"+listPayload.Items[0].ID, nil)
	detailRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(detailRecorder, detailReq)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected detail status: %d", detailRecorder.Code)
	}

	var detailPayload model.ProcessDetail
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detailPayload); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailPayload.Source.Kind != "ptrack" {
		t.Fatalf("expected source kind ptrack, got %q", detailPayload.Source.Kind)
	}
}

func TestLogsAndChildrenEndpoints(t *testing.T) {
	service := daemon.NewService(daemon.Config{})
	if err := service.SeedPrototypeState(t.TempDir()); err != nil {
		t.Fatalf("seed prototype state: %v", err)
	}

	server := NewServer(service, nil)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes?status=running&limit=10", nil)
	listRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(listRecorder, listReq)

	var listPayload model.ProcessListResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	processID := listPayload.Items[0].ID

	logsReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes/"+processID+"/logs?after=0&limit=10", nil)
	logsRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(logsRecorder, logsReq)
	if logsRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected logs status: %d", logsRecorder.Code)
	}

	var logsPayload model.ProcessLogsResponse
	if err := json.Unmarshal(logsRecorder.Body.Bytes(), &logsPayload); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
	if len(logsPayload.Items) == 0 {
		t.Fatal("expected retained logs")
	}

	childrenReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes/"+processID+"/children", nil)
	childrenRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(childrenRecorder, childrenReq)
	if childrenRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected children status: %d", childrenRecorder.Code)
	}

	var childrenPayload model.ProcessChildrenResponse
	if err := json.Unmarshal(childrenRecorder.Body.Bytes(), &childrenPayload); err != nil {
		t.Fatalf("decode children response: %v", err)
	}
	if len(childrenPayload.Items) != 1 {
		t.Fatalf("expected 1 child process, got %d", len(childrenPayload.Items))
	}
}

func TestWebSocketProcessSubscription(t *testing.T) {
	service := daemon.NewService(daemon.Config{})
	detail, err := service.TrackInvocation("ptrack", []string{"sh", "-c", "printf 'ws-start\\n'; sleep 0.4; printf 'ws-end\\n'"}, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("track invocation: %v", err)
	}
	defer func() {
		_, _ = service.WaitForExit(detail.ID)
	}()

	httpServer := httptest.NewServer(NewServer(service, nil).Mux())
	defer httpServer.Close()

	client := newTestWebSocketClient(t, httpServer.URL, "/api/v1/ws")
	defer client.Close()

	message := client.ReadEnvelope(t, time.Now().Add(2*time.Second))
	if message.Type != "hello" {
		t.Fatalf("expected hello event, got %q", message.Type)
	}

	client.WriteJSON(t, model.SubscriptionMessage{
		Type:      "subscribe",
		Topic:     "process",
		ProcessID: detail.ID,
		LogsAfter: 0,
	})

	seenUpdated := false
	seenLog := false
	seenExited := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !(seenUpdated && seenLog && seenExited) {
		envelope := client.ReadEnvelope(t, deadline)
		switch envelope.Type {
		case "process.updated":
			seenUpdated = true
		case "process.log.append":
			seenLog = true
		case "process.exited":
			seenExited = true
		}
	}

	if !seenUpdated || !seenLog || !seenExited {
		t.Fatalf("expected process.updated, process.log.append, and process.exited events; got updated=%v log=%v exited=%v", seenUpdated, seenLog, seenExited)
	}
}

func TestInternalLifecycleEndpoints(t *testing.T) {
	service := daemon.NewService(daemon.Config{})
	server := NewServer(service, nil)

	cmd := exec.Command("sh", "-c", "printf 'bridge\\n'; sleep 0.2")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start external process: %v", err)
	}

	registerBody, err := json.Marshal(daemonctl.RegisterRequest{
		Wrapper:   "ptrack",
		Args:      []string{"sh", "-c", "printf 'bridge\\n'; sleep 0.2"},
		CWD:       t.TempDir(),
		PID:       cmd.Process.Pid,
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal register request: %v", err)
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/internal/processes", strings.NewReader(string(registerBody)))
	registerReq.Header.Set("Content-Type", "application/json")
	registerRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(registerRecorder, registerReq)
	if registerRecorder.Code != http.StatusCreated {
		t.Fatalf("unexpected register status: %d", registerRecorder.Code)
	}

	var detail model.ProcessDetail
	if err := json.Unmarshal(registerRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode register response: %v", err)
	}

	appendBody, err := json.Marshal(daemonctl.AppendLogRequest{
		Stream: "stdout",
		Text:   "bridge\n",
	})
	if err != nil {
		t.Fatalf("marshal append request: %v", err)
	}
	appendReq := httptest.NewRequest(http.MethodPost, "/internal/processes/"+detail.ID+"/logs", strings.NewReader(string(appendBody)))
	appendReq.Header.Set("Content-Type", "application/json")
	appendRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(appendRecorder, appendReq)
	if appendRecorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected append status: %d", appendRecorder.Code)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait external process: %v", err)
	}

	exitCode := 0
	completeBody, err := json.Marshal(daemonctl.CompleteRequest{
		FinishedAt: time.Now().UTC(),
		ExitCode:   &exitCode,
		Outcome:    "succeeded",
	})
	if err != nil {
		t.Fatalf("marshal complete request: %v", err)
	}
	completeReq := httptest.NewRequest(http.MethodPost, "/internal/processes/"+detail.ID+"/complete", strings.NewReader(string(completeBody)))
	completeReq.Header.Set("Content-Type", "application/json")
	completeRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(completeRecorder, completeReq)
	if completeRecorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected complete status: %d", completeRecorder.Code)
	}

	time.Sleep(250 * time.Millisecond)

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes/"+detail.ID, nil)
	detailRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(detailRecorder, detailReq)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected detail status: %d", detailRecorder.Code)
	}
	var finished model.ProcessDetail
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &finished); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if finished.Status != "exited" || finished.Outcome != "succeeded" {
		t.Fatalf("expected succeeded exit, got status=%q outcome=%q", finished.Status, finished.Outcome)
	}

	logsReq := httptest.NewRequest(http.MethodGet, "/api/v1/processes/"+detail.ID+"/logs?after=0&limit=20", nil)
	logsRecorder := httptest.NewRecorder()
	server.Mux().ServeHTTP(logsRecorder, logsReq)
	if logsRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected logs status: %d", logsRecorder.Code)
	}
	var logs model.ProcessLogsResponse
	if err := json.Unmarshal(logsRecorder.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
	joined := make([]string, 0, len(logs.Items))
	for _, entry := range logs.Items {
		joined = append(joined, entry.Text)
	}
	text := strings.Join(joined, "")
	if !strings.Contains(text, "singleton daemon") || !strings.Contains(text, "bridge") {
		t.Fatalf("expected internal logs to be retained, got %q", text)
	}
}

type testWebSocketClient struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

type rawEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func newTestWebSocketClient(t *testing.T, baseURL, path string) *testWebSocketClient {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("dial websocket server: %v", err)
	}
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	key := base64.StdEncoding.EncodeToString([]byte("ptrack-websocket"))
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", path, parsed.Host, key)
	if _, err := writer.WriteString(request); err != nil {
		t.Fatalf("write websocket handshake: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("flush websocket handshake: %v", err)
	}

	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read websocket handshake: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("unexpected websocket status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	return &testWebSocketClient{conn: conn, reader: reader, writer: writer}
}

func (c *testWebSocketClient) Close() {
	_ = c.conn.Close()
}

func (c *testWebSocketClient) WriteJSON(t *testing.T, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal websocket payload: %v", err)
	}
	if err := writeMaskedFrame(c.writer, wsTextMessage, payload); err != nil {
		t.Fatalf("write websocket frame: %v", err)
	}
	if err := c.writer.Flush(); err != nil {
		t.Fatalf("flush websocket frame: %v", err)
	}
}

func (c *testWebSocketClient) ReadEnvelope(t *testing.T, deadline time.Time) rawEnvelope {
	t.Helper()
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		opcode, payload, err := readServerFrame(c.reader)
		if err != nil {
			t.Fatalf("read websocket frame: %v", err)
		}
		if opcode == wsPingMessage {
			if err := writeMaskedFrame(c.writer, wsPongMessage, payload); err != nil {
				t.Fatalf("write websocket pong: %v", err)
			}
			if err := c.writer.Flush(); err != nil {
				t.Fatalf("flush websocket pong: %v", err)
			}
			continue
		}
		if opcode != wsTextMessage {
			continue
		}
		var envelope rawEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode websocket payload: %v", err)
		}
		return envelope
	}
}

func writeMaskedFrame(writer io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xFFFF:
		header = append(header, 0x80|126)
		var extended [2]byte
		binary.BigEndian.PutUint16(extended[:], uint16(length))
		header = append(header, extended[:]...)
	default:
		header = append(header, 0x80|127)
		var extended [8]byte
		binary.BigEndian.PutUint64(extended[:], uint64(length))
		header = append(header, extended[:]...)
	}
	mask := []byte{0x01, 0x02, 0x03, 0x04}
	if _, err := writer.Write(header); err != nil {
		return err
	}
	if _, err := writer.Write(mask); err != nil {
		return err
	}
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	_, err := writer.Write(masked)
	return err
}

func readServerFrame(reader io.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}

	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		var extended uint16
		if err := binary.Read(reader, binary.BigEndian, &extended); err != nil {
			return 0, nil, err
		}
		length = int64(extended)
	case 127:
		var extended uint64
		if err := binary.Read(reader, binary.BigEndian, &extended); err != nil {
			return 0, nil, err
		}
		length = int64(extended)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	return header[0] & 0x0F, payload, nil
}
