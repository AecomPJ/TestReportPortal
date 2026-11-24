package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
)

const (
	idempotencyHeader = "Idempotency-Key"
	// Keys are kept for this long; a retry after that creates a new record.
	idempotencyRetention = "7 days"
	// A key left "in progress" longer than this (e.g. the API crashed) can be taken over by a retry.
	idempotencyStale = "2 minutes"
)

const idempotencyContextKey = "idempotency_claim"

// idempotencyClaim is the key claimed by the middleware for the current request.
type idempotencyClaim struct {
	scope, key, hash string
}

// completeIdempotency stores the response for the claimed key inside the handler's transaction, so the
// created record and its replay are committed together (or not at all) before the response is written.
// Without an Idempotency-Key it does nothing.
func completeIdempotency(ctx context.Context, tx pgx.Tx, c echo.Context, status int, body []byte) error {
	claim, ok := c.Get(idempotencyContextKey).(*idempotencyClaim)
	if !ok {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE idempotency_keys SET status_code=$4,response_body=$5,content_type=$6
		WHERE scope=$1 AND key=$2 AND request_hash=$3 AND status_code IS NULL`,
		claim.scope, claim.key, claim.hash, status, body, echo.MIMEApplicationJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return echo.NewHTTPError(409, "Idempotency-Keyの処理が別のリクエストに引き継がれました。再試行してください")
	}
	return nil
}

// writeField adds an unambiguous, length-prefixed component to the fingerprint.
func writeField(h hash.Hash, tag string, value []byte) {
	var n [8]byte
	h.Write([]byte(tag))
	binary.BigEndian.PutUint64(n[:], uint64(len(value)))
	h.Write(n[:])
	h.Write(value)
}

// requestFingerprint hashes what the client meant to send. Multipart bodies are hashed by their
// parsed fields and file contents because clients usually pick a new random boundary on every retry.
func requestFingerprint(c echo.Context) (string, error) {
	h := sha256.New()
	r := c.Request()
	writeField(h, "route", []byte(r.Method+" "+c.Path()))
	if strings.HasPrefix(r.Header.Get(echo.HeaderContentType), echo.MIMEMultipartForm) {
		form, err := c.MultipartForm()
		if err != nil {
			return "", echo.NewHTTPError(400, "multipartの形式が不正です")
		}
		names := make([]string, 0, len(form.Value))
		for k := range form.Value {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			for _, v := range form.Value[k] {
				writeField(h, "value-name", []byte(k))
				writeField(h, "value", []byte(v))
			}
		}
		names = names[:0]
		for k := range form.File {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			for _, f := range form.File[k] {
				writeField(h, "file-name", []byte(k))
				writeField(h, "file-filename", []byte(f.Filename))
				src, err := f.Open()
				if err != nil {
					return "", err
				}
				var n [8]byte
				binary.BigEndian.PutUint64(n[:], uint64(f.Size))
				h.Write([]byte("file-content"))
				h.Write(n[:])
				written, err := io.Copy(h, src)
				src.Close()
				if err != nil {
					return "", err
				}
				if written != f.Size {
					return "", echo.NewHTTPError(400, "添付ファイルを読み取れませんでした")
				}
			}
		}
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return "", err
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		writeField(h, "body", body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// idempotency makes a creating endpoint safe to retry. With an Idempotency-Key header, the first
// successful response is stored (by the handler, atomically with the record it creates) and replayed
// for later requests with the same key and body.
// The same key with a different body is rejected with 409. Failed requests release the key.
// Keys are scoped by route and path parameters (e.g. per app for runs, per result for attachments).
func (s *server) idempotency(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		key := c.Request().Header.Get(idempotencyHeader)
		if key == "" {
			return next(c)
		}
		if len(key) > 255 {
			return echo.NewHTTPError(400, "Idempotency-Keyは255文字以内にしてください")
		}
		for _, ch := range key {
			if ch < 0x21 || ch > 0x7e {
				return echo.NewHTTPError(400, "Idempotency-Keyは空白を含まないASCII文字で指定してください")
			}
		}
		hash, err := requestFingerprint(c)
		if err != nil {
			return err
		}
		scope := c.Request().Method + " " + c.Path()
		for _, name := range c.ParamNames() {
			scope += " " + name + "=" + c.Param(name)
		}
		ctx := c.Request().Context()
		if _, err = s.db.Exec(ctx, "DELETE FROM idempotency_keys WHERE created_at < now() - $1::interval", idempotencyRetention); err != nil {
			return err
		}
		// Claim the key (or take over a stale in-progress claim with the same body).
		var claimed bool
		err = s.db.QueryRow(ctx, `INSERT INTO idempotency_keys(scope,key,request_hash) VALUES($1,$2,$3)
			ON CONFLICT (scope,key) DO UPDATE SET created_at=now()
			WHERE idempotency_keys.status_code IS NULL AND idempotency_keys.request_hash=EXCLUDED.request_hash
			  AND idempotency_keys.created_at < now() - $4::interval
			RETURNING true`, scope, key, hash, idempotencyStale).Scan(&claimed)
		if errors.Is(err, pgx.ErrNoRows) {
			var storedHash, contentType string
			var status *int
			var body []byte
			if err = s.db.QueryRow(ctx, "SELECT request_hash,status_code,response_body,content_type FROM idempotency_keys WHERE scope=$1 AND key=$2", scope, key).Scan(&storedHash, &status, &body, &contentType); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return echo.NewHTTPError(409, "同じIdempotency-Keyのリクエストが競合しました。再試行してください")
				}
				return err
			}
			if storedHash != hash {
				return echo.NewHTTPError(409, "このIdempotency-Keyは異なる内容のリクエストで使用済みです")
			}
			if status == nil {
				return echo.NewHTTPError(409, "同じIdempotency-Keyのリクエストを処理中です。しばらくして再試行してください")
			}
			c.Response().Header().Set("Idempotent-Replayed", "true")
			return c.Blob(*status, contentType, body)
		}
		if err != nil {
			return err
		}
		c.Set(idempotencyContextKey, &idempotencyClaim{scope: scope, key: key, hash: hash})
		herr := next(c)
		// A successful handler stored its response in the same transaction as its record (completeIdempotency).
		// Any claim still incomplete means nothing was committed, so release it for a retry. This must run even
		// if the client has disconnected, hence the context without cancellation.
		if _, err = s.db.Exec(context.WithoutCancel(ctx), "DELETE FROM idempotency_keys WHERE scope=$1 AND key=$2 AND status_code IS NULL", scope, key); err != nil {
			c.Logger().Error(err)
		}
		return herr
	}
}
