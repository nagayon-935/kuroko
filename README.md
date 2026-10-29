# kuroko

[![CI](https://github.com/nagayon-935/kuroko/actions/workflows/ci.yml/badge.svg)](https://github.com/nagayon-935/kuroko/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/kuroko/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/kuroko?branch=main)
[![Release](https://img.shields.io/github/v/release/nagayon-935/kuroko)](https://github.com/nagayon-935/kuroko/releases)

ターミナル上の作業ログ（コマンドと出力結果）を自動保存する CLI ツール。  
`ssh` や `screen` などのコマンドをラップして、セッションを透過的に記録します。

## インストール

### Releases からインストール（推奨）

[Releases](https://github.com/nagayon-935/kuroko/releases) から Linux / macOS 向けのビルド済みバイナリ（`tar.gz`）を取得できます（amd64 / arm64 対応）。

```bash
# OS・アーキテクチャを判定し、最新リリースの該当アーカイブを取得
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; esac
URL=$(curl -fsSL https://api.github.com/repos/nagayon-935/kuroko/releases/latest \
  | grep -o "https://[^\"]*_${OS}_${ARCH}.tar.gz")

curl -fLO "$URL"
tar xzf "kuroko_"*"_${OS}_${ARCH}.tar.gz" kuroko

mkdir -p ~/.local/bin
mv kuroko ~/.local/bin/
chmod +x ~/.local/bin/kuroko
```

PATH に含まれていない場合は以下を `~/.zshrc` または `~/.bashrc` に追加してください。

```bash
export PATH="$HOME/.local/bin:$PATH"
```

インストール確認:

```bash
kuroko --version
```

### ソースからビルドする場合

必要なもの: Go 1.22 以上

```bash
git clone https://github.com/nagayon-935/kuroko
cd kuroko
make install
```

`~/.local/bin/kuroko` にバイナリが配置されます。PATH の設定は上記と同様です。

## 使い方

```
kuroko [options] <command> [args...]
```

引数なしで `kuroko` を起動すると、`ssh` / `screen` を矢印キーで選び、Enter で確定できます（`kuroko -d ~/work/logs` のようにログ保存先だけ指定することもできます）。

- **ssh**: `~/.ssh/config` と `Include` 先から、ワイルドカード・否定パターンを除いた `Host` エイリアスを文字列の昇順（大文字・小文字を区別しない）で一覧表示します。選択中の Host ブロックの設定内容とファイル位置をプレビューに表示します。条件付きブロックの候補も表示し、接続設定の評価は SSH に任せます。
- **screen**: USB シリアル機器のキャラクターデバイス（macOS の `/dev/tty.usb*`、Linux の `/dev/ttyUSB*`・`/dev/ttyACM*`）のみを一覧表示します。該当する機器がなければ、起動時は SSH の Host 選択へ直接進みます。デバイスの手入力と通信速度の指定が可能です（既定: `115200`）。

文字を入力すると、大文字・小文字を区別しないあいまい検索で候補を絞り込みます。`m`・`b`・`q` を含め、通常の文字はすべて検索文字として入力できます。Ctrl+U で検索文字を消去し、矢印キーで移動、Enter で確定します。

操作は Ctrl+N（手入力）・Ctrl+B（戻る）・Ctrl+C（中止）で、Enter は不要です。手入力中も Ctrl+B で戻り、Ctrl+C で中止できます。プレビューは PageUp / PageDown でスクロールでき、幅が狭い端末では候補の下に表示します。

画面を切り替える際は表示をクリアし、終了時に元の画面と端末設定を復元します。候補がない場合も Ctrl+N から手入力できます。標準入力が端末でない場合は、実行するコマンドを明示してください。

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

接続終了と同時にログ保存も完了します。終了時には、保存先・終了コード・所要時間と、ログを開くコマンドを標準エラー出力に表示します。

```text
[kuroko] saved → /path/to/session.log (exit 0, 2m3s)
[kuroko] view: kuroko view '/path/to/session.log'
```

圧縮を有効にしている場合は `.log.gz` の保存先を案内します。ログ保存に失敗した場合や、保存期間・容量の設定によりログが削除された場合は、その状態を表示します。

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

```text
~/.kuroko/
├── config.json
└── logs/
    ├── ssh/
    │   ├── edgeSW03/
    │   └── core-prod-01/
    ├── screen/
    │   └── ttyUSB0/
    └── bash/
```

SSH は入力したホスト名・エイリアス（ユーザー名を除く）、screen はデバイス名、その他はコマンド名ごとに保存します。
`kuroko ssh admin@edgeSW03` は `~/.kuroko/logs/ssh/edgeSW03/` に保存します。
保存先に使えない文字は衝突しない形式に変換します。
`--log-dir`・環境変数・設定で指定するディレクトリは、この階層のルートになります。
ディレクトリは `0700`、ログは `0600` で新規作成します。

### 保存先と使用容量の確認

```bash
kuroko logs --info
kuroko -d ~/work/customer-a/logs logs --info
```

保存先の絶対パス、ログ件数、ディスク使用量、接続先別の件数・使用量を表示します。
対話端末は不要で、保存先が存在しなくても作成せずに0件と表示します。
gzip は圧縮された実サイズで集計し、デバッグ用の `.raw` や書き出した `.txt` は含めません。

### 旧保存先・設定からの移行

```bash
kuroko migrate
```

旧 `~/.config/kuroko/config.json` と、そこに指定された保存先（未指定なら `~/.config/kuroko/logs/`）を新しい配置に移行します。
旧標準保存先にもログが残っている場合は、そちらも移行します。
ログ内の接続先情報を使ってフォルダ分けし、分類できないログは `unclassified/` に保存します。
設定の `log_dir` は `~/.kuroko/logs` に更新し、通知・マスキング・保管設定や未知の設定項目は保持します。

コピー先を同期してSHA-256で全件検証し、削除直前にも元ファイルと移行先を照合します。
検証できた元ファイルだけを削除し、空になった旧ディレクトリを削除します。
同名で内容が異なるログは別名で保存し、既存ファイルは上書きしません。
設定の衝突、壊れた圧縮ログ、使用中のファイルを検出した場合は元データを残して停止します。
中断後の再実行では、内容が一致するコピー済みファイルを再利用します。

移行は明示的なコマンドで行います。通常の閲覧・情報表示・補完で移行や削除は実行しません。
旧バージョンを含む記録中のセッションを終了してから実行してください。
ログ以外のファイルが旧フォルダに残っている場合、そのファイルとフォルダは保持します。

### ファイル名の形式

接続日時と接続先が自動でファイル名に含まれます。  
SSH の場合は入力したホスト名（エイリアス）がそのまま使われます。IP 直打ちの場合は IP アドレスになります。

```
20260617_180000_ssh_edgeSW03.log      # ssh admin@edgeSW03 → エイリアス保持
20260617_180000_ssh_10.70.72.248.log  # ssh admin@10.70.72.248 → IP
20260617_190000_screen_ttyUSB0.log
20260617_200000_bash.log
```

### ログ一覧の確認 (TUI セレクター)

保存先の全サブフォルダにあるログを横断し、対話型の TUI で確認・選択できます。

```bash
kuroko logs
```

一覧には接続先・セッション開始日時・所要時間・終了結果（`OK` / `exit N`）を表示します。
ソートにはログに記録された開始日時を使うため、ファイルをコピーしても作業順を保てます。
終了情報のないログは `open`、情報のない旧ログはファイル名と更新日時を表示します。
検索ではファイル名に加えて、接続先・ラップしたコマンド・終了結果を絞り込めます。

- `j` / `k` または `↓` / `↑` / **マウスホイール** : ログファイルの選択移動
- `s` : ソート順の切り替え（`new→old` 新しい順 / `old→new` 古い順）
- `/` : ログファイル名・接続先・コマンド・終了結果の絞り込みフィルタリング（キーワード入力後 Enter で確定、Esc で解除）
- `Enter` または **左クリック** : 選択したログファイルをビューアで開く
- `q` / `Esc` : セレクターの終了

### ログの閲覧 (TUI ビューア)

保存されたログファイルを TUI (Terminal User Interface) で見やすく閲覧できます。  
実行したコマンドの一覧（タイムライン）と、そのコマンドの出力結果を左右分割画面で確認できます。

```bash
kuroko view <path_to_log_file>

# 設定されたログ保存先から直近のセッションを開く
kuroko view --last
kuroko -d ~/work/logs view --last
```

タイムラインの先頭にある **`Entire session`（セッション全体）** を初期表示します。
接続時のバナーやエラーも含めて確認でき、コマンドを検出できないログも全文表示します。
各コマンドには書き出しに使える番号を表示します。通常のログと gzip 圧縮ログの両方に対応しています。
`--last` は全サブフォルダから記録されたセッション開始日時を基準に選び、開始情報のないログでは更新日時を使います。
保存先ルートからの相対パス（例：`kuroko view ssh/edgeSW03/session.log`）でも開けます。
ファイル名だけの指定が複数のログに一致する場合は、候補の相対パスを案内します。

#### キーおよびマウス操作

- **共通**
  - `Tab` : 操作対象のペイン（コマンドタイムライン / 出力結果）の切り替え
  - `h` / `l` または **各ペインの左クリック** : コマンドタイムライン（左）/ 出力結果（右）へ直接フォーカスを切り替え
  - `q` / `Esc` : ビューアの終了
  - `e` : 選択したコマンドと出力をテキストファイルへ保存。`Entire session` を選択していれば全文を保存

- **コマンドタイムライン (左ペイン)**
  - `j` / `k` または `↓` / `↑` または **マウスホイール** : 選択するコマンドの移動
  - **行の左クリック** : クリックした位置のコマンドを直接選択し、フォーカスを左ペインに切り替え
  - `/` : 実行したコマンド名の検索モード（入力後 Enter で確定、Esc で解除）

- **出力結果 (右ペイン)**
  - `j` / `k` または `↓` / `↑` または **マウスホイール** : 出力結果の上下スクロール（3行ずつ）
  - `Ctrl+D` / `Ctrl+U` または `PageDown` / `PageUp` : 画面の高さ分の上下高速スクロール
  - `f` : 出力結果テキストのキーワード検索モード（入力後 Enter で確定、Esc で解除）
    - `n` : 次の一致箇所へジャンプ（現在選択されている一致は緑色、それ以外は黄色でハイライト表示されます）
    - `N` : 前の一致箇所へジャンプ

### コマンドと出力の書き出し

ビューアで `e` を押すと、現在の作業ディレクトリに `ログ名_command-001.txt`（セッション全体なら `ログ名_session.txt`）を保存します。
保存先は画面下部に表示します。同名ファイルがあれば連番を付け、既存ファイルを上書きしません。
スクロール位置や検索ハイライトに関係なく、選択したコマンドとその出力全体を保存します。

CLI からは保存先を指定したり、標準出力に取り出したりできます。

```bash
# 直近ログの1番目のコマンドと出力をファイルに保存
kuroko export --last --command 1 -o command.txt

# 指定ログの2番目のコマンドと出力を標準出力へ
kuroko export session.log --command 2

# コマンド番号を省略するとセッション全体を書き出す
kuroko export --last -o session.txt
kuroko export session.log.gz
```

コマンド番号はビューアのタイムラインに表示される番号で、1から始まります。
書き出したテキストにはプロンプトとコマンド・出力を含み、内部のコマンド境界メタデータや検索の色付けは含みません。
ファイルは所有者だけが読み書きできる権限（`0600`）で作成し、既存ファイルの保存先指定はエラーにします。

### シェル補完 (bash)

`kuroko` のサブコマンド・オプション・`kuroko view` のログファイル名を bash の TAB 補完で入力できます。

```bash
# 現在のシェルだけで有効化
source <(kuroko completion bash)

# 恒久的に有効化
kuroko completion bash > ~/.local/share/bash-completion/completions/kuroko
# または (Homebrew の bash-completion@2 を使っている場合)
kuroko completion bash > "$(brew --prefix)/etc/bash_completion.d/kuroko"
```

```bash
kuroko <TAB>          # logs / view / export / migrate / help / completion と PATH 上のコマンドを補完
kuroko view <TAB>     # ~/.kuroko/logs (または -d で指定したディレクトリ) のログファイル名を補完
kuroko -<TAB>         # --log-dir / -d / --help / -h / --version / -v を補完
```

候補が1つなら自動で入力され、複数あれば一覧表示されます（bash 標準の補完動作）。

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

### コマンドの検出とシェル統合（OSC 133）

タイムラインに載せるコマンドは、`user@host:~$ ` のような一般的なプロンプトの形から推定します。
シェルが OSC 133（シェル統合のプロンプト境界マーク）を出力している行では、推定の代わりにそのマークでプロンプトとコマンドを区別します。
`λ ` や `→ ` のように推定できないプロンプトでも、コマンドを正しく記録できます。
fish 4.0 以降は標準で出力します。bash / zsh では、設定ファイルの末尾（プロンプトを設定する処理より後）に次の1行を追加してください。

```bash
# ~/.bashrc
PS1='\[\e]133;A\a\]'"$PS1"'\[\e]133;B\a\]'
```

```zsh
# ~/.zshrc
PROMPT=$'%{\e]133;A\a%}'"$PROMPT"$'%{\e]133;B\a%}'
```

SSH 先のホストで記録したい場合は、接続先の設定ファイルに追加します。
マークのない行（設定していない接続先など）は、従来どおりプロンプトの形から推定します。
OSC 133 に対応していない端末でも、通常はマークが画面に表示されることはありません。

## ネットワーク機器サポート

### 接続先バナー

SSH / screen 接続開始時に接続先を強調表示するバナーを標準エラー出力に表示します。  
本番機などの重要なホストにルールを設定しておくと、誤接続の防止に役立ちます。

```
╔═══════════════════════════════════╗
║  ホスト    : edgeSW03             ║
║  IPアドレス: 10.70.72.248         ║
╚═══════════════════════════════════╝
```

ホスト名にルールがマッチした場合はラベルとカラーで強調表示されます。

```
╔══════════════════════════════════════╗
║  ホスト    : core-prod-01           ║
║  [PRODUCTION]                        ║   ← 赤色で強調
╚══════════════════════════════════════╝
```

設定例（`~/.kuroko/config.json`）:

```json
{
  "banner": {
    "enabled": true,
    "rules": [
      { "match": "prod", "label": "PRODUCTION", "color": "red" },
      { "match": "stg",  "label": "STAGING",    "color": "yellow" }
    ]
  }
}
```

`match` は接続先ホスト名への部分一致（大文字小文字を区別しない）です。  
`color` に指定できる値: `red` / `yellow` / `green` / `cyan` / `blue` / `magenta`

### NW 機器のコマンドタイムライン

Cisco / Arista / Juniper などのネットワーク機器プロンプトを自動認識し、  
`kuroko view` のタイムラインにコマンドを正しく記録します。

```
Router#show version          → タイムラインに記録
Router(config)#interface Gi0/1  → 記録
user@junos> show route       → 記録
```

## 設定ファイル

`~/.kuroko/config.json` を作成すると、起動のたびに設定が読み込まれます。

```json
{
  "log_dir": "/path/to/your/logs",
  "notifier": {
    "type": "none",
    "webhook_url": ""
  },
  "redaction": {
    "enabled": true
  },
  "banner": {
    "enabled": true,
    "rules": [
      { "match": "prod", "label": "PRODUCTION", "color": "red" }
    ]
  }
}
```

### 認証情報のマスキング

`redaction.enabled` を `true` にすると、ログに書き込む前に次の情報を伏せ字に置き換えます（既定は無効）。

- AWS アクセスキー ID（`AKIA...`）とシークレットアクセスキー
- `Bearer` トークン
- SQL の `IDENTIFIED BY '...'` のパスワード
- PEM 形式の秘密鍵ブロック

マスキングが有効な間は、デバッグ用の `KUROKO_RAW_DEBUG=1` を指定しても、加工前の出力を保存する `.raw` ファイルは作成しません。

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

- Discord: ログ全体をテキストファイルとして添付します（8 MiB を超える場合は末尾の 8 MiB）。
- Slack: ログの末尾 2,500 バイトをメッセージに含めます。

圧縮したログ（`.log.gz`）も展開したテキストで送ります。
外部に送る内容（ログとコマンド）は、`redaction.enabled` の設定にかかわらず常に[マスキング](#認証情報のマスキング)してから送信します。

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
│   ├── console/console.go      # 標準エラーへのステータスメッセージ出力
│   ├── launcher/               # 引数なし起動時の対話型接続ランチャー（SSH / screen）
│   ├── logger/
│   │   ├── logger.go           # ログファイルの書き込み・ヘッダーとフッター・全画面アプリの除外
│   │   ├── terminal.go         # 端末出力を行に変換する最小限の端末エミュレーション
│   │   ├── line.go             # 編集中の行（表示幅単位のセルとカーソル）
│   │   ├── rows.go             # カーソル移動で書き換わりうる直近の行の保持
│   │   ├── prompt.go           # シェルプロンプトの検出とコマンド行の判定
│   │   ├── nw_prompt.go        # ネットワーク機器プロンプトの検出
│   │   ├── redact.go           # 認証情報のマスキング
│   │   ├── escape.go           # エスケープシーケンスと改行コードの処理
│   │   ├── storage.go          # ファイル名の確保・圧縮・ローテーション
│   │   └── target.go           # 接続先名とログファイル名の生成
│   ├── session/session.go      # PTY セッション制御
│   ├── notifier/notifier.go    # 外部通知（Discord / Slack）
│   ├── logstore/               # ログ列挙・圧縮ログの読み込み・セッション情報・直近ログの解決
│   ├── migration/              # 検証付きの旧ログ・設定の移行と元ファイル削除
│   ├── completion/completion.go # シェル補完の候補計算・スクリプト生成
│   ├── textwidth/              # 端末表示幅の計算・切り詰め・パディング
│   ├── tui/                    # フルスクリーン TUI 共通の端末・入力・描画ヘルパー
│   └── viewer/                 # TUI ログセレクター・ビューア・エクスポート
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
