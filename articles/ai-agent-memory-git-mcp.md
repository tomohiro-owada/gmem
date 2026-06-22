---
title: "AIエージェントの記憶をブラックボックスにしない"
emoji: "🧠"
type: "tech"
topics: ["ai", "mcp", "git", "sqlite", "go"]
published: false
---

AIエージェントに長く作業させていると、地味だけどかなり嫌な問題に当たります。

「前に話した判断、どこに残っているんだっけ」

チャット履歴にはある。たぶん。ベクトルDBにも入っているかもしれない。でも人間が読めない。差分も見えない。いつ、誰が、どういう理由でその記憶を残したのかも追いにくい。

私はここがどうしても気になっていました。AIに記憶を持たせたい。でも、記憶が人間から見えなくなるのは困る。

そこで作ったのが `git-mcp-memory` です。

https://github.com/tomohiro-owada/gmem

この記事では「何を作ったか」よりも、「何に困っていて、何を解決したかったのか」を中心に書きます。

![AIエージェントの記憶をブラックボックスにしない](/images/ai-agent-memory-git-mcp/problem-map.svg)

## 解きたかった問題

AIエージェントの長期記憶には、だいたい2つの寄せ方があります。

ひとつはベクトルDBに寄せる方法。意味検索は強いです。「あの話に近い記憶」を拾える。でも、人間が中身を確認しにくい。運用しているうちに、記憶がだんだんブラックボックスになります。

もうひとつはMarkdownやテキストに寄せる方法。これは読める。Gitで履歴も追える。ただし検索が弱い。ファイル名やキーワードを覚えていないと、欲しい記憶にたどり着けない。

どちらか片方に寄せると、必ず片方が苦しくなる。

だから分けました。

- 正本は Git 管理された Markdown
- 検索用の cache は SQLite
- 曖昧検索はローカル embedding

人間が読むものはMarkdownに残す。AIが探すためのものはSQLiteに持たせる。SQLiteが壊れてもMarkdownから作り直せるので、正本はあくまでGitです。

## 記憶は「検索できるドキュメント」であってほしい

AIの記憶は、単に後から検索できればいいわけではありません。

あとで読める必要があります。なぜその判断をしたのか、どのプロジェクトの話なのか、いつ保存したのか。そこが読めないと、便利なようで怖い。

`git-mcp-memory` では、記憶はこういうMarkdownファイルになります。

```markdown
---
type: Memory
title: "Decision title"
description: "Short description"
resource: null
tags: []
timestamp: 2026-06-22T10:15:30Z
project_id: "my-project-a1b2c3d4"
source: "mcp"
---

Markdown body...
```

Gitリポジトリなので、普通に `git log` で見られます。差分も見られる。戻せる。共有できる。

ここが大事です。AIのための記憶であっても、人間の道具で管理できるようにしておきたい。

## どう動くか

保存と検索の流れはかなり単純です。

![保存と検索の流れ](/images/ai-agent-memory-git-mcp/save-search-flow.svg)

保存するときは、だいたい次の順番で動きます。

1. workspace path から project_id を作る
2. 保存内容にsecretや個人情報がないか見る
3. embeddingを作る
4. memory repoを `git pull --rebase` する
5. Markdownを新規ファイルとして追加する
6. commitしてpushする
7. SQLite indexを更新する

検索するときは逆です。まずpullして最新化し、MarkdownとSQLiteの差分を再indexし、queryのembeddingを作って近い記憶を返します。

検索はプロジェクト単位でも、全プロジェクト横断でもできます。

```bash
git-mcp-memory search "local embeddings" \
  --workspace /path/to/project \
  --limit 5 \
  --output json
```

```bash
git-mcp-memory search "incident summary" --all --limit 10 --output json
```

## MCPでもCLIでも使える

MCP serverとして起動できます。

```bash
git-mcp-memory mcp
```

Claude CodeやCodexのようなMCP clientからは、次のtoolを使います。

- `save_memory`
- `search_memory`
- `retry_push`

CLIも同じ内部処理を呼びます。AIエージェントがCLIを叩く前提なので、出力はJSONを基本にしました。

```bash
git-mcp-memory save \
  --workspace /path/to/project \
  --title "Decision title" \
  --content "Markdown body" \
  --output json
```

空の本文ならエラーにします。エディタは開きません。AIが叩くCLIで急に対話モードに入ると、だいたい事故るので。

## embeddingはローカルで動かす

最初はOllamaのような外部API serverを呼ぶ案も考えました。でも、記憶検索のためだけに別プロセスを立てるのは面倒です。

今は `intfloat/multilingual-e5-small` をONNXでローカル実行しています。

- 384次元
- 日本語と英語に対応
- 初回起動時にモデルをダウンロード
- Goバイナリにはモデルを同梱しない

検索文には `query: `、保存文書には `passage: ` を付けます。E5系の作法です。

## secretを記憶させない

長期記憶で怖いのは、便利さよりもまず漏洩です。

AIエージェントは、作業中にAPI keyやtokenを見てしまうことがあります。それをそのまま「覚えておいて」と保存されると困る。

なので、保存前にsecurity gateを通します。

常に拒否するものは、たとえば次のようなものです。

- private key
- AWS access key
- GitHub token
- OpenAI key
- Slack token
- bearer token
- `TOKEN=...` のようなenv secret

emailや電話番号、会社名・顧客名っぽいものもデフォルトでは拒否します。ここは設定で緩められるようにしていますが、初期値は拒否です。

拒否した値そのものはレスポンスに出しません。返すのはカテゴリとfieldだけです。

```json
{
  "ok": false,
  "error": {
    "code": "content_rejected_by_security_policy",
    "message": "content rejected by security policy",
    "details": {
      "findings": [
        { "category": "openai_key", "field": "content" }
      ]
    }
  }
}
```

## pushに失敗しても、記憶は消さない

Gitを使うなら、ネットワークエラーや認証エラーは避けられません。

ここで「pushできなかったので保存失敗です」として全部捨てるのは嫌でした。書いた記憶が消えるほうが困る。

`git-mcp-memory` は、commitまでは成功してpushだけ失敗した場合、local commitを残します。レスポンスには `pushed: false` とwarningを返します。

```json
{
  "ok": true,
  "data": {
    "pushed": false,
    "indexed": false
  },
  "warnings": [
    {
      "code": "push_failed",
      "message": "memory was committed locally but push failed"
    }
  ]
}
```

その後、通信や認証を直してから `retry-push` します。

```bash
git-mcp-memory retry-push --output json
```

未push commitがある状態で検索した場合も、できるだけ検索は続けます。ただしwarningは返します。

```json
{
  "code": "sync_failed_local_results",
  "message": "retry push failed; search results are based on local repository state",
  "details": {
    "recommended_action": "retry_push",
    "unpushed_commit_count": 1
  }
}
```

仕事を止めない。でも、状態は隠さない。このあたりはかなり意識しました。

## 複数人でも使えるのか

主に想定しているのは、ローカルでAIエージェントを使う個人の開発環境です。

ただ、設計としては複数人や複数端末で同じmemory repoを共有しても壊れにくいようにしています。記憶は常に新規Markdownファイルとして追加します。既存ファイルの同じ行を書き換えないので、普通のGit conflictは起きにくい。

他の人のcommitが先にremoteへ入っていたら、`git pull --rebase` で取り込んでからpushします。

もちろん、複雑な権限管理やコンフリクト解決UIはありません。そこまでやるなら別のプロダクトです。これは、Gitで扱える範囲に寄せた道具です。

## 使い始める

Releaseからバイナリを取れます。

https://github.com/tomohiro-owada/gmem/releases/latest

macOS arm64ならこうです。

```bash
curl -L -O https://github.com/tomohiro-owada/gmem/releases/latest/download/git-mcp-memory-darwin-arm64.tar.gz
tar -xzf git-mcp-memory-darwin-arm64.tar.gz
mkdir -p ~/.local/bin
mv git-mcp-memory-darwin-arm64 ~/.local/bin/git-mcp-memory
chmod +x ~/.local/bin/git-mcp-memory
```

入ったかどうかはschemaで確認できます。

```bash
git-mcp-memory schema --output json
```

configはJSONです。

```json
{
  "git_dir": "/Users/alice/Library/Application Support/git-mcp-memory/repo",
  "remote_url": "git@github.com:alice/my-memory-repo.git",
  "index_path": "/Users/alice/Library/Application Support/git-mcp-memory/index.sqlite",
  "embedding_provider": "builtin_onnx",
  "embedding_model": "multilingual-e5-small",
  "embedding_model_repo": "intfloat/multilingual-e5-small",
  "embedding_model_revision": "main",
  "embedding_query_prefix": "query: ",
  "embedding_document_prefix": "passage: ",
  "security_policy": {
    "reject_personal_information": true,
    "reject_organization_names": true,
    "reject_customer_names": true
  }
}
```

memory repoは自分で作っておく必要があります。GitHub repo作成やSSH key作成まではやりません。

初回の `save`、`search`、`sync` では、ONNX model、tokenizer、ONNX Runtime をローカルにダウンロードします。そのため最初だけ時間がかかります。

AI agentがCLI経由で使う場合は、先に `status` を見れば初回setupが必要か判断できます。

```bash
git-mcp-memory status --output json
```

```json
{
  "assets_ready": false,
  "embedding_model_ready": false,
  "tokenizer_ready": false,
  "onnx_runtime_ready": false
}
```

この状態なら、最初の操作が少し長くなる前提で待てます。ダウンロード済みになれば `assets_ready` は `true` になります。

## まだ荒いところ

正直、まだ作りたいものは残っています。

たとえば、溜まった記憶の圧縮や要約。タグ検索。期間検索。Web UI。あと、SQLiteの全探索で足りなくなったら近似近傍探索も入れたい。

でも、最初に解きたかった問題には届きました。

AIエージェントの記憶を、AIだけのものにしない。

人間が読めて、Gitで追えて、AIが探せる形にする。

そのための小さな道具です。
