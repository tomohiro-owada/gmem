package gmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

type Index struct {
	db *sql.DB
}

func OpenIndex(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	idx := &Index{db: db}
	if err := idx.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

func (i *Index) Close() error {
	if i == nil || i.db == nil {
		return nil
	}
	return i.db.Close()
}

func (i *Index) init() error {
	_, err := i.db.Exec(`
CREATE TABLE IF NOT EXISTS memories (
  path TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  title TEXT NOT NULL,
  content TEXT NOT NULL,
  embedding TEXT NOT NULL,
  embedding_status TEXT NOT NULL,
  embedding_error TEXT,
  embedding_provider TEXT NOT NULL,
  embedding_model TEXT NOT NULL,
  embedding_dim INTEGER NOT NULL,
  content_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  indexed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS memories_project_id_idx ON memories(project_id);
CREATE INDEX IF NOT EXISTS memories_content_hash_idx ON memories(content_hash);
`)
	return err
}

func (i *Index) Upsert(ctx context.Context, mem Memory, provider, model string) error {
	if len(mem.Embedding) == 0 {
		return errors.New("embedding is required")
	}
	emb, err := json.Marshal(mem.Embedding)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if mem.IndexedAt.IsZero() {
		mem.IndexedAt = now
	}
	_, err = i.db.ExecContext(ctx, `
INSERT INTO memories(path, project_id, title, content, embedding, embedding_status, embedding_error, embedding_provider, embedding_model, embedding_dim, content_hash, created_at, indexed_at)
VALUES(?, ?, ?, ?, ?, 'ready', NULL, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  project_id=excluded.project_id,
  title=excluded.title,
  content=excluded.content,
  embedding=excluded.embedding,
  embedding_status='ready',
  embedding_error=NULL,
  embedding_provider=excluded.embedding_provider,
  embedding_model=excluded.embedding_model,
  embedding_dim=excluded.embedding_dim,
  content_hash=excluded.content_hash,
  created_at=excluded.created_at,
  indexed_at=excluded.indexed_at
`, mem.Path, mem.ProjectID, mem.Title, mem.Content, string(emb), provider, model, len(mem.Embedding), mem.Hash, mem.CreatedAt.UTC().Format(time.RFC3339), mem.IndexedAt.UTC().Format(time.RFC3339))
	return err
}

func (i *Index) FindByPath(ctx context.Context, path string) (string, bool, error) {
	var hash string
	err := i.db.QueryRowContext(ctx, `SELECT content_hash FROM memories WHERE path = ?`, path).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

func (i *Index) Search(ctx context.Context, query []float32, projectID string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := i.db.QueryContext(ctx, `SELECT project_id, path, title, content, embedding FROM memories WHERE (? = '' OR project_id = ?)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []SearchResult
	for rows.Next() {
		var project, path, title, content, embJSON string
		if err := rows.Scan(&project, &path, &title, &content, &embJSON); err != nil {
			return nil, err
		}
		var emb []float32
		if err := json.Unmarshal([]byte(embJSON), &emb); err != nil {
			continue
		}
		results = append(results, SearchResult{
			ProjectID: project,
			Path:      path,
			Title:     title,
			Content:   content,
			Score:     Cosine(query, emb),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(results, func(a, b int) bool {
		return results[a].Score > results[b].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}
