package gmem

import "testing"

func TestValidateSaveRejectsOversizedContent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxContentBytes = 5
	cfg.Limits.HardMaxContentBytes = 10
	svc := NewService(cfg, nil, HashEmbedder{})
	err := svc.validateSave(SaveRequest{CurrentWorkspacePath: "/tmp/proj", Title: "t", Content: "123456"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateSaveRequiresContent(t *testing.T) {
	svc := NewService(DefaultConfig(), nil, HashEmbedder{})
	err := svc.validateSave(SaveRequest{CurrentWorkspacePath: "/tmp/proj", Title: "t"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
