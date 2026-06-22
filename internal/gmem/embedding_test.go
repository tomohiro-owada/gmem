package gmem

import (
	"context"
	"strings"
	"testing"
)

func TestE5EmbedderRequiresModelFile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingModelPath = t.TempDir() + "/missing-model.onnx"
	cfg.EmbeddingTokenizerPath = t.TempDir() + "/missing-tokenizer.json"
	cfg.ONNXRuntimePath = t.TempDir() + "/missing-runtime"
	emb := &E5Embedder{Config: cfg}
	err := emb.Ready(context.Background())
	if err == nil {
		t.Fatal("expected readiness error")
	}
	if !strings.Contains(err.Error(), "model.onnx") && !strings.Contains(err.Error(), "onnxruntime") {
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
