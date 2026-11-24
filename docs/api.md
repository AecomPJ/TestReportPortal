# API（初版）

ベースパス `/api`。IDは整数。日時はRFC3339。エラーは `{ "message": "..." }`。
JSON入力は未知の項目と複数JSONを拒否します。

| メソッド | パス | 用途 |
|---|---|---|
| GET | /health | DB接続を含むヘルスチェック |
| GET / POST | /apps | アプリ一覧／作成 |
| GET / POST | /apps/:app/cases | ケースと直近2回の結果／作成 |
| PUT | /cases/:id | ケース定義を更新（全入力項目指定） |
| GET | /cases/:id/results | 過去の実行結果 |
| GET | /cases/:id/history | ケース編集履歴 |
| GET / POST | /apps/:app/runs | 実行一覧／作成 |
| GET | /runs/:id | 環境・結果・スナップショット・添付の詳細 |
| PUT | /results/:id | 結果を更新（全入力項目指定） |
| GET | /results/:id/history | 結果編集履歴 |
| POST | /results/:id/restore/:history | 選択した履歴のafter_dataに復元 |
| POST | /results/:id/attachments | multipartのfileに画像・ログ等を指定 |
| GET | /attachments/:id | 添付本体（画像はinline、それ以外はダウンロード） |

作成例（IDは実際の応答値を使う）：

```sh
curl -H 'Content-Type: application/json' -d '{"name":"対象アプリ","package_name":"com.example.app"}' http://127.0.0.1:3000/api/apps
curl -H 'Content-Type: application/json' -d '{"case_key":"LOGIN-001","category":"認証","title":"ログイン","steps":"ログインボタンを押す","expected_result":"ホーム画面が開く","test_type":"both"}' http://127.0.0.1:3000/api/apps/1/cases
curl -H 'Content-Type: application/json' -d '{"run_type":"automated","runner_name":"Maestro","case_ids":[1],"app_version":"1.0.0","device_name":"Pixel","android_version":"15","executed_by":"CI","started_at":"2026-11-07T01:00:00Z"}' http://127.0.0.1:3000/api/apps/1/runs
curl http://127.0.0.1:3000/api/runs/1
curl -X PUT -H 'Content-Type: application/json' -d '{"status":"passed","actual_result":"ホーム画面を表示","comment":"","changed_by":"CI"}' http://127.0.0.1:3000/api/results/1
curl -F file=@screenshot.png http://127.0.0.1:3000/api/results/1/attachments
```

- `test_type`: `manual` / `automated` / `both`
- `run_type`: `manual` / `automated`
- `status`: `not_run` / `passed` / `failed` / `blocked`
- ケースの `is_active=false` で無効化。既存結果には影響しない。
- 実行には重複のない有効な `case_ids` を1件以上指定。異なるアプリのケースは拒否。
- `started_at` 省略時はサーバー現在時刻。`finished_at` は任意で、開始以降に限定。
- `changed_by`、`reason` はケース・結果の編集と結果の復元で任意指定。
- 添付は `file` が必須、`uploaded_by`、`caption` は任意。
- 結果の復元は編集可能な状態・実際の結果・コメントのみ。実行時スナップショットや添付は変えない。
- 同一レコードの更新はDBロックで直列化。画面で長時間開いた編集の競合検出は未実装。
- 自動連携は実行作成 → 応答IDで詳細取得 → 各結果更新 → 画像添付の順。
  一括インポートの冪等性キーやMaestro形式変換は今後追加する。

## 添付ファイルの形式

| 種別 | 判定 | 保存されるMIME | 上限 |
|---|---|---|---|
| 画像 | 内容がPNG/JPEG/WebP | `image/png` など | 10MB |
| テキスト（logcat等） | 拡張子 `.txt` / `.log` かつ内容がテキスト | `text/plain; charset=utf-8` | 20MB |
| JSON | 拡張子 `.json` かつ正しいJSON | `application/json` | 20MB |
| XML（JUnit XML等） | 拡張子 `.xml` かつ内容がテキスト | `application/xml` | 20MB |
| ZIP | 拡張子 `.zip` かつ内容がZIP | `application/zip` | 50MB |
| 動画（画面録画） | 拡張子 `.mp4` かつ内容がMP4 | `video/mp4` | 100MB |

- 上記以外（HTML、実行ファイル等）は400、上限超過は413。空ファイルは400。
- MIMEはクライアント申告ではなく内容と拡張子からサーバーが決める。HTMLとして保存・配信されることはない。
- `GET /attachments/:id` は画像のみ `Content-Disposition: inline`、それ以外は `attachment`。
  `X-Content-Type-Options: nosniff` とサンドボックス化したCSPを付ける。
- 画面では画像はサムネイル、テキスト系はプレビュー（先頭20万文字）とダウンロード、MP4はプレーヤーで表示する。

```sh
curl -F file=@logcat.txt -F caption=logcat http://127.0.0.1:3000/api/results/1/attachments
curl -F file=@report.xml http://127.0.0.1:3000/api/results/1/attachments
```
