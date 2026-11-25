// Command ledger-mcp is a stdio MCP server that lets AI agents record Android test results in the
// Test Report Portal through a small set of safe tools. Destructive operations (delete, restore,
// arbitrary edits by ID) are intentionally not exposed.
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "0.1.0"

func loadConfig() config {
	cfg := config{
		ledgerURL: os.Getenv("LEDGER_URL"),
		actor:     os.Getenv("LEDGER_ACTOR"),
	}
	if cfg.ledgerURL == "" {
		cfg.ledgerURL = "http://127.0.0.1:3000"
	}
	if cfg.actor == "" {
		cfg.actor = "AI agent (MCP)"
	}
	dirs := os.Getenv("LEDGER_ATTACHMENT_DIRS")
	if dirs == "" {
		// Default: the directory the agent was started in, plus the system temp directory.
		wd, _ := os.Getwd()
		dirs = wd + string(os.PathListSeparator) + os.TempDir()
	}
	for _, d := range filepath.SplitList(dirs) {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		}
		cfg.attachmentDirs = append(cfg.attachmentDirs, d)
	}
	return cfg
}

func newServer(cfg config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "test-report-portal", Title: "Android Test Report Portal", Version: version}, &mcp.ServerOptions{
		Instructions: "Androidテスト結果をポータルに記録するためのツール。手順: list_apps / list_cases で対象を確認 → 必要なら upsert_case でケースを同期 → " +
			"start_run で実行回を作成（端末情報を渡す）→ ケースごとに record_result で結果と証跡（スクリーンショット・logcat等）を記録 → finish_run で終了。" +
			"ケースは case_key、結果は run_id + case_key で指定する。",
	})
	t := &tools{cfg: cfg, api: newLedgerClient(cfg.ledgerURL)}
	t.register(s)
	return s
}

func main() {
	log.SetOutput(os.Stderr) // stdout is reserved for the MCP protocol
	cfg := loadConfig()
	if err := newServer(cfg).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
