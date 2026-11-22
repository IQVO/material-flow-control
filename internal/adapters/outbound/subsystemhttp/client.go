// Package subsystemhttp is the shared REST client of the subsystem-controller interface (relationships 4 and 5):
// JSON in and out, the Idempotency-Key, W3C trace propagation, a per-call timeout, and the classification of an
// answer into a *subsystem.Refusal (with the replay class the producer documents) or subsystem.ErrUnreachable.
package subsystemhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

const maxBody = 1 << 20

// Client calls one subsystem controller.
type Client struct {
	base      string
	http      *http.Client
	retryable map[string]bool
}

// New builds a client for baseURL; retryable lists the producer's RETRYABLE problem codes.
func New(baseURL string, timeout time.Duration, retryable []string) *Client {
	r := make(map[string]bool, len(retryable))
	for _, code := range retryable {
		r[code] = true
	}
	// carry (Task 14 review): never follow a redirect; a 3xx reaches Do's ErrUnreachable branch.
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// carry (Task 14 review): the dispatcher calls from a background loop whose ctx has no span, so
	// the otelhttp transport starts a client span per call and injects its traceparent.
	hc := &http.Client{Timeout: timeout, CheckRedirect: noRedirect, Transport: otelhttp.NewTransport(http.DefaultTransport)}
	return &Client{base: strings.TrimRight(baseURL, "/"), http: hc, retryable: r}
}

// Path joins escaped path segments: Path("asrs", id, "storage-jobs") is "/asrs/<id>/storage-jobs".
func Path(segments ...string) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}

type problem struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Do sends one request: key is the Idempotency-Key ("" for a GET); in and out may be nil.
func (c *Client) Do(ctx context.Context, method, path, key string, in, out any) error {
	req, err := c.request(ctx, method, path, key, in)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %v", subsystem.ErrUnreachable, method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("%w: read %s %s: %v", subsystem.ErrUnreachable, method, path, err)
	}
	switch {
	case resp.StatusCode >= http.StatusInternalServerError || transient(resp.StatusCode):
		return fmt.Errorf("%w: %s %s answered %d", subsystem.ErrUnreachable, method, path, resp.StatusCode)
	case resp.StatusCode >= http.StatusBadRequest:
		return c.refusal(resp.StatusCode, body)
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		return fmt.Errorf("%w: %s %s answered %d", subsystem.ErrUnreachable, method, path, resp.StatusCode)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: decode %s %s: %v", subsystem.ErrUnreachable, method, path, err)
	}
	return nil
}

// transient names the 4xx answers that say "not now" rather than "no": 429, and
// carry (Task 14 review): 408 Request Timeout and 425 Too Early.
func transient(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status == http.StatusTooEarly
}

func (c *Client) request(ctx context.Context, method, path, key string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json, application/problem+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	return req, nil
}

func (c *Client) refusal(status int, body []byte) error {
	var p problem
	_ = json.Unmarshal(body, &p)
	code := p.Code
	if code == "" {
		code = typeSlug(p.Type)
	}
	if code == "" {
		code = fmt.Sprintf("http-%d", status)
	}
	return &subsystem.Refusal{Status: status, Code: code, Detail: p.Detail, Retryable: c.retryable[code]}
}

// typeSlug is the last path segment of a problem type URI ("" for an empty type or about:blank).
// carry (Task 14 review): both producers build the type as problemBaseURI + code.
func typeSlug(typ string) string {
	if typ == "" || typ == "about:blank" {
		return ""
	}
	u, err := url.Parse(typ)
	if err != nil {
		return ""
	}
	if slug := path.Base(u.Path); slug != "." && slug != "/" {
		return slug
	}
	return ""
}
