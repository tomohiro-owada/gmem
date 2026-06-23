---
title: "AIに無料で手軽に長期記憶をもたせるツールを作りました"
emoji: "🧠"
type: "tech"
topics: ["ai", "mcp", "git", "sqlite", "go"]
published: false
---

AIに「記憶を持たせるツール」はたくさんあります。

ただ、自分で使うとなると気になることがありました。

- 外部のクラウドに預けるのは少し不安
- CodexやClaude Codeをまたいで記憶したい
- プロジェクトやプロダクトもまたいで記憶したい
- できれば無料で気軽に使いたい

「このプロジェクトではこの方針でいく」「このAPIはこういう理由で使わない」「前にこのバグでハマった」みたいな話を、毎回貼り直すのは面倒です。

ならどこかに保存すればよさそうです。でも、ベクトルDBにそのまま突っ込むのは少し怖い。人間が読めない記憶は、あとから確認できません。「なぜこの判断が保存されたのか」も追いにくくなります。

そこで `git-mcp-memory` を作りました。

https://github.com/tomohiro-owada/gmem

AIの記憶をGit管理されたMarkdownとして残しつつ、検索だけSQLiteとローカルembeddingで速くするMCP server / CLIです。

![AIに無料で手軽に長期記憶をもたせるツールを作りました](/images/ai-agent-memory-git-mcp/problem-map.png)

## なぜMarkdownとGitなのか

AIの記憶というと、まずベクトルDBを考えます。意味検索は強いです。「あの話に近い記憶」を拾えます。

ただ、記憶の正本をベクトルDBに置くと、人間が確認しづらくなります。

「なぜこの判断が保存されたのか」「いつ保存されたのか」「前と何が変わったのか」。ここが見えにくいです。

逆にMarkdownだけにすると、今度は検索が弱くなります。ファイル名やキーワードを覚えていないと見つけにくい。

なので役割を分けました。

- 正本はOKF互換のMarkdown
- 履歴管理はGit
- 検索用cacheはSQLite
- 曖昧検索はローカルembedding

SQLiteが壊れても、Markdownから作り直せます。正本はGitに残ります。ここが一番大事です。

自分で使うなら、こうしたいと思いました。

- 無料で動く。embedding APIを毎回呼ばない
- 外部プロセスを増やさない。Ollamaは起動したくない
- Claude CodeやCodexからMCPで呼べる
- CLIからも同じことができる
- 中身を人間がいつでも読める

全部をちゃんとしたSaaSにすると重くなります。個人で使うなら、GitHubのリポジトリとローカルのSQLiteで十分です。

## 記憶はOKF互換のMarkdownファイルになります

保存された記憶は、普通のMarkdownファイルです。

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

`type`、`title`、`description`、`resource`、`tags`、`timestamp` は Open Knowledge Format v0.1 の基本フィールドに合わせています。`project_id` と `source` は、このツールで検索や保存元を扱うための追加フィールドです。

Gitリポジトリなので、`git log` で追えます。差分も見られます。戻せます。共有もできます。

AIのための記憶でも、人間が読める場所に置いておきたい。ここを外すと、便利だけど信用しづらい道具になります。

## 保存と検索の流れ

![保存と検索の流れ](/images/ai-agent-memory-git-mcp/save-search-flow.png)

保存するときは、だいたいこう動きます。

1. workspace path から project_id を作る
2. secretや個人情報が入っていないか見る
3. embeddingを作る
4. memory repoを `git pull --rebase` する
5. Markdownを新規ファイルとして追加する
6. commitしてpushする
7. SQLite indexを更新する

検索するときは、先にpullして最新化します。それからMarkdownとSQLiteの差分を再indexし、queryのembeddingで近い記憶を返します。

プロジェクト単位でも検索できます。

```bash
git-mcp-memory search "local embeddings" \
  --workspace /path/to/project \
  --limit 5 \
  --output json
```

全部のプロジェクトから探すこともできます。

```bash
git-mcp-memory search "incident summary" --all --limit 10 --output json
```

## MCPでもCLIでも使えます

MCP serverとして起動できます。

```bash
git-mcp-memory mcp
```

MCP toolは3つです。

- `save_memory`
- `search_memory`
- `retry_push`

CLIも同じ内部処理を呼びます。AIエージェントが叩く前提なので、JSONを基本にしています。

```bash
git-mcp-memory save \
  --workspace /path/to/project \
  --title "Decision title" \
  --content "Markdown body" \
  --output json
```

本文が空ならエラーです。エディタは開きません。AIが使うCLIで急に対話モードへ入ると困るので。

## embedding APIはいりません

長期記憶のために、毎回クラウドのembedding APIを呼ぶのは避けたいところです。Ollamaのような外部API serverを常駐させるのも、依存が増えて面倒です。

`git-mcp-memory` は `intfloat/multilingual-e5-small` をONNXでローカル実行します。

- 384次元
- 日本語と英語に対応
- 初回利用時にモデルをダウンロード
- Goバイナリにはモデルを同梱しない

検索文には `query: `、保存文書には `passage: ` を付けます。E5系の作法です。

初回の `save`、`search`、`sync` では、ONNX model、tokenizer、ONNX Runtimeをローカルへ落とします。ここだけ少し時間がかかります。

AI agent側がそれを判断できるように、`status` で準備状態を返します。

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

`assets_ready` が `false` なら、最初の操作はセットアップ込みです。ダウンロード済みなら `true` になります。

## secretは保存させません

長期記憶で一番怖いのは漏洩です。

AIエージェントは作業中にAPI keyやtokenを見ることがあります。それをうっかり保存されたら困ります。なので保存前にsecurity gateを通します。

常に拒否するものは、たとえばこれです。

- private key
- AWS access key
- GitHub token
- OpenAI key
- Slack token
- bearer token
- `TOKEN=...` のようなenv secret

email、電話番号、会社名、顧客名っぽいものもデフォルトでは拒否します。設定で緩められますが、初期値は拒否です。

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

## pushに失敗しても記憶は消しません

Gitを使う以上、ネットワークエラーや認証エラーは起きます。

commitまでは成功してpushだけ失敗した場合、local commitを残します。保存した記憶を消すほうが困るからです。

レスポンスには `pushed: false` とwarningを返します。

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

通信や認証を直してから `retry-push` します。

```bash
git-mcp-memory retry-push --output json
```

未push commitがある状態で検索した場合も、検索は続けます。ただしwarningは返します。

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

仕事は止めない。でも状態は隠さない。この方針にしています。

## 複数端末でも使えます

主な想定は、ローカルでAIエージェントを使う個人の開発環境です。

ただ、複数端末や複数人で同じmemory repoを共有しても壊れにくいようにはしています。記憶は常に新規Markdownファイルとして追加します。既存ファイルの同じ行を書き換えないので、普通のGit conflictは起きにくいです。

他のcommitが先にremoteへ入っていたら、`git pull --rebase` で取り込んでからpushします。

複雑な権限管理やコンフリクト解決UIはありません。そこまでやる道具ではないです。Gitで扱える範囲に寄せています。

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

設定はJSONです。`git_dir` と `remote_url` だけ自分の環境に合わせれば、他はデフォルトのまま動きます。

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

memory repoは自分で作っておきます。GitHub repo作成やSSH key作成まではやりません。

## おわり

無料で、ローカルで、MCPから使えて、あとから人間が読める。

そのために、記憶の正本をMarkdownにしてGitへ残し、検索だけSQLiteとembeddingに任せました。

AIに長期記憶を持たせるやり方は他にもあります。でも自分が毎日使いたいのはこれでした。

https://github.com/tomohiro-owada/gmem
