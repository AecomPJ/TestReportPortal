# AIエージェント向け MCP サーバー

`mcp/` は、AIエージェント（Claude Code など）が Android 実機テストの結果をポータルに記録するための
[MCP](https://modelcontextprotocol.io/) サーバーです。stdio で動き、ポータルの REST API を呼び出します。

エージェントに REST API を直接叩かせる代わりに、操作を「参照・ケース同期・実行回作成・結果記録・終了」に絞って公開します。
アプリの作成、ID 指定の任意編集、結果の復元、削除などは公開しません。

## ビルド

Go 1.25 以上。

```sh
cd mcp
go build -o ../.local/bin/ledger-mcp .
```

## 導入（Claude Code）

ポータルを起動した状態で、テストを行うプロジェクトの `.mcp.json` に追加します（パスは環境に合わせて変更）。

```json
{
  "mcpServers": {
    "test-report-portal": {
      "command": "/path/to/TestReportPortal/.local/bin/ledger-mcp",
      "env": {
        "LEDGER_URL": "http://127.0.0.1:3000",
        "LEDGER_ACTOR": "Claude Code",
        "LEDGER_ATTACHMENT_DIRS": "/path/to/your-app/.artifacts:/tmp"
      }
    }
  }
}
```

ビルドせずに `go run` で起動することもできます（起動は遅くなります）。

```json
{
  "mcpServers": {
    "test-report-portal": {
      "command": "go",
      "args": ["run", "/path/to/TestReportPortal/mcp"],
      "env": { "LEDGER_URL": "http://127.0.0.1:3000" }
    }
  }
}
```

コマンドラインからは `claude mcp add test-report-portal --env LEDGER_URL=http://127.0.0.1:3000 -- /path/to/ledger-mcp` でも登録できます。

| 環境変数 | 既定値 | 内容 |
|---|---|---|
| `LEDGER_URL` | `http://127.0.0.1:3000` | ポータルのURL（`/api` は付けない） |
| `LEDGER_ACTOR` | `AI agent (MCP)` | 編集履歴の `changed_by`、実行回の `executed_by`、添付の `uploaded_by` |
| `LEDGER_ATTACHMENT_DIRS` | 起動ディレクトリとOSの一時ディレクトリ | 添付を許可するディレクトリ（`:` 区切り）。外のファイルはアップロードしない |

## ツール

| ツール | 内容 |
|---|---|
| `list_apps()` | アプリ一覧 |
| `list_cases(app, include_inactive?)` | ケース一覧と直近2回の結果 |
| `upsert_case(app, case_key, title, steps, expected_result, test_type?, category?, precondition?, sort_order?, is_active?, reason?)` | テスト定義からケースを同期（なければ作成、変更なしなら何もしない） |
| `start_run(app, case_keys, device_name?, android_version?, app_version?, runner_name?, run_type?, notes?, started_at?, idempotency_key?)` | 実行回を作成して `run_id` を返す。端末情報は呼び出し側で取得して渡す |
| `record_result(run_id, case_key, status, actual_result?, comment?, attachments?, reason?)` | 結果を記録し、ローカルファイル（`[{path, caption?}]`）を添付 |
| `finish_run(run_id, finished_at?, summary?)` | 終了日時を記録し、件数を集計（`summary` 省略時は自動生成） |
| `get_run(run_id)` | 実行回の環境情報・結果・添付 |
| `get_case_history(app, case_key, limit?)` | ケースの過去結果（新しい順） |

- `app` はアプリID・アプリ名・パッケージ名のいずれかで指定します。
- `status` は `passed` / `failed` / `blocked` / `not_run`。
- `record_result` の `actual_result` / `comment` は省略すると現状維持です。
- 添付は画像（PNG/JPEG/WebP）、`.txt` / `.log`、`.json`、`.xml`、`.zip`、`.mp4`。形式・サイズの最終判定はポータルが行います。

## 典型的な流れ

```text
list_cases(app="com.example.app")
start_run(app="com.example.app", case_keys=["LOGIN-001","LOGOUT-001"],
          device_name="Pixel 8", android_version="15", app_version="1.2.3", runner_name="Claude Code + adb")
  → run_id=42
record_result(run_id=42, case_key="LOGIN-001", status="failed", actual_result="ログイン後にクラッシュ",
              attachments=[{"path":".artifacts/login.png"},{"path":".artifacts/logcat.txt","caption":"logcat"}])
record_result(run_id=42, case_key="LOGOUT-001", status="passed")
finish_run(run_id=42)
```

## リトライと重複防止

- ポータルへの通信は、接続エラーや5xxの場合に安全なリクエストのみ自動で再送します（GET・PUT・PATCH・冪等性キー付きPOST）。
- `start_run` に同じ `idempotency_key` を渡して呼び直すと、同じ実行回が返り重複作成されません。
- `record_result` の添付は「実行回・ケース・ファイル名・キャプション・内容」から冪等性キーを作るため、
  同じ引数で再実行しても添付は重複しません。添付の途中で失敗した場合もそのまま再実行できます。

## エラー

入力検証エラーやポータルのエラーは、ツールのエラー結果（`isError: true`）として日本語のメッセージで返します。
例: `status は passed / failed / blocked / not_run のいずれかを指定してください（指定値: "ok"）`、
`ポータルAPIがエラーを返しました (404 PATCH /runs/42/results/by-key/LOGIN-009): この実行回にケース「LOGIN-009」は含まれていません`。

## 検証

起動済みのポータルに対して、ケース同期 → 実行回作成 → 結果登録 → 画像・ログ添付 → 終了を通しで確認します
（`Smoke MCP ...` アプリを追加します）。

```sh
cd mcp
LEDGER_URL=http://127.0.0.1:3000 go test ./...
```

## セキュリティ上の注意

ポータル自体には認証がありません。MCP サーバーは公開する操作を絞る入口であり、ポータルへのアクセス制御の代わりにはなりません。
ポータルはローカルまたは信頼できるネットワーク内でのみ使用してください。
