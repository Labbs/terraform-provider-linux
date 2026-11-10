package system

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

func TestArchiveFormat(t *testing.T) {
	tests := map[string]string{
		"/tmp/node_exporter-1.8.2.linux-amd64.tar.gz": FormatTarGz,
		"/tmp/a.TGZ":     FormatTarGz,
		"/tmp/a.tar.bz2": FormatTarBz2,
		"/tmp/a.tbz2":    FormatTarBz2,
		"/tmp/a.tar.xz":  FormatTarXz,
		"/tmp/a.txz":     FormatTarXz,
		"/tmp/a.tar":     FormatTar,
		"/tmp/a.zip":     FormatZip,
		"/tmp/tool.gz":   FormatGz,
		"/tmp/a.rar":     "",
		"/tmp/.gz":       "",
		"/tmp/archive":   "",
	}
	for p, want := range tests {
		got, ok := ArchiveFormat(p)
		if got != want || ok != (want != "") {
			t.Errorf("ArchiveFormat(%s) = %q, %v; want %q", p, got, ok, want)
		}
	}
}

type archiveEntry struct {
	name    string
	content string
	mode    int64
}

var testEntries = []archiveEntry{
	{name: "pkg-1.0/tool", content: "#!/bin/sh\necho tool\n", mode: 0o755},
	{name: "pkg-1.0/LICENSE", content: "license\n", mode: 0o644},
	{name: "pkg-1.0/share/doc/README", content: "readme\n", mode: 0o644},
}

func writeTar(t *testing.T, p string, compress func(*bytes.Buffer) []byte) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "pkg-1.0/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: 4242, Gid: 4242})
	for _, e := range testEntries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.content)), Uid: 4242, Gid: 4242}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(e.content))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, compress(&buf), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gzipped(buf *bytes.Buffer) []byte {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	_, _ = zw.Write(buf.Bytes())
	_ = zw.Close()
	return out.Bytes()
}

// compressWith compresses with an external tool, such as bzip2 or xz.
func compressWith(t *testing.T, tool string) func(*bytes.Buffer) []byte {
	return func(buf *bytes.Buffer) []byte {
		cmd := exec.Command(tool, "-c")
		cmd.Stdin = buf
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}
}

func writeZip(t *testing.T, p string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range testEntries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(os.FileMode(e.mode))
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

func TestExtractArchive(t *testing.T) {
	requireTools(t, "tar", "gzip", "sha256sum")
	c := New(transport.NewLocal())
	ctx := context.Background()

	tests := []struct {
		name   string
		file   string
		tools  []string
		write  func(t *testing.T, p string)
		strip  int
		prefix string
	}{
		{name: "tar.gz", file: "pkg.tar.gz", strip: 1, write: func(t *testing.T, p string) { writeTar(t, p, gzipped) }},
		{name: "tar.gz without strip", file: "pkg.tgz", prefix: "pkg-1.0/", write: func(t *testing.T, p string) { writeTar(t, p, gzipped) }},
		{name: "tar", file: "pkg.tar", strip: 1, write: func(t *testing.T, p string) {
			writeTar(t, p, func(b *bytes.Buffer) []byte { return b.Bytes() })
		}},
		{name: "tar.bz2", file: "pkg.tar.bz2", strip: 1, tools: []string{"bzip2"}, write: func(t *testing.T, p string) { writeTar(t, p, compressWith(t, "bzip2")) }},
		{name: "tar.xz", file: "pkg.tar.xz", strip: 1, tools: []string{"xz"}, write: func(t *testing.T, p string) { writeTar(t, p, compressWith(t, "xz")) }},
		{name: "zip", file: "pkg.zip", strip: 1, tools: []string{"unzip"}, write: writeZip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireTools(t, tt.tools...)
			dir := t.TempDir()
			src := filepath.Join(dir, tt.file)
			tt.write(t, src)
			dest := filepath.Join(dir, "dest")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			// An unrelated file, and a file the archive replaces.
			_ = os.WriteFile(filepath.Join(dest, "keep"), []byte("keep"), 0o644)
			_ = os.WriteFile(filepath.Join(dest, tt.prefix+"tool"), []byte("old"), 0o644)

			files, err := c.ExtractArchive(ctx, src, "", dest, tt.strip)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{tt.prefix + "LICENSE", tt.prefix + "share/doc/README", tt.prefix + "tool"}
			if !reflect.DeepEqual(files, want) {
				t.Fatalf("files = %q, want %q", files, want)
			}
			tool, err := os.Stat(filepath.Join(dest, tt.prefix+"tool"))
			if err != nil {
				t.Fatal(err)
			}
			if tool.Mode().Perm() != 0o755 {
				t.Errorf("tool mode = %s", tool.Mode())
			}
			if got, _ := os.ReadFile(filepath.Join(dest, tt.prefix+"tool")); string(got) != testEntries[0].content {
				t.Errorf("tool = %q", got)
			}
			if entries, _ := filepath.Glob(filepath.Join(dest, ".terraform-provider-linux.*")); len(entries) != 0 {
				t.Errorf("temporary files left: %v", entries)
			}

			missing, err := c.MissingFiles(ctx, dest, files)
			if err != nil || len(missing) != 0 {
				t.Fatalf("missing = %q, %v", missing, err)
			}
			_ = os.Remove(filepath.Join(dest, tt.prefix+"LICENSE"))
			missing, err = c.MissingFiles(ctx, dest, files)
			if err != nil || !reflect.DeepEqual(missing, []string{tt.prefix + "LICENSE"}) {
				t.Fatalf("missing = %q, %v", missing, err)
			}

			if err := c.RemoveFiles(ctx, dest, files); err != nil {
				t.Fatal(err)
			}
			left, _ := os.ReadDir(dest)
			if len(left) != 1 || left[0].Name() != "keep" {
				t.Errorf("left in destination: %v", left)
			}
		})
	}
}

func TestExtractGz(t *testing.T) {
	requireTools(t, "gzip")
	c := New(transport.NewLocal())
	dir := t.TempDir()
	src := filepath.Join(dir, "tool.gz")
	if err := os.WriteFile(src, gzipped(bytes.NewBufferString("binary")), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "bin")
	files, err := c.ExtractArchive(context.Background(), src, "", dest, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{"tool"}) {
		t.Fatalf("files = %q", files)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "tool")); string(got) != "binary" {
		t.Errorf("tool = %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the archive was removed: %v", err)
	}
}

func TestExtractArchiveErrors(t *testing.T) {
	requireTools(t, "tar", "gzip")
	c := New(transport.NewLocal())
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "pkg.tar.gz")
	writeTar(t, src, gzipped)

	if _, err := c.ExtractArchive(ctx, src, "", filepath.Join(dir, "a"), 5); err == nil || !strings.Contains(err.Error(), "no file") {
		t.Errorf("expected an error with too many components stripped, got %v", err)
	}
	if _, err := c.ExtractArchive(ctx, filepath.Join(dir, "missing.tar.gz"), "", filepath.Join(dir, "b"), 0); !IsNotExist(err) {
		t.Errorf("expected a not found error, got %v", err)
	}
	if _, err := c.ExtractArchive(ctx, filepath.Join(dir, "archive"), "", filepath.Join(dir, "c"), 0); err == nil {
		t.Error("expected an unknown format error")
	}
	// A directory where the archive has a file.
	dest := filepath.Join(dir, "d")
	_ = os.MkdirAll(filepath.Join(dest, "tool", "sub"), 0o755)
	if _, err := c.ExtractArchive(ctx, src, "", dest, 1); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("expected a directory error, got %v", err)
	}
	if err := c.RemoveFiles(ctx, dest, []string{"../escape"}); err == nil {
		t.Error("expected an error for a path outside of the directory")
	}
	missing, err := c.MissingFiles(ctx, filepath.Join(dir, "nowhere"), []string{"a", "b"})
	if err != nil || len(missing) != 2 {
		t.Errorf("missing = %q, %v", missing, err)
	}
}

func TestDownload(t *testing.T) {
	requireTools(t, "curl", "sha256sum")
	c := New(transport.NewLocal())
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.bin")
	content := []byte("downloaded content\n")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	want := hex.EncodeToString(digest[:])
	url := "file://" + src

	p := filepath.Join(dir, "sub", "dir", "file.bin")
	sum, err := c.Download(ctx, url, p, want, Attributes{Mode: "0755"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if sum != want {
		t.Errorf("sum = %s, want %s", sum, want)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %s", info.Mode())
	}

	// A wrong checksum leaves the file as it was.
	other := strings.Repeat("0", 64)
	if _, err := c.Download(ctx, url, p, other, Attributes{}, true); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected a checksum error, got %v", err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, content) {
		t.Errorf("content = %q", got)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "sub", "dir", ".terraform-provider-linux.*")); len(entries) != 0 {
		t.Errorf("temporary files left: %v", entries)
	}

	if _, err := c.Download(ctx, "file://"+filepath.Join(dir, "missing"), filepath.Join(dir, "x"), "", Attributes{}, true); err == nil {
		t.Error("expected an error for a missing URL")
	}
}
