# kuroko 利用ガイド

インストールと最初の記録は [README](../README.md) を参照してください。

## セッションを記録する

```bash
kuroko ssh -p 2222 user@hostname
kuroko screen /dev/ttyUSB0 115200
kuroko bash
kuroko -d ~/work/logs ssh user@hostname
```

実行するコマンドは PATH から呼び出せる状態にしておきます。`-d`（`--log-dir`）はコマンド名の前に置きます。標準入力が端末以外の場合は、実行するコマンドを明示してください。

終了時にはログの保存先・終了コード・所要時間と、ビューアで開くコマンドを標準エラー出力に表示します。開始時のバナーには SSH / screen の接続先や実行するコマンド名が表示されます。

### 対話型の接続ランチャー

引数なしで起動すると接続先を選べます。`kuroko -d ~/work/logs` のように保存先だけを指定することもできます。

- **SSH** は `~/.ssh/config` と `Include` 先にある具体的な `Host` エイリアスを一覧に表示し、選択中の設定とファイル位置をプレビューします。接続時の設定評価は SSH が行います。
- **screen** は macOS の `/dev/tty.usb*`、Linux の `/dev/ttyUSB*`・`/dev/ttyACM*` を一覧に表示します。該当する機器がなければ、起動時は SSH のホスト選択に進みます。通信速度の既定値は `115200` です。

文字を入力して候補をあいまい検索し、矢印キーで移動、Enter で確定します。`Ctrl+U` で検索をクリア、`Ctrl+N` で手入力、`Ctrl+B` で戻る、`Ctrl+C` で中止します。プレビューは PageUp / PageDown でスクロールできます。

## ログの閲覧と活用

基本コマンドとビューアのキー操作は README に掲載しています。[ログの閲覧と活用](../README.md#ログの閲覧と活用)

### 横断検索と行番号

`kuroko grep` は Go の正規表現（RE2）を使い、`<相対パス>:<行番号>:<行>` を出力します。`-i` は大文字・小文字を区別せず、`-l` は一致したファイル名だけを出力します。終了コードは一致ありで `0`、一致なしで `1`、読み込み失敗などのエラーで `2` です。

`--open` は対話端末で使います。一致が 1 件なら直接開き、複数なら候補（先頭 10,000 件まで）から選びます。`-l` と組み合わせるとエラーになります。

`kuroko view --last --line 120` のように行番号を指定すると、該当するコマンドやセッション全体を開いて行を強調します。行番号はヘッダー・フッターや内部のメタデータ行を含め、元のログ（gzip は展開後）の先頭から数えます。`--follow` を使う場合は行番号を省略します。

### 記録中のログを追従する

`view --follow`（`-f`）は既存のログを出力してから追記を表示し、セッションが終了するか `Ctrl+C` を押すと終了します。終了済みのログや gzip 圧縮ログは全文を出力して終了します。記録中は、カーソル移動による再描画を処理するため、直近のおよそ 1 画面分の書き出しを保留します。

### テキストへ書き出す

ビューアで `e` を押すと、作業ディレクトリに `ログ名_command-001.txt` または `ログ名_session.txt` を作成します。同名ファイルがあれば連番を付けます。

CLI では `kuroko export session.log --command 2` のように、タイムラインの番号（1 始まり）でコマンドを指定できます。`--command` を省略するとセッション全体、`-o` を省略すると標準出力に書き出します。出力ファイルは権限 `0600` で新規作成し、`-o` で既存ファイルを指定するとエラーになります。

### コマンドの検出

一般的なシェルや Cisco / Arista / Juniper などのネットワーク機器のプロンプトから、タイムラインに載せるコマンドを検出します。たとえば `Router#show version` や `user@junos> show route` に対応します。

独自のプロンプトを使う場合は、OSC 133 の境界マークでプロンプトとコマンドを区別できます。fish 4.0 以降では標準で出力します。bash / zsh ではプロンプト設定より後に、利用するシェルに対応する行を追加します。SSH 先のシェルで使う場合は接続先で設定します。

```bash
# ~/.bashrc
PS1='\[\e]133;A\a\]'"$PS1"'\[\e]133;B\a\]'
```

```zsh
# ~/.zshrc
PROMPT=$'%{\e]133;A\a%}'"$PROMPT"$'%{\e]133;B\a%}'
```

## 設定

設定を変える場合は `~/.kuroko/config.json` を作成します。`kuroko config check` で現在の設定とキー名などの警告を確認できます。Webhook URL は設定の有無だけを表示します。警告や読み込みエラーがあれば終了コードは `1` です。

### 保存先と保管

ログの保存先は `--log-dir`、`KUROKO_LOG_DIR`、設定ファイルの `log_dir`、既定値の順に優先します。既定の保存先は `~/.kuroko/logs/` です。配下に `ssh/<host>/`、`screen/<device>/`、`bash/` などのフォルダを作り、ファイル名に開始日時を付けます。ディレクトリは `0700`、ログは `0600` で新規作成されます。

次の例では、セッション終了時に 1 MiB 以上のログを gzip 圧縮し、30 日を超えたログや総容量 1,024 MiB を超過した分を古い順に削除します。

```json
{
  "storage": {
    "compress_on_close": true,
    "compress_threshold_mb": 1,
    "rotation": {
      "enabled": true,
      "max_age_days": 30,
      "max_total_size_mb": 1024
    }
  }
}
```

圧縮とローテーションは既定で無効です。`compress_threshold_mb` が `0` の場合はサイズにかかわらず圧縮し、`max_age_days` または `max_total_size_mb` が `0` の場合は、その条件での削除を無効にします。

### マスキングと接続先バナー

```json
{
  "redaction": { "enabled": true },
  "banner": {
    "enabled": true,
    "rules": [
      { "match": "prod", "label": "PRODUCTION", "color": "red" }
    ]
  }
}
```

`redaction.enabled` は既定で無効です。有効にすると、保存前に AWS キー、Bearer トークン、SQL の `IDENTIFIED BY '...'` のパスワード、PEM 形式の秘密鍵など、検出した認証情報を伏せ字にします。有効な間は `KUROKO_RAW_DEBUG=1` を指定しても、加工前の `.raw` は作成されません。

バナーは既定で有効です。`banner.rules` の `match` に接続先名が部分一致すると、ラベルと色を表示します。大文字・小文字は区別せず、色は `red`、`yellow`、`green`、`cyan`、`blue`、`magenta` から選べます。

### Discord / Slack 通知

通知は既定で無効です。`notifier.type` に `discord` または `slack` を設定し、対応する Webhook URL を指定します。

```json
{
  "notifier": {
    "type": "discord",
    "webhook_url": "https://discord.com/api/webhooks/...",
    "include_args": false,
    "include_log": false
  }
}
```

有効にすると、開始時にコマンド名、終了時にコマンド名・終了状態・所要時間・ログファイル名を通知します。`include_args` を有効にすると開始時と終了時にコマンドラインも送信します。`include_log` を有効にすると、終了時に Discord にはテキストを添付（8 MiB を超える場合は末尾 8 MiB）、Slack には末尾 2,500 バイトを送ります。圧縮ログは展開され、外部に送るログとコマンドは常にマスキングされます。

環境変数の `KUROKO_NOTIFIER`、`KUROKO_WEBHOOK_URL`、`KUROKO_NOTIFIER_INCLUDE_ARGS`、`KUROKO_NOTIFIER_INCLUDE_LOG` でそれぞれ上書きできます。通知を無効にするタイプは `none` です。

## 旧ログと設定の移行

旧バージョンを含め、記録中のセッションを終了してから実行します。

```bash
kuroko migrate
```

旧設定の `~/.config/kuroko/config.json` と、設定されている旧ログ保存先を `~/.kuroko/` に移します。旧標準の `~/.config/kuroko/logs/` にログがあればそれも移行します。主な移行元を指定する場合は `kuroko migrate --from /absolute/log/directory` を使います。

ログは接続先ごとに分類し、分類できないログは `unclassified/` に保存します。同名で内容の異なるログは別名で保存します。設定の `log_dir` は新しい保存先に更新し、ほかの設定項目は保持します。

コピー先を SHA-256 で検証し、削除直前に照合できた元ファイルだけを削除します。設定の衝突、壊れた圧縮ログ、使用中のファイルが見つかると停止します。中断後に再実行すると、内容が一致するコピー済みファイルを再利用します。

## シェル補完とソースビルド

補完を有効にすると、サブコマンド、オプション、ログファイル名、SSH の接続先などを TAB で入力できます。

```bash
# bash の現在のセッション。毎回読み込むには ~/.bashrc に追加
eval "$(kuroko completion bash)"
```

```zsh
# zsh。~/.zshrc の compinit より後に追加
eval "$(kuroko completion zsh)"
```

```fish
# fish の現在のセッション
kuroko completion fish | source

# 次回以降も有効化
mkdir -p ~/.config/fish/completions
kuroko completion fish > ~/.config/fish/completions/kuroko.fish
```

ソースからビルドするには Go 1.26 以上、Git、Make が必要です。`make install` は `~/.local/bin/kuroko` に配置します。

```bash
git clone https://github.com/nagayon-935/kuroko.git
cd kuroko
make install
```
