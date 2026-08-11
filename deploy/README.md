# gmem を HTTPS サーバーとして VPS に置く

Streamable HTTP + OAuth 2.1 で公開し、Claude Code / claude.ai / Cursor / VS Code などの
MCP クライアントから共通で使えるようにする手順。

## 前提

- Ubuntu の VPS に root（sudo）で入れること
- 公開するドメインの A レコードが VPS の IP を指していること
- メモリリポジトリ（`gmem-memory`）の Deploy keys を編集できること

## 手順

1. ソースを VPS に送る

   ```bash
   rsync -az --exclude .git --exclude 'deploy/*.log' ./ VPS:/opt/gmem-src/
   ```

2. VPS 上で構築する

   ```bash
   sudo GMEM_DOMAIN=gmem.example.com /opt/gmem-src/deploy/setup.sh
   ```

   実行内容: Go の導入、cgo ありでのビルド、`gmem` サービスユーザー作成、
   デプロイキー生成、`config.json` 作成、認証情報の生成、systemd 登録、
   nginx への vhost 追加と Let's Encrypt 証明書の取得。冪等なので再実行で再ビルド・再起動になる。

3. 表示されたデプロイキーの公開鍵を、`gmem-memory` リポジトリの
   **Deploy keys に「Allow write access」を有効にして**登録する。

4. 登録後、メモリリポジトリを取り込む

   ```bash
   sudo -u gmem /usr/local/bin/git-mcp-memory sync
   ```

5. 動作確認

   ```bash
   curl https://gmem.example.com/healthz
   curl -X POST https://gmem.example.com/mcp \
     -H "Authorization: Bearer $TOKEN" \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
   ```

## クライアントからの接続

**Claude Code**（固定トークン）

```bash
claude mcp add --transport http gmem https://gmem.example.com/mcp \
  --header "Authorization: Bearer <GMEM_HTTP_TOKEN>"
```

**claude.ai / その他の MCP クライアント**（OAuth）

接続先に `https://gmem.example.com/mcp` を入れるだけでよい。クライアントが
自分自身を動的登録（DCR）し、ブラウザで同意画面が開くので
`GMEM_OAUTH_PASSWORD` を入力する。コールバック URL の事前登録は不要。

## 運用

| 操作 | コマンド |
|---|---|
| 状態 | `systemctl status gmem-http` |
| ログ | `journalctl -u gmem-http -f` |
| 再起動 | `systemctl restart gmem-http` |
| 認証情報の確認 | `sudo cat /etc/gmem/env` |
| 認証の取り消し | `/etc/gmem/env` のトークン/パスワードを変えて再起動。<br>個別の OAuth トークンだけ切る場合は `/var/lib/gmem/.config/git-mcp-memory/oauth.json` から該当エントリを消す |

## 構成

```
クライアント ──HTTPS──> nginx(443) ──> gmem http(127.0.0.1:8765)
                                          │
                                          ├─ save/search は子プロセスを起動
                                          │  （ONNX モデル約470MB を都度読み込み、
                                          │    終了時に OS へ返却）
                                          └─ メモリを git commit して GitHub へ push
```

`MemoryMax=1500M` は、常駐する親プロセスと子プロセス1つ分を見込んだ値。
