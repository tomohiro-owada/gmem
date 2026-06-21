package gmem

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestIndexSearch(t *testing.T) {
	ctx := context.Background()
	idx, err := OpenIndex(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	emb := HashEmbedder{}
	vec, err := emb.Embed(ctx, "query: alpha")
	if err != nil {
		t.Fatal(err)
	}
	mem := Memory{
		ProjectID: "proj",
		Path:      "projects/proj/a.md",
		Title:     "Alpha",
		Content:   "alpha content",
		Embedding: vec,
		Hash:      ContentHash("Alpha", "alpha content"),
		CreatedAt: time.Now(),
	}
	if err := idx.Upsert(ctx, mem, "test", "hash"); err != nil {
		t.Fatal(err)
	}
	results, err := idx.Search(ctx, vec, "proj", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Alpha" {
		t.Fatalf("unexpected results: %+v", results)
	}
}
