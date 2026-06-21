package gmem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

func DefaultConfig() Config {
	base := defaultDataDir()
	return Config{
		GitDir:                  filepath.Join(base, "repo"),
		IndexPath:               filepath.Join(base, "index.sqlite"),
		EmbeddingProvider:       "builtin_onnx",
		EmbeddingModel:          "multilingual-e5-small",
		EmbeddingModelRepo:      "intfloat/multilingual-e5-small",
		EmbeddingModelRevision:  "main",
		EmbeddingModelURL:       "https://huggingface.co/intfloat/multilingual-e5-small",
		EmbeddingQueryPrefix:    "query: ",
		EmbeddingDocumentPrefix: "passage: ",
		Limits: Limits{
			MaxTitleBytes:       512,
			MaxContentBytes:     65536,
			HardMaxContentBytes: 1048576,
		},
		SecurityPolicy: SecurityPolicy{
			RejectPersonalInformation: true,
			RejectOrganizationNames:   true,
			RejectCustomerNames:       true,
		},
	}
}

func DefaultConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "git-mcp-memory", "config.json")
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "git-mcp-memory", "config.json")
		}
	default:
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(v, "git-mcp-memory", "config.json")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "git-mcp-memory", "config.json")
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = DefaultConfigPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	def := DefaultConfig()
	if c.GitDir == "" {
		c.GitDir = def.GitDir
	}
	if c.IndexPath == "" {
		c.IndexPath = def.IndexPath
	}
	if c.EmbeddingProvider == "" {
		c.EmbeddingProvider = def.EmbeddingProvider
	}
	if c.EmbeddingModel == "" {
		c.EmbeddingModel = def.EmbeddingModel
	}
	if c.EmbeddingModelRepo == "" {
		c.EmbeddingModelRepo = def.EmbeddingModelRepo
	}
	if c.EmbeddingModelRevision == "" {
		c.EmbeddingModelRevision = def.EmbeddingModelRevision
	}
	if c.EmbeddingModelURL == "" {
		c.EmbeddingModelURL = def.EmbeddingModelURL
	}
	if c.EmbeddingQueryPrefix == "" {
		c.EmbeddingQueryPrefix = def.EmbeddingQueryPrefix
	}
	if c.EmbeddingDocumentPrefix == "" {
		c.EmbeddingDocumentPrefix = def.EmbeddingDocumentPrefix
	}
	if c.Limits.MaxTitleBytes == 0 {
		c.Limits.MaxTitleBytes = def.Limits.MaxTitleBytes
	}
	if c.Limits.MaxContentBytes == 0 {
		c.Limits.MaxContentBytes = def.Limits.MaxContentBytes
	}
	if c.Limits.HardMaxContentBytes == 0 {
		c.Limits.HardMaxContentBytes = def.Limits.HardMaxContentBytes
	}
}

func defaultDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "git-mcp-memory")
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "git-mcp-memory")
		}
	default:
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return filepath.Join(v, "git-mcp-memory")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "git-mcp-memory")
}
