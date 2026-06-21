package gmem

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"sync"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	ort "github.com/yalue/onnxruntime_go"
)

const EmbeddingDim = 384

type Embedder interface {
	Ready(ctx context.Context) error
	Embed(ctx context.Context, input string) ([]float32, error)
}

type E5Embedder struct {
	Config    Config
	mu        sync.Mutex
	loaded    bool
	tokenizer *tokenizer.Tokenizer
	session   *ort.DynamicAdvancedSession
}

func (e *E5Embedder) Ready(ctx context.Context) error {
	return e.ensureLoaded()
}

func (e *E5Embedder) Embed(ctx context.Context, input string) ([]float32, error) {
	if err := e.ensureLoaded(); err != nil {
		return nil, err
	}
	if input == "" {
		return nil, errors.New("embedding input is empty")
	}
	enc, err := e.tokenizer.EncodeSingle(input)
	if err != nil {
		return nil, err
	}
	ids, mask, types := encodeForONNX(enc, 512)
	shape := ort.NewShape(1, int64(len(ids)))
	inputIDs, err := ort.NewTensor(shape, ids)
	if err != nil {
		return nil, err
	}
	defer inputIDs.Destroy()
	attentionMask, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer attentionMask.Destroy()
	tokenTypes, err := ort.NewTensor(shape, types)
	if err != nil {
		return nil, err
	}
	defer tokenTypes.Destroy()
	outputs := []ort.Value{nil}
	err = e.session.Run([]ort.Value{inputIDs, attentionMask, tokenTypes}, outputs)
	if err != nil {
		return nil, err
	}
	defer outputs[0].Destroy()
	out, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, errors.New("unexpected ONNX output tensor type")
	}
	pooled := meanPool(out.GetData(), mask, EmbeddingDim)
	normalize(pooled)
	return pooled, nil
}

var ortInit sync.Once
var ortInitErr error

func (e *E5Embedder) ensureLoaded() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return nil
	}
	if e.Config.EmbeddingProvider != "builtin_onnx" {
		return errors.New("embedding_provider must be builtin_onnx")
	}
	if e.Config.EmbeddingModel != "multilingual-e5-small" {
		return errors.New("embedding_model must be multilingual-e5-small")
	}
	if e.Config.EmbeddingModelPath == "" {
		return errors.New("embedding_model_path is not configured")
	}
	if e.Config.EmbeddingTokenizerPath == "" {
		return errors.New("embedding_tokenizer_path is not configured")
	}
	if e.Config.ONNXRuntimePath == "" {
		return errors.New("onnx_runtime_path is not configured")
	}
	for _, path := range []string{e.Config.EmbeddingModelPath, e.Config.EmbeddingTokenizerPath, e.Config.ONNXRuntimePath} {
		if _, err := os.Stat(path); err != nil {
			return err
		}
	}
	ort.SetSharedLibraryPath(e.Config.ONNXRuntimePath)
	ortInit.Do(func() {
		ortInitErr = ort.InitializeEnvironment()
	})
	if ortInitErr != nil {
		return ortInitErr
	}
	tk, err := pretrained.FromFile(e.Config.EmbeddingTokenizerPath)
	if err != nil {
		return err
	}
	session, err := ort.NewDynamicAdvancedSession(
		e.Config.EmbeddingModelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil,
	)
	if err != nil {
		return err
	}
	e.tokenizer = tk
	e.session = session
	e.loaded = true
	return nil
}

type HashEmbedder struct{}

func (HashEmbedder) Ready(ctx context.Context) error { return nil }

func (HashEmbedder) Embed(ctx context.Context, input string) ([]float32, error) {
	vec := make([]float32, EmbeddingDim)
	words := []byte(input)
	if len(words) == 0 {
		return nil, errors.New("embedding input is empty")
	}
	for i := 0; i < len(words); i += 16 {
		end := i + 16
		if end > len(words) {
			end = len(words)
		}
		sum := sha256.Sum256(words[i:end])
		idx := int(binary.LittleEndian.Uint32(sum[:4]) % EmbeddingDim)
		sign := float32(1)
		if sum[4]&1 == 1 {
			sign = -1
		}
		vec[idx] += sign
	}
	normalize(vec)
	return vec, nil
}

func normalize(v []float32) {
	var total float64
	for _, x := range v {
		total += float64(x * x)
	}
	if total == 0 {
		return
	}
	scale := float32(1 / math.Sqrt(total))
	for i := range v {
		v[i] *= scale
	}
}

func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, an, bn float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		an += av * av
		bn += bv * bv
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / (math.Sqrt(an) * math.Sqrt(bn))
}

func encodeForONNX(enc *tokenizer.Encoding, maxLen int) ([]int64, []int64, []int64) {
	n := len(enc.Ids)
	if n > maxLen {
		n = maxLen
	}
	ids := make([]int64, n)
	mask := make([]int64, n)
	types := make([]int64, n)
	for i := 0; i < n; i++ {
		ids[i] = int64(enc.Ids[i])
		if i < len(enc.AttentionMask) {
			mask[i] = int64(enc.AttentionMask[i])
		} else {
			mask[i] = 1
		}
		if i < len(enc.TypeIds) {
			types[i] = int64(enc.TypeIds[i])
		}
	}
	return ids, mask, types
}

func meanPool(data []float32, mask []int64, dim int) []float32 {
	out := make([]float32, dim)
	var count float32
	for token := 0; token < len(mask); token++ {
		if mask[token] == 0 {
			continue
		}
		count++
		base := token * dim
		if base+dim > len(data) {
			break
		}
		for i := 0; i < dim; i++ {
			out[i] += data[base+i]
		}
	}
	if count == 0 {
		return out
	}
	for i := range out {
		out[i] /= count
	}
	return out
}
