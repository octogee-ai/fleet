// Package admin is the HTTP client for the platform's Admin API.
package admin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	apiPrefix      = "/api/admin"
	requestTimeout = 2 * time.Minute
	maxLogLine     = 4 << 20
)

// Doer sends one HTTP request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Request is one Admin API call. A nil Body sends no body at all.
type Request struct {
	Method         string
	Path           string
	Query          url.Values
	Body           map[string]any
	IdempotencyKey string
}

// Target is the request's path and query, as shown to the operator.
func (r Request) Target() string {
	if len(r.Query) == 0 {
		return r.Path
	}
	return r.Path + "?" + r.Query.Encode()
}

// Path joins escaped segments under the Admin API prefix.
func Path(segments ...string) string {
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	return apiPrefix + "/" + strings.Join(escaped, "/")
}

// HTTPError is a non-2xx Admin API response.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return fmt.Sprintf("the Admin API rejected this sign-in (%d): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("the Admin API returned %d: %s", e.Status, e.Message)
}

// Client calls one environment's Admin API as the operator.
type Client struct {
	Origin string
	HTTP   Doer
	Token  func(context.Context) (string, error)

	token string
}

// Send performs a request and returns the response body.
func (c *Client) Send(ctx context.Context, request Request) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	response, err := c.open(ctx, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read the Admin API response: %w", err)
	}
	return data, nil
}

// Get reads a JSON response into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	data, err := c.Send(ctx, Request{Method: http.MethodGet, Path: path, Query: query})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// Stream calls handle for each line of a streamed response.
func (c *Client) Stream(ctx context.Context, request Request, handle func([]byte) error) error {
	response, err := c.open(ctx, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLogLine)
	for scanner.Scan() {
		if err := handle(scanner.Bytes()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("read the Admin API stream: %w", err)
	}
	return nil
}

func (c *Client) open(ctx context.Context, request Request) (*http.Response, error) {
	httpRequest, err := c.build(ctx, request)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTP.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("call the Admin API: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read the Admin API error: %w", readErr)
	}
	return nil, &HTTPError{Status: response.StatusCode, Message: errorMessage(data)}
}

func (c *Client) build(ctx context.Context, request Request) (*http.Request, error) {
	token, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if request.Body != nil {
		data, err := json.Marshal(request.Body)
		if err != nil {
			return nil, fmt.Errorf("encode the request body: %w", err)
		}
		body = bytes.NewReader(data)
	}
	target := strings.TrimRight(c.Origin, "/") + request.Target()
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, target, body)
	if err != nil {
		return nil, fmt.Errorf("build the Admin API request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Accept", "application/json")
	if request.Body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if request.IdempotencyKey != "" {
		httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	}
	return httpRequest, nil
}

func (c *Client) bearer(ctx context.Context) (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	token, err := c.Token(ctx)
	if err != nil {
		return "", err
	}
	c.token = token
	return token, nil
}

func errorMessage(data []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	return strings.TrimSpace(string(data))
}
