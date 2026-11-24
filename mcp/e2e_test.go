package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestWorkflow runs the agent workflow against a running portal.
//
//	LEDGER_URL=http://127.0.0.1:3000 go test ./...
func TestWorkflow(t *testing.T) {
	base := os.Getenv("LEDGER_URL")
	if base == "" {
		t.Skip("LEDGER_URL が未設定のためスキップ（起動済みのポートに対して実行する）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	cfg := config{ledgerURL: base, actor: "mcp e2e", attachmentDirs: []string{mustReal(t, dir)}}
	session := connect(t, ctx, newServer(cfg))

	// Create an isolated app through the REST API (the MCP server intentionally cannot create apps).
	api := newLedgerClient(base)
	var a app
	name := fmt.Sprintf("Smoke MCP %d", time.Now().UnixNano())
	if err := api.send(ctx, "POST", "/apps", map[string]string{"name": name}, "", &a); err != nil {
		t.Fatal(err)
	}
	appRef := fmt.Sprint(a.ID)

	call(t, ctx, session, "upsert_case", map[string]any{"app": appRef, "case_key": "LOGIN-001", "title": "ログイン", "steps": "ログインを押す", "expected_result": "ホーム画面"}, false)
	call(t, ctx, session, "upsert_case", map[string]any{"app": name, "case_key": "LOGOUT-001", "title": "ログアウト", "steps": "ログアウトを押す", "expected_result": "ログイン画面"}, false)
	cases := call(t, ctx, session, "list_cases", map[string]any{"app": appRef}, false)
	if n := len(cases["cases"].([]any)); n != 2 {
		t.Fatalf("cases=%d", n)
	}
	// Validation errors come back as tool errors the agent can read.
	msg := callErr(t, ctx, session, "upsert_case", map[string]any{"app": appRef, "case_key": "X", "title": "x", "steps": "s", "expected_result": "e", "test_type": "robot"})
	if !strings.Contains(msg, "test_type") {
		t.Fatal(msg)
	}
	msg = callErr(t, ctx, session, "start_run", map[string]any{"app": appRef, "case_keys": []string{"NOPE-1"}})
	if !strings.Contains(msg, "NOPE-1") {
		t.Fatal(msg)
	}

	start := map[string]any{"app": appRef, "case_keys": []string{"LOGIN-001", "LOGOUT-001"}, "device_name": "Pixel 8", "android_version": "15", "app_version": "1.2.3", "runner_name": "Claude Code", "idempotency_key": "e2e-" + name}
	started := call(t, ctx, session, "start_run", start, false)
	again := call(t, ctx, session, "start_run", start, false)
	if started["run_id"] != again["run_id"] {
		t.Fatalf("idempotent start_run created two runs: %v %v", started["run_id"], again["run_id"])
	}
	runID := started["run_id"]

	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=")
	shot := filepath.Join(dir, "login.png")
	logcat := filepath.Join(dir, "logcat.txt")
	os.WriteFile(shot, png, 0600)
	os.WriteFile(logcat, []byte("E/AndroidRuntime: FATAL EXCEPTION\n"), 0600)
	rec := map[string]any{"run_id": runID, "case_key": "LOGIN-001", "status": "failed", "actual_result": "クラッシュ", "comment": "再現率100%",
		"attachments": []map[string]any{{"path": shot, "caption": "ログイン画面"}, {"path": logcat}}}
	res := call(t, ctx, session, "record_result", rec, false)
	if len(res["uploaded_attachments"].([]any)) != 2 {
		t.Fatal(res)
	}
	call(t, ctx, session, "record_result", rec, false) // retry: attachments are not duplicated
	call(t, ctx, session, "record_result", map[string]any{"run_id": runID, "case_key": "LOGOUT-001", "status": "passed"}, false)
	msg = callErr(t, ctx, session, "record_result", map[string]any{"run_id": runID, "case_key": "LOGIN-001", "status": "ok"})
	if !strings.Contains(msg, "status") {
		t.Fatal(msg)
	}
	msg = callErr(t, ctx, session, "record_result", map[string]any{"run_id": runID, "case_key": "LOGIN-001", "status": "passed", "attachments": []map[string]any{{"path": "/etc/passwd"}}})
	if !strings.Contains(msg, "LEDGER_ATTACHMENT_DIRS") {
		t.Fatal(msg)
	}

	done := call(t, ctx, session, "finish_run", map[string]any{"run_id": runID}, false)
	if done["finished_at"] == nil || !strings.Contains(done["summary"].(string), "失敗1") {
		t.Fatal(done)
	}
	detail := call(t, ctx, session, "get_run", map[string]any{"run_id": runID}, false)
	for _, r := range detail["results"].([]any) {
		row := r.(map[string]any)
		if row["case_key"] == "LOGIN-001" {
			// Failed validations must not have changed the result; the retry must not duplicate files.
			if row["status"] != "failed" || len(row["attachments"].([]any)) != 2 {
				t.Fatalf("unexpected result: %v", row)
			}
		}
	}
	hist := call(t, ctx, session, "get_case_history", map[string]any{"app": appRef, "case_key": "LOGIN-001"}, false)
	if hist["total_results"].(float64) != 1 {
		t.Fatal(hist)
	}
}

func mustReal(t *testing.T, p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func connect(t *testing.T, ctx context.Context, s *mcp.Server) *mcp.ClientSession {
	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "e2e"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callRaw(t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func call(t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args any, _ bool) map[string]any {
	t.Helper()
	res := callRaw(t, ctx, s, name, args)
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("%s failed: %s", name, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: %v: %s", name, err, text)
	}
	return out
}

func callErr(t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args any) string {
	t.Helper()
	res := callRaw(t, ctx, s, name, args)
	text := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError {
		t.Fatalf("%s should fail: %s", name, text)
	}
	return text
}
