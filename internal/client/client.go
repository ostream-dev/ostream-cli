// Package client is a thin HTTP wrapper around the ostream relay's API.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var (
	ErrUnauthorized = errors.New("unauthorized (invalid or missing token)")
	ErrForbidden    = errors.New("forbidden (insufficient permission)")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict (another consumer is connected)")
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP: &http.Client{
			// No overall timeout — tail requests are long-lived.
			Timeout: 0,
		},
	}
}

type PushOpts struct {
	EOF bool
}

// Push streams `body` to the given stream path. Blocks until body returns
// EOF (or an error), then waits for the server's response. On a clean
// close with EOF=true, the relay marks the stream terminated.
func (c *Client) Push(ctx context.Context, path string, body io.Reader, opts PushOpts) error {
	u, err := c.urlFor("/s/" + path)
	if err != nil {
		return err
	}
	if opts.EOF {
		q := u.Query()
		q.Set("eof", "1")
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return statusError(resp)
}

type TailOpts struct {
	Tail   int    // ?tail=N; zero means omit
	NoKick bool   // ?kick=0
	After  string // ?after=<stream-id>
}

// Tail streams from the given stream path to `out`. Returns when the
// server closes the connection (EOF marker from a producer, a 4xx, or
// network drop). The caller is responsible for reconnecting as desired.
func (c *Client) Tail(ctx context.Context, path string, out io.Writer, opts TailOpts) error {
	u, err := c.urlFor("/s/" + path)
	if err != nil {
		return err
	}
	q := u.Query()
	if opts.Tail > 0 {
		q.Set("tail", strconv.Itoa(opts.Tail))
	}
	if opts.NoKick {
		q.Set("kick", "0")
	}
	if opts.After != "" {
		q.Set("after", opts.After)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusError(resp); err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

type Stream struct {
	Path              string `json:"path"`
	Lines             int    `json:"lines"`
	ConsumerConnected bool   `json:"consumer_connected"`
}

func (c *Client) ListStreams(ctx context.Context) ([]Stream, error) {
	u, err := c.urlFor("/streams")
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusError(resp); err != nil {
		return nil, err
	}
	var body struct {
		Streams []Stream `json:"streams"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Streams, nil
}

func (c *Client) DeleteStream(ctx context.Context, path string) error {
	u, err := c.urlFor("/streams/" + path)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return statusError(resp)
}

func (c *Client) auth(r *http.Request) {
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) urlFor(path string) (*url.URL, error) {
	if c.BaseURL == "" {
		return nil, errors.New("relay URL not configured")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid relay URL %q: %w", c.BaseURL, err)
	}
	// path already includes its leading slash.
	u.Path = path
	return u, nil
}

func statusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	}
	// Try to include the body for diagnostics, but bound it.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("http %d: %s", resp.StatusCode, trim(body))
}

func trim(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// Deadline used for non-streaming requests (list, delete, etc).
const quickTimeout = 15 * time.Second

// QuickContext returns a context with the package's short deadline.
// Callers that need long-running streams should use a parent context
// bound to a signal handler instead.
func QuickContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, quickTimeout)
}
