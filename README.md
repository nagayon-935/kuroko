# kuroko

[![CI](https://github.com/nagayon-935/kuroko/actions/workflows/ci.yml/badge.svg)](https://github.com/nagayon-935/kuroko/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/nagayon-935/kuroko/branch/main/graph/badge.svg)](https://codecov.io/gh/nagayon-935/kuroko)
[![Go Report Card](https://goreportcard.com/badge/github.com/nagayon-935/kuroko)](https://goreportcard.com/report/github.com/nagayon-935/kuroko)
[![Release](https://img.shields.io/github/v/release/nagayon-935/kuroko)](https://github.com/nagayon-935/kuroko/releases)

ターミナル上の作業ログ（コマンドと出力結果）を自動保存する CLI ツール。  
`ssh` や `screen` などのコマンドをラップして、セッションを透過的に記録します。

## インストール

### 必要なもの

- Go 1.22 以上

### ビルド

```bash
git clone https://github.com/ryu/kuroko
cd kuroko
make install
```

`~/.local/bin/kuroko` にバイナリが配置されます。  
PATH に含まれていない場合は以下を `~/.zshrc` または `~/.bashrc` に追加してください。

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## 使い方

```
kuroko [options] <command> [args...]
```

コマンドの前に `kuroko` を付けるだけで、セッション中の全出力がログファイルに保存されます。

```bash
# SSH 接続
kuroko ssh user@hostname
kuroko ssh -p 2222 user@hostname

# シリアル接続
kuroko screen /dev/ttyUSB0 115200

# シェルごと録画
kuroko bash
```

接続終了と同時にログ保存も完了します。

## オプション

| オプション | 短縮形 | 説明 |
|-----------|--------|------|
| `--log-dir <dir>` | `-d <dir>` | ログの保存先を指定（最優先） |
| `--help` | `-h` | ヘルプを表示 |
| `--version` | `-v` | バージョンを表示 |

```bash
# このセッションだけ別の場所に保存
kuroko -d ~/work/logs ssh user@hostname
```

## ログファイル

### 保存場所

```
~/.config/kuroko/logs/
```

### ファイル名の形式

接続日時と接続先が自動でファイル名に含まれます。

```
20260617_180000_ssh_user@hostname.log
20260617_190000_screen_ttyUSB0.log
20260617_200000_bash.log
```

### ログ一覧の確認

```bash
kuroko logs
```

### ログの閲覧 (TUI ビューア)

保存されたログファイルを TUI (Terminal User Interface) で見やすく閲覧できます。  
実行したコマンドの一覧（タイムライン）と、そのコマンドの出力結果を左右分割画面で確認できます。

```bash
kuroko view <path_to_log_file>
```

#### キー操作
- `j` / `k` または `↓` / `↑` : コマンドの選択移動
- `/` : 実行コマンドの検索モード（キーワード入力後 Enter で確定、Esc で検索解除）
- `q` / `Esc` : ビューアの終了

### ログの中身

```
# kuroko session log
# Started : 2026-06-17T18:00:00+09:00
# Command : ssh user@hostname
# --------------------------------------------------------------------

（セッション中の全出力）

# --------------------------------------------------------------------
# Ended   : 2026-06-17T19:30:00+09:00
# Exit    : 0
```

## 設定ファイル

`~/.config/kuroko/config.json` を作成すると、起動のたびに設定が読み込まれます。

```json
{
  "log_dir": "/path/to/your/logs",
  "notifier": {
    "type": "none",
    "webhook_url": ""
  }
}
```

### 設定の優先順位

```
--log-dir フラグ  >  $KUROKO_LOG_DIR 環境変数  >  config.json  >  デフォルト
```

## 環境変数

| 変数名 | 説明 |
|--------|------|
| `KUROKO_LOG_DIR` | ログの保存先 |
| `KUROKO_NOTIFIER` | 通知タイプ: `none` / `discord` / `slack` |
| `KUROKO_WEBHOOK_URL` | Discord または Slack の Webhook URL |

## 通知機能（Discord / Slack）

セッション終了時にログを自動送信できます。

### Discord

```bash
export KUROKO_NOTIFIER=discord
export KUROKO_WEBHOOK_URL=https://discord.com/api/webhooks/...
kuroko ssh user@hostname
```

または `config.json` に記載：

```json
{
  "notifier": {
    "type": "discord",
    "webhook_url": "https://discord.com/api/webhooks/..."
  }
}
```

### Slack

```json
{
  "notifier": {
    "type": "slack",
    "webhook_url": "https://hooks.slack.com/services/..."
  }
}
```

## ディレクトリ構造

```
kuroko/
├── cmd/kuroko/main.go          # エントリポイント
├── internal/
│   ├── config/config.go        # 設定管理
│   ├── logger/logger.go        # ログファイル生成
│   ├── session/session.go      # PTY セッション制御
│   ├── notifier/notifier.go    # 外部通知（Discord / Slack）
│   └── viewer/viewer.go        # TUI ログビューア
├── go.mod
└── Makefile
```

## 仕組み

PTY（疑似端末）を使い、対象コマンドとユーザー端末の間に透過的に割り込みます。  
ターミナルのリサイズや Ctrl+C などの操作も正常に動作します。

```
ユーザー端末 ←→ kuroko (PTY master) ←→ ssh / screen / bash
                      ↓
                  ログファイル
```
