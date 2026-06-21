package gmem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func RenderMemoryMarkdown(projectID, title, content, source string, createdAt time.Time) string {
	var b bytes.Buffer
	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b, "type: Memory")
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&b, "description: %s\n", yamlScalar(firstLine(content)))
	fmt.Fprintln(&b, "resource: null")
	fmt.Fprintln(&b, "tags: []")
	fmt.Fprintf(&b, "timestamp: %s\n", createdAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "project_id: %s\n", yamlScalar(projectID))
	fmt.Fprintf(&b, "source: %s\n", yamlScalar(source))
	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, content)
	return b.String()
}

func ContentHash(title, content string) string {
	sum := sha256.Sum256([]byte(title + "\n\n" + content))
	return hex.EncodeToString(sum[:])
}

func UniqueMemoryFilename(title string, now time.Time, randomSuffix string) string {
	prefix := sanitizeSegment(strings.ToLower(title))
	if prefix == "" {
		prefix = "memory"
	}
	if len(prefix) > 32 {
		prefix = prefix[:32]
		prefix = strings.Trim(prefix, "-.")
	}
	return fmt.Sprintf("%s_%s_%s.md", prefix, now.UTC().Format("20060102_150405"), randomSuffix)
}

var frontMatterRE = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n\n?(.*)$`)

func ParseMemoryMarkdown(path, raw string) (Memory, bool) {
	matches := frontMatterRE.FindStringSubmatch(raw)
	if len(matches) != 3 {
		return Memory{}, false
	}
	meta, body := matches[1], matches[2]
	mem := Memory{Path: path, Content: strings.TrimRight(body, "\n")}
	for _, line := range strings.Split(meta, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.Trim(strings.TrimSpace(v), `"`)
		switch key {
		case "title":
			mem.Title = val
		case "project_id":
			mem.ProjectID = val
		case "timestamp":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				mem.CreatedAt = t
			}
		}
	}
	if mem.Hash == "" {
		mem.Hash = ContentHash(mem.Title, mem.Content)
	}
	return mem, true
}

func yamlScalar(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 160 {
				return line[:160]
			}
			return line
		}
	}
	return ""
}
