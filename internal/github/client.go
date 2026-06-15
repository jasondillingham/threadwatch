// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// DefaultBaseURL is the standard GitHub REST API root. Tests override
	// it with the httptest server's URL.
	DefaultBaseURL = "https://api.github.com"

	// apiVersion follows GitHub's recommended pinning header.
	apiVersion = "2022-11-28"
)

// Client is a small wrapper around http.Client that knows how to talk to
// the GitHub REST API: it adds auth + UA + accept headers, supports
// conditional requests via ETag, and surfaces rate-limit information from
// response headers.
type Client struct {
	BaseURL    string
	UserAgent  string
	Token      string // optional; empty triggers 60 req/hr unauthenticated limit
	HTTPClient *http.Client
}

// NewClient returns a Client with sensible defaults. token may be empty.
func NewClient(token, userAgent string) *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		UserAgent:  userAgent,
		Token:      token,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Response wraps the parsed body and the conditional-request bookkeeping
// the fetcher needs to carry forward to the next poll.
type Response struct {
	StatusCode int
	ETag       string    // value of the response's ETag header (raw)
	RateLimit  RateLimit // parsed rate-limit headers
	Body       []byte    // non-nil on 200; nil on 304/non-2xx
}

// RateLimit mirrors the GitHub rate-limit headers. Zero values mean "not
// reported"; callers should treat that as "no signal" rather than
// "exhausted".
type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
	Used      int
}

// errKind classifies the most common upstream failures so the caller
// (poller) can choose a sensible reaction.
var (
	ErrNotFound     = errors.New("github: not found")
	ErrUnauthorized = errors.New("github: unauthorized")
	ErrRateLimited  = errors.New("github: rate limited")
)

// get issues a conditional GET against the given relative path, honoring
// the supplied ETag (sent as If-None-Match when non-empty). The path must
// begin with a leading slash.
func (c *Client) get(ctx context.Context, path, etag string) (Response, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = http.DefaultClient
	}
	url := c.BaseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if ua := c.UserAgent; ua != "" {
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", "threadwatch")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	r := Response{
		StatusCode: resp.StatusCode,
		ETag:       resp.Header.Get("ETag"),
		RateLimit:  parseRateLimit(resp.Header),
	}

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return r, fmt.Errorf("read body: %w", err)
		}
		r.Body = body
		return r, nil

	case http.StatusNotModified:
		// 304 with the ETag we sent. No body, no error.
		return r, nil

	case http.StatusNotFound:
		return r, ErrNotFound

	case http.StatusUnauthorized:
		return r, ErrUnauthorized

	case http.StatusForbidden:
		// 403 with rate-limit headers === rate-limited; otherwise auth.
		if r.RateLimit.Remaining == 0 && !r.RateLimit.Reset.IsZero() {
			return r, ErrRateLimited
		}
		return r, ErrUnauthorized

	case http.StatusTooManyRequests:
		return r, ErrRateLimited

	default:
		body, _ := io.ReadAll(resp.Body)
		return r, fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func parseRateLimit(h http.Header) RateLimit {
	rl := RateLimit{
		Limit:     atoi(h.Get("X-RateLimit-Limit")),
		Remaining: atoi(h.Get("X-RateLimit-Remaining")),
		Used:      atoi(h.Get("X-RateLimit-Used")),
	}
	if reset := h.Get("X-RateLimit-Reset"); reset != "" {
		if epoch, err := strconv.ParseInt(reset, 10, 64); err == nil {
			rl.Reset = time.Unix(epoch, 0).UTC()
		}
	}
	return rl
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// decode reads the response body into v. It tolerates an empty body
// (returns nil) so callers can ignore 304s without special-casing.
func decode(r Response, v any) error {
	if len(r.Body) == 0 {
		return nil
	}
	return json.Unmarshal(r.Body, v)
}
