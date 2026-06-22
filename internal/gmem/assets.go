package gmem

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func EnsureAssets(ctx context.Context, cfg Config) error {
	if err := ensureDownloaded(ctx, cfg.EmbeddingModelPath, hfResolveURL(cfg, "onnx/model.onnx")); err != nil {
		return err
	}
	if err := ensureDownloaded(ctx, cfg.EmbeddingTokenizerPath, hfResolveURL(cfg, "onnx/tokenizer.json")); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.ONNXRuntimePath); err == nil {
		return nil
	}
	return downloadONNXRuntime(ctx, cfg.ONNXRuntimePath)
}

func hfResolveURL(cfg Config, file string) string {
	revision := cfg.EmbeddingModelRevision
	if revision == "" {
		revision = "main"
	}
	return fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", cfg.EmbeddingModelRepo, revision, file)
}

func ensureDownloaded(ctx context.Context, path, url string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return downloadFile(ctx, url, path)
}

func downloadFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s: %s", url, resp.Status)
	}
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, path)
}

func downloadONNXRuntime(ctx context.Context, runtimePath string) error {
	url, member, err := onnxRuntimeArchive()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(runtimePath), "onnxruntime-download")
	if strings.HasSuffix(url, ".zip") {
		tmp += ".zip"
	} else {
		tmp += ".tgz"
	}
	if err := downloadFile(ctx, url, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if strings.HasSuffix(tmp, ".zip") {
		return extractZipMember(tmp, member, runtimePath)
	}
	return extractTarGzMember(tmp, member, runtimePath)
}

func onnxRuntimeArchive() (url, member string, err error) {
	const version = "1.26.0"
	base := "https://github.com/microsoft/onnxruntime/releases/download/v" + version + "/"
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		name := "onnxruntime-osx-arm64-" + version
		return base + name + ".tgz", name + "/lib/libonnxruntime.dylib", nil
	case "darwin/amd64":
		name := "onnxruntime-osx-x86_64-" + version
		return base + name + ".tgz", name + "/lib/libonnxruntime.dylib", nil
	case "linux/amd64":
		name := "onnxruntime-linux-x64-" + version
		return base + name + ".tgz", name + "/lib/libonnxruntime.so.1.26.0", nil
	case "linux/arm64":
		name := "onnxruntime-linux-aarch64-" + version
		return base + name + ".tgz", name + "/lib/libonnxruntime.so.1.26.0", nil
	case "windows/amd64":
		name := "onnxruntime-win-x64-" + version
		return base + name + ".zip", name + "/lib/onnxruntime.dll", nil
	default:
		return "", "", fmt.Errorf("unsupported onnxruntime platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func extractTarGzMember(archivePath, member, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("member not found: %s", member)
		}
		if err != nil {
			return err
		}
		if archiveMemberMatches(h.Name, member) {
			return writeExtracted(dest, tr)
		}
	}
}

func extractZipMember(archivePath, member, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !archiveMemberMatches(f.Name, member) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeExtracted(dest, rc)
	}
	return fmt.Errorf("member not found: %s", member)
}

func archiveMemberMatches(got, want string) bool {
	return strings.TrimPrefix(got, "./") == strings.TrimPrefix(want, "./")
}

func writeExtracted(dest string, r io.Reader) error {
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, r)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
