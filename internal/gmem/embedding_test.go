package gmem

import (
	"context"
	"strings"
	"testing"
)

func TestE5EmbedderRequiresModelPath(t *testing.T) {
	emb := &E5Embedder{Config: DefaultConfig()}
	err := emb.Ready(context.Background())
	if err == nil {
		t.Fatal("expected readiness error")
	}
	if !strings.Contains(err.Error(), "embedding_model_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHashEmbedderProduces384DimensionsForTests(t *testing.T) {
	vec, err := HashEmbedder{}.Embed(context.Background(), "query: hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != EmbeddingDim {
		t.Fatalf("got dim %d want %d", len(vec), EmbeddingDim)
	}
}
