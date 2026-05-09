package daemonctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"ptrack/internal/model"
)

type Client struct {
	baseURL string
	client  *http.Client
}

type RegisterRequest struct {
	Wrapper   string    `json:"wrapper"`
	Args      []string  `json:"args"`
	CWD       string    `json:"cwd"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

type AppendLogRequest struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

type CompleteRequest struct {
	FinishedAt time.Time `json:"finished_at"`
	ExitCode   *int      `json:"exit_code"`
	Signal     *string   `json:"signal"`
	Outcome    string    `json:"outcome"`
}

func NewClient(state State) *Client {
	return &Client{
		baseURL: BaseURL(state),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Client) RegisterInvocation(ctx context.Context, req RegisterRequest) (model.ProcessDetail, error) {
	var detail model.ProcessDetail
	err := c.doJSON(ctx, http.MethodPost, "/internal/processes", req, &detail)
	return detail, err
}

func (c *Client) AppendLog(ctx context.Context, id string, req AppendLogRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/internal/processes/"+id+"/logs", req, nil)
}

func (c *Client) CompleteInvocation(ctx context.Context, id string, req CompleteRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/internal/processes/"+id+"/complete", req, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var apiErr model.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil && apiErr.Error.Message != "" {
			return fmt.Errorf("%s", apiErr.Error.Message)
		}
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}
	if responseBody != nil {
		return json.NewDecoder(resp.Body).Decode(responseBody)
	}
	return nil
}
