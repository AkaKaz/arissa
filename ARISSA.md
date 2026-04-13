# Arissa — 仕様書

**Autonomous Resilient Infrastructure Setup & System Agent**

対象サーバーに systemd サービスとして常駐し、Slack からの自然言語指示を受けて Claude がシェルコマンドを実行するエージェント。Go 実装。

---

## 1. 概要

- 対象サーバー上で systemd サービスとして動作する単一プロセス
- Slack（Socket Mode）を UI とし、`app_mention` と DM を受ける
- Claude（Anthropic API）が tool_use でシェルコマンドを生成する
- `shell_exec` は実行前に Slack ボタンで operator 承認を取る
- 権限境界は `arissa` サービスユーザの groups と sudoers で OS が担保する

---

## 2. リポジトリ構成

```
arissa/
├── ARISSA.md
├── Makefile
├── version.mk
├── go.mod, go.sum
├── .gitignore
├── .devcontainer/devcontainer.json
├── .github/workflows/{ci.yml, release.yml}
├── .golangci.yml
├── cmd/arissa/main.go             エントリ
├── internal/
│   ├── config/config.go           /etc/arissa/config.toml 読み込み
│   ├── prompt/prompt.go           system.prompt.md + context/*.md + skills/*.md 合成
│   ├── slack/slack.go             Socket Mode ゲートウェイ
│   ├── agent/agent.go             Claude tool-use ループ (Session / SessionRegistry)
│   ├── tools/shell/shell.go       shell_exec
│   ├── tools/approval/approval.go Slack ボタン承認
│   └── version/version.go
├── defaults/{config.toml.default, system.prompt.md.default}
├── debian/{control, postinst}
└── systemd/arissa.service
```

---

## 3. 技術スタック

| レイヤー | 採用技術 |
|----------|---------|
| 言語 | Go 1.23 |
| LLM | `github.com/anthropics/anthropic-sdk-go` (Beta) / 既定モデル `claude-sonnet-4-20250514` |
| チャット UI | `github.com/slack-go/slack`（Socket Mode） |
| 設定 | `github.com/pelletier/go-toml/v2` |
| プロセス管理 | systemd (`Type=simple`) |
| パッケージング | Debian (`debian/control`, `debian/postinst`) |
| 開発環境 | Dev container (`mcr.microsoft.com/devcontainers/go:1-trixie`) |
| CI | GitHub Actions (`actions/setup-go@v5`) |
| Lint | `golangci-lint` |

---

## 4. モジュール

| モジュール | 責務 |
|-----------|------|
| `cmd/arissa/main.go` | config 読み込み → Anthropic / Slack / Session 起動。SIGINT/SIGTERM で shutdown broadcast |
| `internal/config` | `/etc/arissa/config.toml`（`ARISSA_CONFIG` で上書き可）を読む。必須項目（slack bot/app token、anthropic api key）が欠ければ `(nil, nil)` を返し、`main` が `os.Exit(0)` |
| `internal/prompt` | `prompt.system` / `prompt.context_dir` / `prompt.skills_dir` を起動時に合成。context は `<context name="…">…</context>`、skills は `<skill name="…">…</skill>` で囲む |
| `internal/agent` | Claude tool-use ループ。`Session` がユーザ毎の履歴（ローリング 20 ターン）を持つ。ループ上限は `agent.max_tool_iterations`（既定 10） |
| `internal/slack` | Socket Mode。`app_mention` と channel type `im` の `message` を処理。受信時 `:thumbsup:` リアクション、`!reset` でセッションクリア、返答は 3900 バイトでチャンク分割 |
| `internal/tools/shell` | `shell_exec` スキーマと実行・整形 |
| `internal/tools/approval` | Slack ボタン承認。5 分タイムアウト |
| `internal/version` | `go build -ldflags '-X'` で注入される版情報 |

---

## 5. `shell_exec` と承認フロー

- 単一ツール `shell_exec`。入力は `command`（必須）と `reason`（必須、承認プロンプトに表示）
- Claude の全ての `shell_exec` 呼び出しに対し `RequestApproval` が走る
- 承認メッセージは requester のスレッドに Approve / Deny ボタン付きで投稿される
- 承認権限: `allowed_user_ids` が空なら requester 本人のみ、設定があれば allowlist 内ユーザ
- 5 分で未決着ならタイムアウト扱い
- 承認時のみ `exec.CommandContext(ctx, "sh", "-c", command)` で実行、既定 30 秒タイムアウト、stdout/stderr は各 4000 バイトで truncate
- 拒否時は tool_result に拒否理由を返す。system プロンプトで「拒否されたらリトライせず operator に代替案を聞く」ことを規定

---

## 6. セキュリティ

### 6.1 OS レベル

- 専用システムユーザ `arissa` で稼働
- systemd: `NoNewPrivileges=yes`, `ProtectHome=yes`
- 実際に実行できるコマンドは `arissa` ユーザの groups と sudoers で決まる。allowlist はコード側に持たない

### 6.2 Slack レベル

- `slack.allowed_channel_ids` 空でなければ、リストに無いチャンネルは無視
- `slack.allowed_user_ids` 空でなければ、リストに無いユーザには拒否応答
- 承認ボタンは決定者をチェック（requester 本人 or allowlist 内ユーザ）

### 6.3 シークレット

- Slack bot/app トークン、Anthropic API キーは `/etc/arissa/config.toml`（`root:arissa 0640` 想定）

---

## 7. 設定ファイル

### 7.1 `/etc/arissa/config.toml`

```toml
[slack]
bot_token = ""
app_token = ""
allowed_channel_ids = []
allowed_user_ids = []

[anthropic]
api_key = ""
model = "claude-sonnet-4-20250514"

[agent]
name = "arissa"
max_tool_iterations = 10

[prompt]
system = "/etc/arissa/system.prompt.md"
context_dir = "/etc/arissa/context"
skills_dir = "/etc/arissa/skills"
```

### 7.2 プロンプト関連

- `/etc/arissa/system.prompt.md` — ベースのペルソナ・運用ルール
- `/etc/arissa/context/*.md` — 起動時に読まれ `<context name="…">…</context>` として合成
- `/etc/arissa/skills/*.md` — 起動時に読まれ `<skill name="…">…</skill>` として合成

operator は再起動でプロンプトを差し替えられる。

---

## 8. ビルド・配布

### 8.1 Go によるシングルバイナリ

- `go build` で pure Go バイナリを生成（`CGO_ENABLED=0`）
- 期待サイズ: 約 15〜20 MB（Bun `--compile` の 97 MB と比較して 1/5）
- deb サイズ: 約 5〜8 MB

### 8.2 開発環境

- Dev container (`mcr.microsoft.com/devcontainers/go:1-trixie`)
- 配布先の Debian trixie と同じベース、`dpkg-deb` 標準搭載
- `github-cli` feature で `gh` を追加

### 8.3 CI

- `actions/setup-go@v5` による軽量な build ジョブ
- `deb-install-test` ジョブで `debian:trixie` への `dpkg -i` と `systemd-analyze verify`
- `golangci-lint-action` による lint
- リリースは `v*` タグ push で `softprops/action-gh-release`

### 8.4 バージョン採番

- `version.mk` が `git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --dirty` からバージョンを導出
- 初回 tag 前は `0.0.0-dev.<sha>`
- `go build -ldflags '-X arissa/internal/version.Version=...'` で埋め込み

---

## 9. 運用

- Debian パッケージ経由でインストール。`postinst` が `arissa` ユーザと必要ディレクトリを作る
- `systemctl start arissa` で起動
- 必須 config 欠落時は `os.Exit(0)` で restart ループを避ける
- 起動時に allowed channel へ `online`、停止時に `shutting down` を broadcast
- operator は Slack でメンションまたは DM、`!reset` でセッションをクリア
