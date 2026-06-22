package gmem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Service struct {
	Config   Config
	Repo     GitRepo
	Index    *Index
	Embedder Embedder
	Security SecurityGate
}

func NewService(cfg Config, idx *Index, emb Embedder) *Service {
	return &Service{
		Config:   cfg,
		Repo:     GitRepo{Dir: cfg.GitDir, RemoteURL: cfg.RemoteURL},
		Index:    idx,
		Embedder: emb,
		Security: SecurityGate{Policy: cfg.SecurityPolicy},
	}
}

func (s *Service) Ready(ctx context.Context) error {
	if err := s.Repo.Ensure(ctx); err != nil {
		return err
	}
	return s.Embedder.Ready(ctx)
}

func (s *Service) Save(ctx context.Context, req SaveRequest) Response[SaveResult] {
	if err := s.validateSave(req); err != nil {
		return Fail[SaveResult]("validation_failed", err.Error(), "", nil)
	}
	projectID, err := DeriveProjectID(req.CurrentWorkspacePath)
	if err != nil {
		return Fail[SaveResult]("validation_failed", err.Error(), "current_workspace_path", nil)
	}
	findings := s.Security.Check(req.Title, req.Content)
	if len(findings) > 0 {
		return Fail[SaveResult]("content_rejected_by_security_policy", "content rejected by security policy", "", map[string]any{"findings": findings})
	}
	documentInput := s.Config.EmbeddingDocumentPrefix + req.Title + "\n\n" + req.Content
	embedding, err := s.Embedder.Embed(ctx, documentInput)
	if err != nil {
		return Fail[SaveResult]("embedding_failed", err.Error(), "", nil)
	}
	now := time.Now().UTC()
	name := UniqueMemoryFilename(req.Title, now, randomHex(3))
	rel := filepath.Join("projects", projectID, name)
	if req.DryRun {
		return OK(SaveResult{ProjectID: projectID, Path: rel, DryRun: true, EmbeddingDim: len(embedding)})
	}
	if err := s.Repo.Ensure(ctx); err != nil {
		return Fail[SaveResult]("git_failed", err.Error(), "", nil)
	}
	if err := s.Repo.PullRebase(ctx); err != nil {
		return Fail[SaveResult]("git_pull_failed", err.Error(), "", gitDetails(err))
	}
	raw := RenderMemoryMarkdown(projectID, req.Title, req.Content, "mcp", now)
	full := filepath.Join(s.Config.GitDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return Fail[SaveResult]("filesystem_failed", err.Error(), "", nil)
	}
	if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
		return Fail[SaveResult]("filesystem_failed", err.Error(), "", nil)
	}
	commit, err := s.Repo.AddCommit(ctx, rel, "Save memory: "+req.Title)
	if err != nil {
		return Fail[SaveResult]("git_commit_failed", err.Error(), "", gitDetails(err))
	}
	var warnings []Warning
	pushed := true
	if err := s.Repo.Push(ctx); err != nil {
		pushed = false
		warnings = append(warnings, Warning{Code: "push_failed", Message: "memory was committed locally but push failed", Details: gitDetails(err)})
	}
	if pushed && s.Index != nil {
		mem := Memory{
			ProjectID: projectID,
			Path:      rel,
			Title:     req.Title,
			Content:   req.Content,
			Embedding: embedding,
			Hash:      ContentHash(req.Title, req.Content),
			CreatedAt: now,
			IndexedAt: time.Now().UTC(),
		}
		if err := s.Index.Upsert(ctx, mem, s.Config.EmbeddingProvider, s.Config.EmbeddingModel); err != nil {
			warnings = append(warnings, Warning{Code: "index_failed", Message: err.Error()})
		}
	}
	return OK(SaveResult{ProjectID: projectID, Path: rel, CommitHash: commit, Pushed: pushed, Indexed: pushed, EmbeddingDim: len(embedding)}, warnings...)
}

func (s *Service) Search(ctx context.Context, req SearchRequest) Response[SearchData] {
	if strings.TrimSpace(req.Query) == "" {
		return Fail[SearchData]("validation_failed", "query is required", "query", nil)
	}
	if err := s.Repo.Ensure(ctx); err != nil {
		return Fail[SearchData]("git_failed", err.Error(), "", nil)
	}
	var warnings []Warning
	if commits, _ := s.Repo.Unpushed(ctx); len(commits) > 0 {
		r := s.RetryPush(ctx, RetryPushRequest{})
		if !r.OK || !r.Data.Pushed {
			warnings = append(warnings, Warning{
				Code:    "sync_failed_local_results",
				Message: "retry push failed; search results are based on local repository state",
				Details: map[string]any{
					"unpushed_commit_count": len(commits),
					"recommended_action":    "retry_push",
				},
			})
		}
	}
	if len(warnings) == 0 {
		if err := s.Repo.PullRebase(ctx); err != nil {
			warnings = append(warnings, Warning{Code: "pull_failed_local_results", Message: "pull failed; search results are based on local repository state", Details: gitDetails(err)})
		}
	}
	if err := s.Resync(ctx); err != nil {
		return Fail[SearchData]("index_failed", err.Error(), "", nil)
	}
	queryEmbedding, err := s.Embedder.Embed(ctx, s.Config.EmbeddingQueryPrefix+req.Query)
	if err != nil {
		return Fail[SearchData]("embedding_failed", err.Error(), "", nil)
	}
	projectID := ""
	if !req.All && req.CurrentWorkspacePath != "" {
		projectID, err = DeriveProjectID(req.CurrentWorkspacePath)
		if err != nil {
			return Fail[SearchData]("validation_failed", err.Error(), "current_workspace_path", nil)
		}
	}
	results, err := s.Index.Search(ctx, queryEmbedding, projectID, req.Limit)
	if err != nil {
		return Fail[SearchData]("index_failed", err.Error(), "", nil)
	}
	results = applySearchOptions(results, req)
	if results == nil {
		results = []SearchResult{}
	}
	return OK(SearchData{Results: results}, warnings...)
}

func (s *Service) Sync(ctx context.Context) Response[SyncResult] {
	if err := s.Repo.Ensure(ctx); err != nil {
		return Fail[SyncResult]("git_failed", err.Error(), "", nil)
	}
	var warnings []Warning
	if commits, _ := s.Repo.Unpushed(ctx); len(commits) > 0 {
		r := s.RetryPush(ctx, RetryPushRequest{})
		if !r.OK || !r.Data.Pushed {
			warnings = append(warnings, Warning{
				Code:    "sync_failed_local_state",
				Message: "retry push failed; sync continued from local repository state",
				Details: map[string]any{
					"unpushed_commit_count": len(commits),
					"recommended_action":    "retry_push",
				},
			})
		}
	}
	if len(warnings) == 0 {
		if err := s.Repo.PullRebase(ctx); err != nil {
			warnings = append(warnings, Warning{Code: "pull_failed_local_state", Message: "pull failed; sync continued from local repository state", Details: gitDetails(err)})
		}
	}
	if err := s.Resync(ctx); err != nil {
		return Fail[SyncResult]("index_failed", err.Error(), "", nil)
	}
	count, err := s.Index.Count(ctx)
	if err != nil {
		return Fail[SyncResult]("index_failed", err.Error(), "", nil)
	}
	commits, _ := s.Repo.Unpushed(ctx)
	return OK(SyncResult{IndexedDocumentCount: count, UnpushedCommitCount: len(commits)}, warnings...)
}

func (s *Service) Status(ctx context.Context) Response[StatusResult] {
	if err := s.Repo.Ensure(ctx); err != nil {
		return Fail[StatusResult]("git_failed", err.Error(), "", nil)
	}
	branch, _ := s.Repo.CurrentBranch(ctx)
	dirty, _ := s.Repo.Dirty(ctx)
	commits, _ := s.Repo.Unpushed(ctx)
	indexed := 0
	failed := 0
	if s.Index != nil {
		indexed, _ = s.Index.Count(ctx)
		failed, _ = s.Index.FailedEmbeddingCount(ctx)
	}
	return OK(StatusResult{
		GitDir:               s.Config.GitDir,
		RemoteURL:            s.Config.RemoteURL,
		CurrentBranch:        branch,
		UnpushedCommitCount:  len(commits),
		DirtyWorkingTree:     dirty,
		IndexPath:            s.Config.IndexPath,
		IndexedDocumentCount: indexed,
		FailedEmbeddingCount: failed,
		EmbeddingProvider:    s.Config.EmbeddingProvider,
		EmbeddingModel:       s.Config.EmbeddingModel,
		EmbeddingModelRepo:   s.Config.EmbeddingModelRepo,
		EmbeddingModelPath:   s.Config.EmbeddingModelPath,
	})
}

func (s *Service) RetryPush(ctx context.Context, req RetryPushRequest) Response[RetryPushResult] {
	commits, err := s.Repo.Unpushed(ctx)
	if err != nil {
		return Fail[RetryPushResult]("git_failed", err.Error(), "", gitDetails(err))
	}
	if req.DryRun {
		return OK(RetryPushResult{Pushed: len(commits) == 0, UnpushedCommitCount: len(commits), PushedCommitHashes: commits})
	}
	if len(commits) == 0 {
		return OK(RetryPushResult{Pushed: true, UnpushedCommitCount: 0})
	}
	if err := s.Repo.Push(ctx); err != nil {
		if ge, ok := err.(*GitError); ok && ge.Category == "remote_ahead" {
			if pullErr := s.Repo.PullRebase(ctx); pullErr != nil {
				return OK(RetryPushResult{Pushed: false, UnpushedCommitCount: len(commits), ErrorCategory: "remote_ahead"})
			}
			if pushErr := s.Repo.Push(ctx); pushErr == nil {
				return OK(RetryPushResult{Pushed: true, PushedCommitHashes: commits})
			}
		}
		category := "unknown"
		if ge, ok := err.(*GitError); ok {
			category = ge.Category
		}
		return OK(RetryPushResult{Pushed: false, UnpushedCommitCount: len(commits), ErrorCategory: category})
	}
	return OK(RetryPushResult{Pushed: true, PushedCommitHashes: commits})
}

func applySearchOptions(results []SearchResult, req SearchRequest) []SearchResult {
	fields := map[string]bool{}
	for _, f := range req.Fields {
		fields[strings.TrimSpace(f)] = true
	}
	useFields := len(fields) > 0
	for i := range results {
		if req.SnippetChars > 0 {
			results[i].Content = truncateRunes(results[i].Content, req.SnippetChars)
		}
		if !useFields {
			continue
		}
		score := results[i].Score
		next := SearchResult{Score: score}
		if fields["project_id"] {
			next.ProjectID = results[i].ProjectID
		}
		if fields["path"] {
			next.Path = results[i].Path
		}
		if fields["title"] {
			next.Title = results[i].Title
		}
		if fields["content"] {
			next.Content = results[i].Content
		}
		results[i] = next
	}
	return results
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func (s *Service) Resync(ctx context.Context) error {
	if s.Index == nil {
		return nil
	}
	root := filepath.Join(s.Config.GitDir, "projects")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		rel, err := filepath.Rel(s.Config.GitDir, path)
		if err != nil {
			return err
		}
		rawBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mem, ok := ParseMemoryMarkdown(rel, string(rawBytes))
		if !ok {
			return nil
		}
		hash, found, err := s.Index.FindByPath(ctx, rel)
		if err != nil {
			return err
		}
		if found && hash == mem.Hash {
			return nil
		}
		emb, err := s.Embedder.Embed(ctx, s.Config.EmbeddingDocumentPrefix+mem.Title+"\n\n"+mem.Content)
		if err != nil {
			return err
		}
		mem.Embedding = emb
		return s.Index.Upsert(ctx, mem, s.Config.EmbeddingProvider, s.Config.EmbeddingModel)
	})
}

func (s *Service) validateSave(req SaveRequest) error {
	if strings.TrimSpace(req.CurrentWorkspacePath) == "" {
		return fmt.Errorf("current_workspace_path is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return fmt.Errorf("content is required")
	}
	if len([]byte(req.Title)) > s.Config.Limits.MaxTitleBytes {
		return fmt.Errorf("title exceeds max_title_bytes")
	}
	contentBytes := len([]byte(req.Content))
	if contentBytes > s.Config.Limits.HardMaxContentBytes {
		return fmt.Errorf("content exceeds hard_max_content_bytes")
	}
	if contentBytes > s.Config.Limits.MaxContentBytes {
		return fmt.Errorf("content exceeds max_content_bytes")
	}
	return nil
}

func gitDetails(err error) map[string]any {
	if ge, ok := err.(*GitError); ok {
		return map[string]any{"category": ge.Category, "output": ge.Output}
	}
	return map[string]any{"error": err.Error()}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
