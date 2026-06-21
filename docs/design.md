# Git-SQLite MCP Memory Server Design

## Status

Draft

## Date

2026-06-22

## Context

LLM の長期記憶を Git リポジトリで管理し、MCP 経由で読み書きできるようにする。

通常のベクトル DB だけで記憶を管理すると、人間が中身を確認しにくく、履歴や差分も追いづらい。一方で Git は履歴管理、差分確認、共有、復元に強いが、自然文の曖昧検索には向かない。

そのため、正本は Git 管理された Markdown とし、検索用キャッシュとして SQLite とベクトルを併用する。

## Goals

- Go のワンバイナリでローカル実行できる MCP サーバーにする
- MCP transport は stdio とする
- 記憶の正本は Git リポジトリ内の Markdown にする
- SQLite は検索用のローカルインデックスとして扱う
- ベクトル検索で曖昧な記憶検索を可能にする
- 記憶保存時は必ず Git に追加し、push まで試みる
- 検索時は必ず pull して最新化してから検索する
- プロジェクト単位の検索と、全プロジェクト横断検索の両方を可能にする
- 非エンジニアでも Git のブランチやコンフリクトを意識せず使える運用にする
- 個人情報、認証情報、機密情報を含む記憶は保存前に拒否する
- MCP だけでなく CLI からも同じ保存・検索操作を実行できるようにする
- 初期運用は単一ユーザー利用を前提にし、変更は基本的にすべて commit して push する

## Non-Goals

- ブランチ運用はしない
- 記憶ファイルの上書き編集は基本機能にしない
- 複雑な Git コンフリクト解決 UI は作らない
- SQLite を正本にはしない
- 最初から大規模な HNSW などの近似近傍探索は必須にしない
- Web UI は初期スコープに含めない
- Git ホスティング側のリポジトリ作成や SSH key 作成は行わない

## Decision

### 0. Design Priority

設計判断の優先順位は以下とする。

1. 機能を落とさない
2. ユーザーの作業を止めない
3. AI agent が状況を整理・復旧できる構造化情報を返す
4. 危険な情報保存やデータ破壊だけは明確に止める

異常系では、可能な限り検索や状態確認などの読み取り機能は継続する。失敗を隠すのではなく、warning や error metadata として返し、AI agent が次の整理や復旧操作を提案できるようにする。

### 1. Storage

正本は Git リポジトリ内の Markdown とする。

SQLite は Markdown を高速検索するための派生データとして扱う。SQLite が壊れた場合でも、Git リポジトリ内の Markdown から再インデックスできる状態を保つ。

### 2. Git Operation

main ブランチに直線的にコミットする。

初期運用は単一ユーザー利用を前提にする。記憶リポジトリに発生した変更は、基本的にすべて commit し、可能な限り即座に push する。

複数人や複数端末から使われた場合でも、専用のブランチ運用や複雑な conflict UI は用意しない。他の利用者の commit が先に remote に入っていた場合は、`git pull --rebase` で取り込んでから push する。

記憶保存時は、サーバー側で以下を行う。

1. リモートから `git pull --rebase` で最新化する
2. 保存内容に個人情報、認証情報、機密情報が含まれないか検査する
3. 新しい Markdown ファイルを生成する
4. embedding を生成する
5. Git に add / commit する
6. リモートへ push する
7. SQLite インデックスを更新する

SQLite は再生成可能なキャッシュなので、Git の commit と push を正本保存の主処理として扱う。SQLite 更新に失敗した場合でも、Markdown と Git commit が成功していれば記憶自体は保存済みとみなし、再インデックスで復旧できるようにする。

既存ファイルの同じ行を書き換えないため、通常の Git コンフリクトは発生しにくい。

### 3. Conflict Avoidance

記憶は常に新規 Markdown ファイルとして追加する。

ファイル名にはタイムスタンプや短いランダム値を含め、同時実行時でもファイル名が衝突しないようにする。

例:

- `decision_20260622_101530_a1b2c3.md`
- `bug-note_20260622_101544_d4e5f6.md`

同一内容の統合や要約が必要な場合は、別途「圧縮」「整理」機能として後続フェーズで扱う。

### 4. Project ID

プロジェクト ID は、原則として MCP クライアントが渡す現在の作業ディレクトリのフォルダ名から自動導出する。

例:

- 作業ディレクトリ: `/Users/towada/projects/foo-api`
- `project_id`: `foo-api`

導出した `project_id` はファイルパスとして使う前に必ず sanitize する。許可する文字は英数字、ハイフン、アンダースコア、ドット程度に限定し、それ以外は `-` に置換する。空文字、`.`、`..` は拒否する。

初期設計では同名フォルダの衝突は許容する。必要になった場合は、Git remote URL 由来の slug や workspace path hash の付与、明示的な `project_id` override を検討する。

### 5. Directory Layout

記憶用 Git リポジトリは以下の構造にする。

```text
memory-repo/
├── projects/
│   ├── foo-api/
│   │   ├── decision_20260622_101530_a1b2c3.md
│   │   └── incident_20260622_102010_f7g8h9.md
│   └── bar-frontend/
│       └── design_20260622_103300_i1j2k3.md
└── README.md
```

SQLite ファイルはローカルキャッシュとして扱う想定。

初期案では Git 管理下に `memory.db` を置く案もあったが、毎回 DB 全体の差分が発生しやすく、衝突や履歴汚染の原因になる。そのため初期設計では Git 管理しない。

### 5.1 Local Repository Path

記憶用 Git リポジトリの clone 先は、JSON 設定ファイルでフルパス指定できるようにする。

設定例:

- `git_dir`: 記憶用 Git リポジトリのローカル clone 先
- `remote_url`: clone 元の Git remote URL

設定ファイル形式は JSON とする。

例:

```json
{
  "git_dir": "/Users/towada/Library/Application Support/git-mcp-memory/repo",
  "remote_url": "git@github.com:tomohiro-owada/gmem-memory.git",
  "embedding_provider": "builtin_onnx",
  "embedding_model": "bge-small-ja",
  "embedding_model_repo": "bge-japanese-lightweight-model-repo",
  "embedding_model_revision": "main",
  "embedding_model_url": "https://example.com/models/bge-small.onnx",
  "embedding_model_checksum": "sha256:...",
  "limits": {
    "max_title_bytes": 512,
    "max_content_bytes": 65536,
    "hard_max_content_bytes": 1048576
  },
  "security_policy": {
    "reject_personal_information": true,
    "reject_organization_names": true,
    "reject_customer_names": true
  }
}
```

`git_dir` が設定されている場合は、そのパスを常に優先する。

`git_dir` が未設定の場合は、OS ごとのアプリケーションデータ領域に自動 clone する。

Git remote は作成済みで、SSH 認証も設定済みであることを前提にする。

初回起動時に `git_dir` が存在しない場合は、設定済みの `remote_url` から clone する。`git_dir` が存在する場合は、既存 clone として扱い、起動時またはツール実行時に pull して最新化する。

推奨デフォルト:

- macOS: `~/Library/Application Support/git-mcp-memory/repo`
- Windows: `%LOCALAPPDATA%\git-mcp-memory\repo`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/git-mcp-memory/repo`

SQLite は Git リポジトリとは別に、同じアプリケーションデータ領域配下へ置く。

推奨デフォルト:

- macOS: `~/Library/Application Support/git-mcp-memory/index.sqlite`
- Windows: `%LOCALAPPDATA%\git-mcp-memory\index.sqlite`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/git-mcp-memory/index.sqlite`

### 6. SQLite Index

SQLite には検索に必要なメタデータとベクトルを保存する。

保持する主な項目:

- `project_id`
- `path`
- `title`
- `content`
- `embedding`
- `embedding_status`
- `embedding_error`
- `embedding_provider`
- `embedding_model`
- `embedding_dim`
- `content_hash`
- `created_at`
- `indexed_at`

SQLite は再生成可能なキャッシュなので、起動時または pull 後に Markdown と SQLite の差分を検出し、未インデックスのファイルだけ追加する。

### 7. Embedding

ベクトル化はローカルの日本語対応 embedding model を使う。

初期実装では Ollama のような外部 API サーバーを必須にしない。Go MCP サーバーのプロセス内で embedding 推論を実行する。

モデル本体は Go バイナリには同梱しない。初回起動時に、設定されたモデルをローカルのアプリケーションデータ領域へ自動ダウンロードする。

`save_memory` と `search_memory` は embedding を前提にした機能なので、embedding なしの低品質 fallback は提供しない。初回起動時にモデル準備を完了させ、通常の保存・検索操作で embedding 未準備により失敗しない状態を作る。

モデル準備に失敗した場合は、MCP server / CLI は readiness error を返す。エラーには原因、再試行可否、推奨アクションを含める。

初期デフォルト:

- provider: `builtin_onnx`
- model: bge 系の軽量 embedding model
- model source: Hugging Face Hub
- model cache:
  - macOS: `~/Library/Application Support/git-mcp-memory/models/`
  - Windows: `%LOCALAPPDATA%\git-mcp-memory\models\`
  - Linux: `${XDG_DATA_HOME:-~/.local/share}/git-mcp-memory/models/`

初期モデル:

- bge 系の日本語対応軽量 ONNX model

初期モデルの配布元は Hugging Face Hub とする。Hugging Face は embedding API として使うのではなく、モデルファイルのダウンロード元として使う。ダウンロードしたモデルはローカル cache に保存し、Go プロセス内で推論する。

候補方針:

- 日本語対応の bge 系軽量 model を優先する
- ONNX 形式または実装時に ONNX 変換しやすい形式を優先する
- bge-m3 は日本語を含む多言語対応だが軽量ではないため、初期デフォルトではなく上位候補として扱う

確認済みの参考情報:

- `BAAI/bge-small-en-v1.5` は Hugging Face 上で ONNX タグがあり、license は MIT と表示されている
- `BAAI/bge-m3` は多言語対応で、日本語検索の候補になる

参考:

- https://huggingface.co/BAAI/bge-small-en-v1.5
- https://huggingface.co/BAAI/bge-m3

将来候補:

- Ruri v3 系の日本語 embedding model
- bge-m3 などの多言語 embedding model

初期モデルは、以下の条件を満たすものを優先する。

- 日本語検索品質が十分である
- ONNX など、Go からプロセス内推論しやすい形式で配布できる
- モデルサイズが軽量で、初回ダウンロードとローカル常用に耐える
- 初回ダウンロード元と checksum を固定できる
- ライセンス上、ローカル利用と再配布または自動ダウンロードに問題がない

設定ファイルでは embedding provider を差し替え可能にする。

設定項目:

- `embedding_provider`
- `embedding_model`
- `embedding_model_repo`
- `embedding_model_revision`
- `embedding_model_url`
- `embedding_model_path`
- `embedding_model_checksum`

外部 API サーバー型 provider は任意の拡張として扱う。

任意 provider 例:

- `ollama`
- `openai`
- その他の HTTP embedding endpoint

クラウド embedding API は初期スコープに含めない。

### 8. Vector Search

初期実装では SQLite から候補レコードを取得し、Go 側でコサイン類似度を全探索する。

数千から数万件程度の Markdown 記憶であれば、Go の全探索で十分実用的な可能性が高い。必要になった段階で HNSW や SQLite 拡張による近似近傍探索を検討する。

検索は以下の 2 種類を提供する。

- プロジェクト検索: `project_id` で絞って検索する
- 全体検索: `project_id` を指定せず、全プロジェクト横断で検索する

検索結果はデフォルトで本文全文を返す。AI agent が一回の検索で十分な文脈を得られることを優先する。

返却量を抑えたい場合は、CLI では `--fields` や `--snippet-chars`、MCP では同等の入力フィールドを追加して制御できるようにする。

### 8.1 Save Size Limits

一回の `save_memory` または CLI `save` で保存できる量には上限を設ける。

デフォルト:

- `max_title_bytes`: 512
- `max_content_bytes`: 65536
- `hard_max_content_bytes`: 1048576

`max_content_bytes` を超えた場合は保存を拒否し、AI agent に分割保存や要約を促せる構造化エラーを返す。

`hard_max_content_bytes` は設定ファイルでも超えられない安全上限とする。巨大な入力を Git、SQLite、embedding 推論に流さないための防御境界として扱う。

エラー例:

```json
{
  "ok": false,
  "error": {
    "code": "content_too_large",
    "message": "content exceeds max_content_bytes",
    "field": "content",
    "details": {
      "max_content_bytes": 65536,
      "actual_content_bytes": 120000
    }
  },
  "warnings": []
}
```

### 9. MCP Transport

transport は stdio とする。

stdio にする理由:

- リモートサーバーとして動かす予定がない
- MCP クライアントからローカルコマンドとして起動しやすい
- HTTP サーバーの待受ポート、認証、プロセス管理を考えなくてよい
- Go ワンバイナリとの相性がよく、配布と設定が単純になる
- 初期スコープで複数クライアント同時接続を扱わなくてよい

stdout は MCP の JSON-RPC 通信専用とする。通常ログ、警告、診断情報は stderr に出力し、保存対象の本文や検出した秘密情報をログに出さない。

### 10. MCP Tools

初期スコープのツールは 3 つに絞る。

#### save_memory

新しい記憶を保存する。

入力:

- `current_workspace_path`: 現在の作業ディレクトリの絶対パス
- `title`: 記憶タイトル
- `content`: Markdown 形式の本文

処理:

1. `current_workspace_path` の末尾フォルダ名から `project_id` を導出し、sanitize する
2. `title` と `content` のサイズ上限を検査する
3. Git pull で記憶リポジトリを最新化する
4. `title` と `content` に対して security gate を実行する
5. 拒否対象が見つかった場合は Markdown、SQLite、Git、ログのいずれにも本文を残さずに失敗を返す
6. embedding を生成する
7. embedding に失敗した場合は保存を失敗させ、Markdown、SQLite、Git、ログのいずれにも本文を残さない
8. `projects/{project_id}/` 配下にユニークな Markdown ファイルを生成する
9. Git add / commit を実行する
10. Git push を実行する
11. push 成功後、SQLite に登録する

返却:

- 保存された `project_id`
- 保存された Markdown パス
- commit hash
- push 結果
- インデックス更新結果

#### search_memory

過去の記憶を検索する。

入力:

- `query`: 検索クエリ
- `current_workspace_path`: 任意。指定された場合はプロジェクト検索、未指定の場合は全体検索
- `limit`: 任意。返却件数

処理:

1. ローカル記憶リポジトリに未 push commit があるか確認する
2. 未 push commit がある場合は、pull より先に `retry_push` と同じ処理で push を試みる
3. remote ahead の場合は `git pull --rebase` で他の commit を取り込んでから push を再試行する
4. retry push が成功した場合、または未 push commit がない場合は、`git pull --rebase` で記憶リポジトリを最新化する
5. retry push が失敗した場合は、pull は実行せず、ローカル状態で検索を続行する
6. retry push が失敗した場合は、レスポンスに同期失敗の構造化エラーまたは warning を含め、AI agent が `retry_push` や状況整理を促せるようにする
7. Markdown と SQLite の差分を再インデックスする
8. `query` の embedding を生成する
9. `project_id` 指定があれば絞り込む
10. ベクトル類似度で上位結果を返す

返却:

- `project_id`
- Markdown パス
- タイトル
- 内容全文
- 類似度スコア
- 同期失敗がある場合は warning / error metadata

retry push に失敗したがローカル検索を続行した場合の返却例:

```json
{
  "ok": true,
  "data": {
    "results": []
  },
  "warnings": [
    {
      "code": "sync_failed_local_results",
      "message": "retry push failed; search results are based on local repository state",
      "details": {
        "unpushed_commit_count": 1,
        "recommended_action": "retry_push"
      }
    }
  ]
}
```

#### retry_push

未 push のローカル commit を再送する。

入力:

- `dry_run`: 任意。true の場合は未 push commit 数と再送予定だけを返し、push は実行しない

処理:

1. ローカル記憶リポジトリの状態を確認する
2. 未 push commit があるか確認する
3. remote ahead の場合は `git pull --rebase` を実行する
4. `git push` を実行する
5. 成功または失敗の詳細を返す

返却:

- pushed
- unpushed commit count
- pushed commit hashes
- retry result
- error category

### 11. CLI

MCP サーバーと同じ Go バイナリで CLI も提供する。

CLI は MCP の代替 UI として扱い、保存・検索・再同期などの操作をターミナルから実行できるようにする。MCP tool と CLI command は別々に business logic を持たず、同じ内部ユースケース層を呼び出す。

CLI は AI agent からの利用を第一級に扱う。人間向けの短いフラグ入力は残すが、AI 向けには構造化入力、構造化出力、実行時 schema 取得、dry-run、非対話実行を正式な操作経路として提供する。

想定コマンド:

```text
git-mcp-memory mcp
git-mcp-memory save --input json --output json
git-mcp-memory save --workspace /path/to/project --title "..." --content "..." --output json
git-mcp-memory save --workspace /path/to/project --title "..." --file memory.md --output json
git-mcp-memory search --input json --output json
git-mcp-memory search "query" --workspace /path/to/project --limit 10 --output json
git-mcp-memory search "query" --all --limit 10 --output json
git-mcp-memory sync --output json
git-mcp-memory status --output json
git-mcp-memory retry-push --output json
git-mcp-memory schema --output json
```

CLI のデフォルト出力は JSON とする。人間向けの text 出力は `--output text` で明示指定する。

AI agent から使う場合は、原則として `--input json` と `--output json` を使う。人間向けの省略記法は補助機能とする。

#### `mcp`

stdio transport の MCP サーバーとして起動する。

stdout は MCP JSON-RPC 専用とし、ログは stderr に出力する。

#### `save`

新しい記憶を保存する。

入力:

- `--workspace`: 現在の作業ディレクトリ。未指定時は CLI の current working directory を使う
- `--title`: 記憶タイトル
- `--content`: Markdown 本文
- `--file`: Markdown 本文を読み込むファイル
- `--input json`: stdin から構造化 JSON request を受け取る
- `--output json`: 構造化 JSON response を出力する
- `--dry-run`: security gate、入力検証、保存予定パス生成、embedding model の準備状態確認まで行い、Markdown 作成、Git commit、push、SQLite 更新は行わない
- `--non-interactive`: 対話プロンプトを禁止し、必要な入力が不足している場合は構造化エラーで失敗する

`--content` と `--file` の両方が指定された場合はエラーにする。

`--input json`、`--content`、`--file` のいずれも指定されていない場合はエラーにする。CLI は AI agent が非対話で叩く前提なので、本文が空でも editor は起動しない。

CLI の `save` でも MCP の `save_memory` と同じ security gate を必ず通す。拒否された場合は、本文や検出値を stdout / stderr に出さず、カテゴリと理由コードだけを表示する。

`--input json` の例:

```json
{
  "current_workspace_path": "/path/to/project",
  "title": "Decision title",
  "content": "Markdown body"
}
```

`--output json` の成功レスポンスは MCP の `save_memory` 返却と同じ構造にする。失敗時も固定された error object を返す。

#### `search`

過去の記憶を検索する。

入力:

- positional `query`: 検索クエリ
- `--workspace`: 指定された場合はプロジェクト検索
- `--all`: 全プロジェクト横断検索
- `--limit`: 返却件数
- `--input json`: stdin から構造化 JSON request を受け取る
- `--output json`: JSON 形式で出力
- `--output ndjson`: 結果を 1 行 1 JSON で出力する
- `--fields`: 返却フィールドを絞る
- `--snippet-chars`: 内容抜粋の最大文字数

`--workspace` と `--all` の両方が未指定の場合は、CLI の current working directory を `--workspace` として扱う。

検索結果はデフォルトで本文全文を返す。必要に応じて `--fields` と `--snippet-chars` により、返却量を絞れるようにする。

#### `sync`

Git pull と SQLite 再インデックスを明示的に実行する。

用途:

- embedding model 準備後に未処理ファイルを再処理する
- SQLite を削除・破損した後に再生成する
- CLI や MCP を使う前に手動で状態を最新化する

#### `status`

ローカル記憶リポジトリの状態を表示する。

表示する主な項目:

- `git_dir`
- `remote_url`
- current branch
- unpushed commit count
- dirty working tree の有無
- SQLite index path
- indexed document count
- failed embedding count

CLI の出力は JSON をデフォルトにし、人間向けに `--output text` を用意する。

#### `retry-push`

未 push のローカル commit を再送する。

入力:

- `--dry-run`: 未 push commit 数と再送予定だけを表示し、push は実行しない
- `--output json`: JSON 形式で出力する

MCP tool の `retry_push` と同じ内部ユースケースを呼び出す。remote ahead の場合は `git pull --rebase` 後に push を再試行し、それ以外の失敗ではローカル commit を残す。

#### `schema`

CLI と MCP tool の実行時仕様を JSON で返す。

schema の形式は JSON Schema とする。CLI command、MCP tool、JSON 設定ファイルの schema は、可能な限り同じ定義から生成する。

返却する主な項目:

- command / tool 名
- 入力 schema
- 出力 schema
- 必須フィールド
- 任意フィールド
- デフォルト値
- enum 値
- destructive / write operation かどうか
- dry-run 対応有無
- security gate 対象かどうか
- 認証・外部依存

AI agent は静的ドキュメントではなく、この `schema` の結果を実行時の正本として扱えるようにする。

### 12. CLI Input and Output Contract

CLI の AI 向け経路では、成功時と失敗時の JSON 形式を固定する。

成功時:

```json
{
  "ok": true,
  "data": {},
  "warnings": []
}
```

失敗時:

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "input validation failed",
    "field": "current_workspace_path",
    "details": {}
  },
  "warnings": []
}
```

JSON 出力には ANSI color、進捗表示、人間向け説明文を混ぜない。ログは stderr に出すが、`--output json` では stdout を JSON response 専用にする。

入力検証では以下を拒否する。

- path traversal
- 空の project_id
- `.` または `..` の project_id
- 不可視制御文字
- 低 ASCII 制御文字
- 不正な UTF-8
- `--content` と `--file` の同時指定
- `--workspace` と `--all` の矛盾指定

CLI は曖昧な入力を自動補正しない。不正入力は早く、明確に、構造化エラーで失敗させる。

### 13. AI Agent Guidance

CLI 配布物には AI agent 向けの `AGENTS.md` または同等のガイドファイルを含める。

記載する主な内容:

- AI agent は原則として `--input json` と `--output json` を使う
- 書き込み系操作では、必要に応じて `--dry-run` を先に実行する
- 検索では `--fields` と `--snippet-chars` で出力を絞る
- security gate による拒否内容の本文や検出値を再表示しない
- stdout / stderr の扱い
- よくある失敗コードと復旧方法

## Security Gate

`save_memory` は保存前に内容を検査する。API key、private key、token などの認証情報や secret は常に保存を拒否する。この拒否は設定ファイルでも無効化できない。

メールアドレス、氏名、会社名、顧客名などの個人情報・組織情報・顧客情報は、デフォルトでは拒否する。ただし、運用上必要な場合に備えて、設定ファイルの `security_policy` でカテゴリ単位の緩和を可能にする。

検出対象:

常時拒否:

- API key、access token、secret key、password
- SSH private key、証明書秘密鍵
- OAuth token、Bearer token、Cookie、session ID
- `.env` 形式の認証情報

デフォルト拒否。設定ファイルで policy 変更可能な対象:

- メールアドレス、電話番号、住所
- 氏名などの個人識別情報
- 顧客名、契約情報、社内限定情報
- その他、AI クライアントが機密と判断した内容

判定は二段階にする。

1. MCP クライアント側の AI が保存前に機密性を判断する
2. MCP サーバー側でもルールベース検査を行い、危険な内容は保存拒否する

AI の判断だけには依存しない。最終判断は MCP サーバー側で強制する。

初期実装で拒否する代表例:

- `BEGIN PRIVATE KEY`
- `BEGIN RSA PRIVATE KEY`
- `AKIA` で始まる AWS access key 形式
- `ghp_`、`github_pat_`、`sk-`、`xoxb-`
- `Authorization: Bearer ...`
- `PASSWORD=...`
- `SECRET=...`
- `TOKEN=...`
- `API_KEY=...`
- 長いランダム文字列に見える secret 候補

保存拒否時の返却例:

```json
{
  "saved": false,
  "pushed": false,
  "reason": "content_rejected_by_security_policy",
  "detected_categories": ["api_token", "personal_information"]
}
```

拒否時は本文、検出値、検出周辺文脈をログに出力しない。返却するのはカテゴリや理由コードまでに留める。

## Push Failure Policy

`git push` が失敗した場合でも、成功済みのローカル commit は削除しない。

ただし、失敗原因が remote ahead、non-fast-forward、fetch first など、リモートが先に進んでいることによる拒否だと判定できる場合のみ、自動復旧を試みる。

### Remote Ahead の場合

1. `git pull --rebase` を実行する
2. rebase 成功後、再度 `git push` を実行する
3. 再 push 成功なら保存成功として返す
4. rebase または再 push が失敗した場合は、ローカル commit を残して失敗を返す

remote ahead とみなす代表的な stderr:

- `non-fast-forward`
- `fetch first`
- `remote contains work that you do not have locally`
- `rejected`

### それ以外の場合

ネットワーク不調、認証失敗、権限不足、リモート側 policy、ディスク問題、Git 状態異常などの場合は、自動で reset / revert / commit 削除をしない。

ローカル commit を残し、未 push 状態として返す。

返却例:

```json
{
  "saved": true,
  "pushed": false,
  "local_commit": "abc1234",
  "reason": "push_failed",
  "retry_attempted": false,
  "error_category": "network_or_auth_or_remote_policy"
}
```

remote ahead から復旧できた場合の返却例:

```json
{
  "saved": true,
  "pushed": true,
  "local_commit": "def5678",
  "retry_attempted": true,
  "recovery": "pull_rebase_then_push"
}
```

## Concurrency

複数の `save_memory` が同時に実行される可能性を考慮し、Git 操作と SQLite 更新の周辺にはプロセス間ロックを設ける。

ロック対象:

- clone / pull
- Markdown ファイル生成
- Git add / commit / push
- SQLite 再インデックス

同時実行時でも append-only の新規ファイル追加に限定するため、コンフリクト発生率は低い。ただし push 順序の競合は起きるため、remote ahead の場合は `pull --rebase` で再試行する。

## Write Flow

```text
MCP Client
  |
  | save_memory
  v
Go MCP Server
  |
  | derive and sanitize project_id from workspace path
  | git pull
  | security gate
  | create unique markdown file
  | generate embedding
  | git add / commit
  | git push
  | update sqlite index
  v
Memory Git Repository
```

## Search Flow

```text
MCP Client
  |
  | search_memory
  v
Go MCP Server
  |
  | check unpushed commits
  | retry push if needed
  | git pull --rebase if remote is ahead
  | git pull --rebase after retry push succeeds or no unpushed commits exist
  | continue local search with warning if retry push fails
  | resync sqlite index from markdown
  | generate query embedding
  | vector search with optional project_id filter
  v
Search Results
```

## Markdown Format

各記憶ファイルは、人間が読める Markdown とし、Open Knowledge Format (OKF) v0.1 の考え方に寄せる。

OKF は、知識を Markdown ファイルの集合として表現し、構造化された小さなメタデータを YAML frontmatter に置く形式である。Google Cloud の紹介記事では、OKF v0.1 は Markdown files with YAML frontmatter として説明されており、主要フィールドとして `type`、`title`、`description`、`resource`、`tags`、`timestamp` が挙げられている。

参考: https://cloud.google.com/blog/products/data-analytics/how-the-open-knowledge-format-can-improve-data-sharing?hl=en

初期実装では、Markdown frontmatter を独自形式にせず、OKF 互換のフィールドを使う。

想定する frontmatter:

```yaml
---
type: Memory
title: Example decision
description: Short summary for search and browsing.
resource: null
tags: [decision]
timestamp: 2026-06-22T00:00:00Z
project_id: foo-api
source: mcp
---
```

本文には、LLM が後から読んで意味が取れる粒度で記憶を保存する。

短すぎる断片ではなく、背景、判断、理由、結果を含めることを推奨する。

## Operational Policy

- 記憶保存は append-only とする
- main への直線コミットを基本とする
- pull 失敗、push 失敗、embedding 失敗は MCP ツールの結果として明示する
- embedding は必須とする。embedding 失敗時は保存・検索とも失敗させ、Markdown、SQLite、Git、ログに本文を残さない
- push が remote ahead により失敗した場合のみ、`git pull --rebase` 後に再 push する
- それ以外の push 失敗ではローカル commit を残し、未 push 状態として返す
- 検索時に未 push commit の再送が失敗した場合でも業務を止めないためローカル状態で検索を続行し、同期失敗 warning を返す
- SQLite は壊れても再生成できる設計にする
- security gate で拒否された内容は Markdown、SQLite、Git、ログのいずれにも残さない

## Security

- Git remote の認証情報は OS や Git の既存認証機構に委譲する
- 外部 embedding provider の認証情報が必要になった場合でも、Markdown や Git には保存しない
- 記憶には秘密情報が混入しうるため、保存前に security gate を必ず通す
- 個人情報、認証情報、機密情報を含む可能性がある内容は保存拒否する
- 将来的にプロジェクト単位、ユーザー単位のアクセス制御を検討する

## Risks

### 記憶ファイルが増え続ける

append-only のため、長期運用では Markdown ファイル数が増える。

対策:

- 後続フェーズで要約・圧縮ツールを追加する
- 古い記憶をまとめた summary ファイルを生成する
- 元ファイルは履歴として残す

### 類似記憶が重複する

同じ話題が複数ファイルに保存される。

対策:

- 検索結果では類似度上位を複数返す
- 将来的に重複検出や整理ツールを追加する

### フォルダ名だけでは project_id が衝突する

別ディレクトリに同名リポジトリがある場合、project_id が衝突する。

初期設計では許容する。必要になった場合は以下を検討する。

- Git remote URL から slug を作る
- workspace path の hash を project_id に付与する
- 明示的な project_id override を許可する

### embedding model 依存

embedding model が未ダウンロード、破損、checksum 不一致、またはローカル推論ランタイムで読み込めない場合、保存・検索が失敗する。

対策:

- provider を差し替え可能にする
- 初回起動時にモデルを自動ダウンロードする
- ダウンロード済みモデルは checksum で検証する
- embedding model を準備できない場合は保存・検索を失敗させる

### security gate の誤判定

機密ではない内容が保存拒否される可能性がある。一方で、検出漏れも起こりうる。

対策:

- 初期実装では安全側に倒して拒否する
- 返却時は検出カテゴリのみを示し、本文や検出値は返さない
- 将来的に設定ファイルによるカテゴリ単位の policy 変更を検討する

## Future Work

- 記憶の要約・圧縮ツール
- タグ検索、期間検索、ファイル名検索
- Git log を使った記憶履歴の参照
- プロジェクト ID の明示 override
- 複数ユーザー対応
- ローカル Web UI
- OpenTelemetry などによる操作ログ
- MCP client ごとのデフォルト workspace path 解決
- security gate の検出ルール拡張
- CLI の shell completion
- SDK wrapper
- MCP tool schema と CLI schema の単一ソース化
- 大量検索結果の pagination

## Open Questions

- bge 系軽量 ONNX model の具体的な配布元、checksum、ライセンスをどれに固定するか
