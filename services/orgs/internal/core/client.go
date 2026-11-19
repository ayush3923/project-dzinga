// Package core reads applications from core-api. The organizations service owns no
// application data; it groups core-api's analysis by company on each request.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Applications returns every application from core-api's analysis, as raw JSON.
func (c *Client) Applications(ctx context.Context) ([]json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/v1/analysis", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("core-api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("core-api: analysis returned %d", res.StatusCode)
	}
	var body struct {
		Applications []json.RawMessage `json:"applications"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("core-api: %w", err)
	}
	return body.Applications, nil
}
