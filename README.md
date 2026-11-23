# kuroko

[![CI](https://github.com/nagayon-935/kuroko/actions/workflows/ci.yml/badge.svg)](https://github.com/nagayon-935/kuroko/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/kuroko/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/kuroko?branch=main)
[![Release](https://img.shields.io/github/v/release/nagayon-935/kuroko)](https://github.com/nagayon-935/kuroko/releases)

**kuroko** は、SSH 接続やシリアルコンソール、シェルでの作業をテキストログに残す CLI ツールです。
普段のコマンドの先頭に `kuroko` を付けると、端末を操作しながら出力を記録できます。

## 主な機能

| 機能 | 使い方 |
|------|--------|
| セッションの記録 | `kuroko ssh user@host` や `kuroko screen /dev/ttyUSB0 115200` で接続し、ホストやデバイスごとにログを自動保存 |
| 接続先の選択 | 引数なしの `kuroko` で SSH 設定のホストや USB シリアル機器を絞り込み、接続先を選択 |
| ログの閲覧と活用 | コマンド単位の閲覧、横断検索、追従、テキストへの書き出し。ネットワーク機器のプロンプトにも対応 |
| 保存と通知の設定 | 認証情報のマスキング、gzip 圧縮、保存期間・容量による整理、Discord / Slack 通知を必要に応じて設定 |

## インストール

Go を使える環境では `go install` が手軽です。Go を入れていない macOS / Linux では、GitHub Releases のビルド済みバイナリを使えます。

### Go でインストール

ビルドには Go 1.26 以上が必要です。インストールと更新には同じコマンドを使います。

```bash
go install github.com/nagayon-935/kuroko/cmd/kuroko@latest
```

### ビルド済みバイナリでインストール

`uname -s` と `uname -m` で環境を確認し、対応するファイルを選びます。

| OS | `x86_64` | `arm64` / `aarch64` |
|----|----------|---------------------|
| macOS（Darwin） | `*_darwin_amd64.tar.gz` | `*_darwin_arm64.tar.gz` |
| Linux | `*_linux_amd64.tar.gz` | `*_linux_arm64.tar.gz` |

GitHub CLI（`gh`）が使える場合は、作業用ディレクトリで次のコマンドを実行すると、最新版のアーカイブと `checksums.txt` を取得できます。例は macOS arm64 です。環境に応じて、表のパターンを指定してください。

```bash
gh release download --repo nagayon-935/kuroko \
  --pattern '*_darwin_arm64.tar.gz' --pattern 'checksums.txt'
```

ブラウザで取得する場合も、対応する `.tar.gz` 1 つと `checksums.txt` を同じ作業用ディレクトリに置きます。[GitHub Releases の最新版](https://github.com/nagayon-935/kuroko/releases/latest)

macOS では `shasum -a 256 kuroko_*.tar.gz`、Linux では `sha256sum kuroko_*.tar.gz` を実行し、表示された値を `checksums.txt` の同じファイル名の行と照合します。

値が一致したら、そのディレクトリで次のコマンドを実行します。

```bash
tar -xzf kuroko_*.tar.gz kuroko &&
  mkdir -p "$HOME/.local/bin" &&
  install -m 755 kuroko "$HOME/.local/bin/kuroko"
```

### ソースからビルドしてインストール

Git、Go 1.26 以上、Make が必要です。`make install` はビルドしたバイナリを `~/.local/bin/kuroko` に配置します。

```bash
git clone https://github.com/nagayon-935/kuroko.git
cd kuroko
make install
```

### PATH の設定と動作確認

配布バイナリと `make install` の配置先は `~/.local/bin` です。`go install` で導入した場合は、以下の設定例の `$HOME/.local/bin` を `go env GOBIN` で表示されるディレクトリに置き換えます。空の場合は `go env GOPATH` で表示されるディレクトリの `bin` を使います。

zsh / bash では、次の1行を `~/.zshrc` または `~/.bashrc` に追加し、端末を開き直します。

```bash
export PATH="$HOME/.local/bin:$PATH"
```

fish では `fish_add_path "$HOME/.local/bin"` を実行します。最後に `kuroko --version` で確認できます。

## クイックスタート

まずローカルのコマンドで記録と閲覧を試せます。ビューアは `q` で閉じます。

```bash
kuroko echo "hello kuroko"
kuroko view --last
```

SSH やシリアル接続を記録するときも、普段のコマンドの先頭に `kuroko` を付けます。

```bash
kuroko ssh user@hostname
kuroko screen /dev/ttyUSB0 115200
```

端末で `kuroko` だけを実行すると、接続先を選ぶ画面が開きます。

ログの既定の保存先は `~/.kuroko/logs/` です。セッションごとに場所を変える場合は `kuroko -d ~/work/logs ssh user@hostname` を使います。

### エイリアスで普段の接続を記録する

zsh なら `~/.zshrc`、bash なら `~/.bashrc` に使いたいエイリアスを追加し、端末を開き直します。

```bash
alias ssh='kuroko ssh'
alias screen='kuroko screen'
```

以降は普段どおり `ssh user@hostname` や `screen /dev/ttyUSB0 115200` と入力するだけで、kuroko がセッションを記録します。

## ログの閲覧と活用

| コマンド | 動作 |
|----------|------|
| `kuroko logs` | 接続先・開始日時・所要時間・終了結果の一覧を開く |
| `kuroko logs --info` | 保存先・件数・ディスク使用量を接続先別に集計する |
| `kuroko view --last` | 直近のセッションをビューアで開く |
| `kuroko view <logfile>` | 指定したログをビューアで開く |
| `kuroko view --follow --last` | 直近ログの内容を標準出力に流し、追記を待つ |
| `kuroko grep -i 'error\|down'` | 保存先の全ログを正規表現で横断検索する |
| `kuroko grep --open 'error'` | 検索に一致した行をビューアで開く |
| `kuroko export --last --command 1 -o command.txt` | 直近ログの最初のコマンドと出力を書き出す |

`view` と `export` には、ファイルのパスまたは保存先からの相対パスを指定できます。ファイル名だけで複数のログに一致する場合は、候補の相対パスを案内します。`--last` は記録された開始日時、開始情報のないログは更新日時を使います。閲覧・検索・書き出しは `.log` と `.log.gz` に対応します。

### 一覧とビューアの操作

ログ一覧では矢印キー、`j` / `k`、マウスホイールで移動します。`/` でファイル名・接続先・実行コマンド・終了結果を絞り込み、`s` で並び順を切り替えます。Enter またはクリックで開きます。

ビューアの左側には検出したコマンドのタイムライン、右側には出力を表示します。初期表示の `Entire session` では接続時のバナーやエラーも含めて全文を確認できます。

| キー | 操作 |
|------|------|
| `Tab`、`h` / `l` | 左右のペインを切り替える |
| 矢印キー、`j` / `k`、マウスホイール | 左ペインではコマンドを選択し、右ペインでは出力をスクロールする |
| `/` | タイムラインのコマンドを絞り込む |
| `f`、`n` / `N` | 出力内を検索し、次または前の一致へ移動する |
| PageUp / PageDown、`Ctrl+U` / `Ctrl+D` | 出力をページ単位でスクロールする |
| `e` | 選択中の内容をテキストに書き出す |
| `q` / Esc | ビューアや一覧を閉じる |

## 利用ガイド

- [検索・追従・書き出しの詳細](docs/usage.md#ログの閲覧と活用)
- [保存先・圧縮・マスキング・通知の設定](docs/usage.md#設定)
- [旧ログと設定の移行](docs/usage.md#旧ログと設定の移行)
- [シェル補完とソースからのビルド](docs/usage.md#シェル補完とソースビルド)
