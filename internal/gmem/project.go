package gmem

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var projectCharRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func DeriveProjectID(workspace string) (string, error) {
	if workspace == "" {
		return "", errors.New("workspace path is required")
	}
	clean := filepath.Clean(workspace)
	base := filepath.Base(clean)
	slug := sanitizeSegment(base)
	if slug == "" || slug == "." || slug == ".." {
		return "", errors.New("workspace path does not produce a valid project id")
	}
	hash := workspaceHash(clean)
	if remote := gitRemoteURL(clean); remote != "" {
		hash = workspaceHash(remote)
	}
	return slug + "-" + hash[:8], nil
}

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	s = projectCharRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	return s
}

func workspaceHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func gitRemoteURL(dir string) string {
	cmd := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
