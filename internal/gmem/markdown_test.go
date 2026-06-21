package gmem

import (
	"strings"
	"testing"
	"time"
)

func TestRenderAndParseMemoryMarkdown(t *testing.T) {
	now := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	raw := RenderMemoryMarkdown("proj-123", "Decision", "Body\ntext", "test", now)
	if !strings.Contains(raw, "type: Memory") {
		t.Fatal("missing OKF type")
	}
	mem, ok := ParseMemoryMarkdown("projects/proj-123/file.md", raw)
	if !ok {
		t.Fatal("parse failed")
	}
	if mem.ProjectID != "proj-123" || mem.Title != "Decision" || mem.Content != "Body\ntext" {
		t.Fatalf("unexpected parse result: %+v", mem)
	}
}

func TestUniqueMemoryFilename(t *testing.T) {
	now := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	got := UniqueMemoryFilename("My Decision!", now, "a1b2c3")
	want := "my-decision_20260622_010203_a1b2c3.md"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
