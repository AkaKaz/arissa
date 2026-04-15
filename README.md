# Arissa

**Autonomous Resilient Infrastructure Setup & System Agent**

Slack から自然言語で指示を受け、Claude がシェルコマンドを生成・実行する Go 製 systemd 常駐エージェント。全コマンドは Slack ボタンで operator 承認を取ってから実行する。

## 特徴

- **Slack (Socket Mode) 駆動** — `app_mention` と DM を受けて Anthropic Beta Messages API の tool-use ループで応答
- **承認付き `shell_exec`** — 全コマンドを Slack の Approve / Deny ボタンでゲート (5 分タイムアウト、実行は 30 秒タイムアウト)
- **Memory tool** — Anthropic 公式 `memory_20250818` を filesystem backend で実装。`/memories` を deployment 全体で共有
- **プロンプト合成** — `system.prompt.md` に `context/*.md` と `skills/*.md` を起動時に合成し、再起動で挙動を差し替え可能
- **OS で権限境界** — 実行可能コマンドは `arissa` ユーザの groups と sudoers で OS が担保。コード側に allowlist を持たない

## インストール

```sh
sudo dpkg -i arissa_<version>_amd64.deb
sudoedit /etc/arissa/config.toml   # slack tokens, anthropic api key
sudo systemctl start arissa
```

`postinst` が `arissa` システムユーザと `/var/lib/arissa/memories` などの必要ディレクトリを作成する。Slack / Anthropic API への HTTPS 接続に必要なため、`ca-certificates` に依存する (dpkg が自動で導入)。

## 設定

`/etc/arissa/config.toml` の最小構成:

```toml
[slack]
bot_token = "xoxb-..."
app_token = "xapp-..."
allowed_channel_ids = []   # 空なら全チャンネル許可
allowed_user_ids    = []   # 空なら全ユーザ許可

[anthropic]
api_key = "sk-ant-..."
model   = "claude-sonnet-4-20250514"
```

詳細は [ARISSA.md](ARISSA.md) を参照。

## 使い方

Slack で bot をメンションするか DM を送る:

```
@arissa disk usage 教えて
```

`shell_exec` が呼ばれるとスレッドに承認ボタンが出る。Approve で実行、Deny で拒否理由が Claude に返り、リトライせず代替案を聞いてくる。

`!reset` でセッション履歴をクリア。

## 開発

```sh
make build         # bin/arissa にビルド
make lint          # golangci-lint
make deb           # Debian パッケージ生成
```

Dev container (`mcr.microsoft.com/devcontainers/go:1-trixie`) を同梱。配布先 Debian trixie と同じベース。

## アーキテクチャ・運用詳細

[ARISSA.md](ARISSA.md) に仕様書を置いている。モジュール構成、承認フロー、memory tool の挙動、systemd hardening、CI 構成などを記載。

## ライセンス

[MIT](LICENSE)
