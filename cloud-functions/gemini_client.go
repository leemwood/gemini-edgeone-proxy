package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type geminiClient struct {
	pool   *tokenPool
	client *http.Client
}

func newGeminiClient(pool *tokenPool) *geminiClient {
	return &geminiClient{
		pool: pool,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *geminiClient) do(path string, body []byte) (*http.Response, []byte, string, error) {
	token, err := c.pool.getToken()
	if err != nil {
		return nil, nil, "", fmt.Errorf("token error: %w", err)
	}

	url := fmt.Sprintf("%s%s?key=%s", geminiBaseURL, path, token)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, token, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, token, err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		c.pool.markRateLimited(token)
		resp.Body.Close()
		return nil, nil, token, fmt.Errorf("rate limited, token rotated")
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, token, err
	}

	return resp, respBody, token, nil
}

func (c *geminiClient) doWithRetry(path string, body []byte, maxRetries int) (*http.Response, []byte, error) {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		resp, respBody, _, err := c.do(path, body)
		if err != nil {
			lastErr = err
			if i < maxRetries {
				continue
			}
			return nil, nil, lastErr
		}
		return resp, respBody, nil
	}
	return nil, nil, lastErr
}
