package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"ptrack/internal/model"
)

const (
	wsTextMessage  = 0x1
	wsCloseMessage = 0x8
	wsPingMessage  = 0x9
	wsPongMessage  = 0xA
)

type wsConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writer  *bufio.Writer
	writeMu sync.Mutex
}

type wsInbound struct {
	subscription *model.SubscriptionMessage
	pong         []byte
	err          error
}

type wsSubscription struct {
	processes bool
	processID string
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := acceptWebSocket(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_websocket", err.Error(), nil)
		return
	}
	defer conn.Close()

	_ = conn.WriteJSON(model.EventEnvelope{
		Type:      "hello",
		Timestamp: time.Now().UTC(),
		Payload:   model.HelloPayload{Daemon: s.service.Health()},
	})

	events, unsubscribe := s.service.SubscribeEvents(128)
	defer unsubscribe()

	inbound := make(chan wsInbound, 8)
	go readWebSocketLoop(conn, inbound)

	var subscription wsSubscription
	for {
		select {
		case message, ok := <-inbound:
			if !ok || message.err != nil {
				return
			}
			if len(message.pong) > 0 {
				_ = conn.WriteControl(wsPongMessage, message.pong)
				continue
			}
			if message.subscription != nil {
				s.applySubscription(conn, &subscription, *message.subscription)
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			if !subscription.matches(event) {
				continue
			}
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}
}

func (s *Server) applySubscription(conn *wsConn, subscription *wsSubscription, message model.SubscriptionMessage) {
	if message.Type != "subscribe" {
		_ = conn.WriteJSON(model.EventEnvelope{
			Type:      "error",
			Timestamp: time.Now().UTC(),
			Payload:   model.ErrorResponse{Error: model.APIError{Code: "invalid_message", Message: "unsupported websocket message type"}},
		})
		return
	}

	switch message.Topic {
	case "processes":
		subscription.processes = true
		response, err := s.service.ListProcesses("all", 500, "")
		if err != nil {
			_ = conn.WriteJSON(model.EventEnvelope{
				Type:      "error",
				Timestamp: time.Now().UTC(),
				Payload:   model.ErrorResponse{Error: model.APIError{Code: "internal_error", Message: "failed to list tracked processes"}},
			})
			return
		}
		for _, item := range response.Items {
			detail, err := s.service.ProcessDetail(item.ID)
			if err != nil {
				continue
			}
			if err := conn.WriteJSON(model.EventEnvelope{
				Type:      "process.created",
				Timestamp: time.Now().UTC(),
				Payload:   model.ProcessEventPayload{Process: detail},
			}); err != nil {
				return
			}
		}
	case "process":
		if message.ProcessID == "" {
			_ = conn.WriteJSON(model.EventEnvelope{
				Type:      "error",
				Timestamp: time.Now().UTC(),
				Payload:   model.ErrorResponse{Error: model.APIError{Code: "invalid_message", Message: "process subscriptions require process_id"}},
			})
			return
		}
		subscription.processID = message.ProcessID
		detail, err := s.service.ProcessDetail(message.ProcessID)
		if err != nil {
			_ = conn.WriteJSON(model.EventEnvelope{
				Type:      "error",
				Timestamp: time.Now().UTC(),
				Payload:   model.ErrorResponse{Error: model.APIError{Code: "process_not_found", Message: err.Error(), Details: map[string]any{"id": message.ProcessID}}},
			})
			return
		}
		if err := conn.WriteJSON(model.EventEnvelope{
			Type:      "process.updated",
			Timestamp: time.Now().UTC(),
			Payload:   model.ProcessEventPayload{Process: detail},
		}); err != nil {
			return
		}
		children, err := s.service.ProcessChildren(message.ProcessID)
		if err == nil {
			if err := conn.WriteJSON(model.EventEnvelope{
				Type:      "process.children.updated",
				Timestamp: time.Now().UTC(),
				Payload:   model.ProcessChildrenEventPayload{ProcessID: message.ProcessID, Children: children.Items},
			}); err != nil {
				return
			}
		}
		logs, err := s.service.ProcessLogs(message.ProcessID, message.LogsAfter, 5000)
		if err == nil {
			for _, entry := range logs.Items {
				if err := conn.WriteJSON(model.EventEnvelope{
					Type:      "process.log.append",
					Timestamp: entry.Timestamp,
					Payload:   model.ProcessLogEventPayload{ProcessID: message.ProcessID, Entry: entry},
				}); err != nil {
					return
				}
			}
		}
	default:
		_ = conn.WriteJSON(model.EventEnvelope{
			Type:      "error",
			Timestamp: time.Now().UTC(),
			Payload:   model.ErrorResponse{Error: model.APIError{Code: "invalid_message", Message: "unsupported subscription topic"}},
		})
	}
}

func readWebSocketLoop(conn *wsConn, inbound chan<- wsInbound) {
	defer close(inbound)
	for {
		opcode, payload, err := conn.ReadMessage()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				inbound <- wsInbound{err: err}
			}
			return
		}
		switch opcode {
		case wsTextMessage:
			var message model.SubscriptionMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				message.Type = "invalid"
			}
			inbound <- wsInbound{subscription: &message}
		case wsPingMessage:
			inbound <- wsInbound{pong: payload}
		case wsCloseMessage:
			inbound <- wsInbound{err: io.EOF}
			return
		}
	}
}

func (s wsSubscription) matches(event model.EventEnvelope) bool {
	processID := eventProcessID(event)
	if s.processID != "" && processID == s.processID {
		switch event.Type {
		case "process.created", "process.updated", "process.exited", "process.log.append", "process.children.updated":
			return true
		}
	}
	if s.processes {
		switch event.Type {
		case "process.created", "process.updated", "process.exited":
			return true
		}
	}
	return false
}

func eventProcessID(event model.EventEnvelope) string {
	switch payload := event.Payload.(type) {
	case model.ProcessEventPayload:
		return payload.Process.ID
	case model.ProcessLogEventPayload:
		return payload.ProcessID
	case model.ProcessChildrenEventPayload:
		return payload.ProcessID
	default:
		return ""
	}
}

func acceptWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !headerHasToken(r.Header.Get("Connection"), "Upgrade") {
		return nil, fmt.Errorf("request must include websocket upgrade headers")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, fmt.Errorf("unsupported websocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return nil, fmt.Errorf("missing websocket key")
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("response writer does not support websocket upgrades")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}

	response := fmt.Sprintf(
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		websocketAcceptKey(key),
	)
	if _, err := rw.WriteString(response); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &wsConn{conn: conn, reader: rw.Reader, writer: rw.Writer}, nil
}

func websocketAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerHasToken(headerValue, token string) bool {
	for _, part := range strings.Split(headerValue, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func (c *wsConn) ReadMessage() (byte, []byte, error) {
	header, err := c.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	lengthByte, err := c.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if header&0x80 == 0 {
		return 0, nil, fmt.Errorf("fragmented websocket frames are not supported")
	}
	if lengthByte&0x80 == 0 {
		return 0, nil, fmt.Errorf("client websocket frames must be masked")
	}

	payloadLength, err := readPayloadLength(c.reader, lengthByte&0x7F)
	if err != nil {
		return 0, nil, err
	}
	if payloadLength > 1<<20 {
		return 0, nil, fmt.Errorf("websocket frame too large")
	}

	maskKey := make([]byte, 4)
	if _, err := io.ReadFull(c.reader, maskKey); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= maskKey[i%4]
	}
	return header & 0x0F, payload, nil
}

func (c *wsConn) WriteJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteControl(wsTextMessage, payload)
}

func (c *wsConn) WriteControl(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := writeFrame(c.writer, opcode, payload); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

func readPayloadLength(reader io.Reader, base byte) (int64, error) {
	switch base {
	case 126:
		var length uint16
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return 0, err
		}
		return int64(length), nil
	case 127:
		var length uint64
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return 0, err
		}
		return int64(length), nil
	default:
		return int64(base), nil
	}
}

func writeFrame(writer io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 0xFFFF:
		header = append(header, 126)
		var extended [2]byte
		binary.BigEndian.PutUint16(extended[:], uint16(length))
		header = append(header, extended[:]...)
	default:
		header = append(header, 127)
		var extended [8]byte
		binary.BigEndian.PutUint64(extended[:], uint64(length))
		header = append(header, extended[:]...)
	}
	if _, err := writer.Write(header); err != nil {
		return err
	}
	if _, err := writer.Write(payload); err != nil {
		return err
	}
	return nil
}
