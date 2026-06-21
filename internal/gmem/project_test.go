package gmem

import (
	"strings"
	"testing"
)

func TestDeriveProjectIDIncludesHash(t *testing.T) {
	got, err := DeriveProjectID("/tmp/foo-api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "foo-api-") {
		t.Fatalf("unexpected project id: %s", got)
	}
	if len(got) <= len("foo-api-") {
		t.Fatalf("missing hash suffix: %s", got)
	}
}
