package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// ledgerClient is a thin client for the portal REST API.
type ledgerClient struct {
	base string // e.g. http://127.0.0.1:3000/api
	http *http.Client
}

// apiError is an error response from the portal. Message is the server's Japanese message.
type apiError struct {
	Status  int
	Method  string
	Path    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("ポータルAPIがエラーを返しました (%d %s %s): %s", e.Status, e.Method, e.Path, e.Message)
}

func newLedgerClient(base string) *ledgerClient {
	return &ledgerClient{base: strings.TrimRight(base, "/") + "/api", http: &http.Client{Timeout: 2 * time.Minute}}
}

// esc escapes a path segment such as a case_key ("LOGIN/001" -> "LOGIN%2F001").
func esc(s string) string { return url.PathEscape(s) }

type request struct {
	method      string
	path        string
	body        []byte
	contentType string
	idemKey     string
}

// retryable reports whether a request can be re-sent safely: reads, PUT/PATCH (which set absolute
// values) and POSTs protected by an Idempotency-Key.
func (r request) retryable() bool {
	return r.method == http.MethodGet || r.method == http.MethodPut || r.method == http.MethodPatch || r.idemKey != ""
}

func (c *ledgerClient) do(ctx context.Context, r request, out any) error {
	attempts := 1
	if r.retryable() {
		attempts = 3
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i) * 500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, r.method, c.base+r.path, bytes.NewReader(r.body))
		if err != nil {
			return err
		}
		if r.contentType != "" {
			req.Header.Set("Content-Type", r.contentType)
		}
		if r.idemKey != "" {
			req.Header.Set("Idempotency-Key", r.idemKey)
		}
		res, err := c.http.Do(req)
		if err != nil {
			last = fmt.Errorf("ポータル(%s)に接続できません。LEDGER_URLとポータルの起動状態を確認してください: %w", c.base, err)
			continue
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out == nil {
				return nil
			}
			return json.Unmarshal(data, out)
		}
		var msg struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &msg) != nil || msg.Message == "" {
			msg.Message = strings.TrimSpace(string(data))
		}
		last = &apiError{Status: res.StatusCode, Method: r.method, Path: r.path, Message: msg.Message}
		// 5xx and "in progress" conflicts on idempotent requests are worth another try; other 4xx are final.
		if res.StatusCode < 500 && !(res.StatusCode == 409 && r.idemKey != "" && strings.Contains(msg.Message, "処理中")) {
			return last
		}
	}
	return last
}

func (c *ledgerClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, request{method: http.MethodGet, path: path}, out)
}

func (c *ledgerClient) send(ctx context.Context, method, path string, body any, idemKey string, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(ctx, request{method: method, path: path, body: b, contentType: "application/json", idemKey: idemKey}, out)
}

func (c *ledgerClient) upload(ctx context.Context, path, fileName string, content []byte, fields map[string]string, idemKey string, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v != "" {
			if err := w.WriteField(k, v); err != nil {
				return err
			}
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, strings.NewReplacer(`"`, "_", "\\", "_", "\r", "", "\n", "").Replace(fileName)))
	h.Set("Content-Type", "application/octet-stream")
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err = part.Write(content); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodPost, path: path, body: buf.Bytes(), contentType: w.FormDataContentType(), idemKey: idemKey}, out)
}

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}
