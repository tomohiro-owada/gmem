package gmem

import "time"

type Warning struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type ErrorObject struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Field   string         `json:"field,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type Response[T any] struct {
	OK       bool         `json:"ok"`
	Data     T            `json:"data,omitempty"`
	Error    *ErrorObject `json:"error,omitempty"`
	Warnings []Warning    `json:"warnings"`
}

func OK[T any](data T, warnings ...Warning) Response[T] {
	if warnings == nil {
		warnings = []Warning{}
	}
	return Response[T]{OK: true, Data: data, Warnings: warnings}
}

func Fail[T any](code, message, field string, details map[string]any) Response[T] {
	return Response[T]{
		OK:       false,
		Error:    &ErrorObject{Code: code, Message: message, Field: field, Details: details},
		Warnings: []Warning{},
	}
}

type Limits struct {
	MaxTitleBytes       int `json:"max_title_bytes"`
	MaxContentBytes     int `json:"max_content_bytes"`
	HardMaxContentBytes int `json:"hard_max_content_bytes"`
}

type SecurityPolicy struct {
	RejectPersonalInformation bool `json:"reject_personal_information"`
	RejectOrganizationNames   bool `json:"reject_organization_names"`
	RejectCustomerNames       bool `json:"reject_customer_names"`
}

type Config struct {
	GitDir                  string         `json:"git_dir"`
	RemoteURL               string         `json:"remote_url"`
	IndexPath               string         `json:"index_path"`
	EmbeddingProvider       string         `json:"embedding_provider"`
	EmbeddingModel          string         `json:"embedding_model"`
	EmbeddingModelRepo      string         `json:"embedding_model_repo"`
	EmbeddingModelRevision  string         `json:"embedding_model_revision"`
	EmbeddingModelURL       string         `json:"embedding_model_url"`
	EmbeddingModelPath      string         `json:"embedding_model_path"`
	EmbeddingModelChecksum  string         `json:"embedding_model_checksum"`
	EmbeddingTokenizerPath  string         `json:"embedding_tokenizer_path"`
	ONNXRuntimePath         string         `json:"onnx_runtime_path"`
	EmbeddingQueryPrefix    string         `json:"embedding_query_prefix"`
	EmbeddingDocumentPrefix string         `json:"embedding_document_prefix"`
	Limits                  Limits         `json:"limits"`
	SecurityPolicy          SecurityPolicy `json:"security_policy"`
}

type Memory struct {
	ProjectID string    `json:"project_id"`
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Embedding []float32 `json:"-"`
	Hash      string    `json:"content_hash"`
	CreatedAt time.Time `json:"created_at"`
	IndexedAt time.Time `json:"indexed_at"`
}

type SaveRequest struct {
	CurrentWorkspacePath string `json:"current_workspace_path"`
	Title                string `json:"title"`
	Content              string `json:"content"`
	DryRun               bool   `json:"dry_run,omitempty"`
}

type SaveResult struct {
	ProjectID    string `json:"project_id"`
	Path         string `json:"path"`
	CommitHash   string `json:"commit_hash,omitempty"`
	Pushed       bool   `json:"pushed"`
	Indexed      bool   `json:"indexed"`
	DryRun       bool   `json:"dry_run,omitempty"`
	EmbeddingDim int    `json:"embedding_dim"`
}

type SearchRequest struct {
	Query                string   `json:"query"`
	CurrentWorkspacePath string   `json:"current_workspace_path,omitempty"`
	Limit                int      `json:"limit,omitempty"`
	All                  bool     `json:"all,omitempty"`
	Fields               []string `json:"fields,omitempty"`
	SnippetChars         int      `json:"snippet_chars,omitempty"`
}

type SearchResult struct {
	ProjectID string  `json:"project_id,omitempty"`
	Path      string  `json:"path,omitempty"`
	Title     string  `json:"title,omitempty"`
	Content   string  `json:"content,omitempty"`
	Score     float64 `json:"score"`
}

type SearchData struct {
	Results []SearchResult `json:"results"`
}

type RetryPushRequest struct {
	DryRun bool `json:"dry_run,omitempty"`
}

type RetryPushResult struct {
	Pushed              bool     `json:"pushed"`
	UnpushedCommitCount int      `json:"unpushed_commit_count"`
	PushedCommitHashes  []string `json:"pushed_commit_hashes"`
	ErrorCategory       string   `json:"error_category,omitempty"`
}

type SyncResult struct {
	IndexedDocumentCount int `json:"indexed_document_count"`
	UnpushedCommitCount  int `json:"unpushed_commit_count"`
}

type StatusResult struct {
	GitDir               string `json:"git_dir"`
	RemoteURL            string `json:"remote_url"`
	CurrentBranch        string `json:"current_branch,omitempty"`
	UnpushedCommitCount  int    `json:"unpushed_commit_count"`
	DirtyWorkingTree     bool   `json:"dirty_working_tree"`
	IndexPath            string `json:"index_path"`
	IndexedDocumentCount int    `json:"indexed_document_count"`
	FailedEmbeddingCount int    `json:"failed_embedding_count"`
	EmbeddingProvider    string `json:"embedding_provider"`
	EmbeddingModel       string `json:"embedding_model"`
	EmbeddingModelRepo   string `json:"embedding_model_repo"`
	EmbeddingModelPath   string `json:"embedding_model_path"`
}
