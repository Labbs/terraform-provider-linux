package system

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Download fetches url into the file p on the host, with curl or wget, and
// returns the SHA-256 digest of its content. When wantSHA256 is set, a file
// with another digest is rejected. The file is replaced atomically. The mode
// defaults to 0644.
func (c *Client) Download(ctx context.Context, url, p, wantSHA256 string, attrs Attributes, createParents bool) (string, error) {
	if attrs.Mode == "" {
		attrs.Mode = "0644"
	}
	lines := []string{
		"set -e",
		"p=" + q(p),
		"d=" + q(path.Dir(p)),
		"url=" + q(url),
		"want=" + q(wantSHA256),
		`if [ -d "$p" ]; then echo "$p is a directory" >&2; exit 1; fi`,
	}
	if createParents {
		lines = append(lines, `mkdir -p -- "$d"`)
	}
	lines = append(lines,
		// The temporary file lives next to p, so that mv is a rename.
		`tmp=$(mktemp "$d/.terraform-provider-linux.XXXXXX")`,
		`trap 'rm -f -- "$tmp"' EXIT`,
		`if command -v curl >/dev/null 2>&1; then`,
		`  curl --fail --silent --show-error --location --retry 3 --output "$tmp" --url "$url"`,
		`elif command -v wget >/dev/null 2>&1; then`,
		`  wget -q -O "$tmp" -- "$url"`,
		`else`,
		`  echo "downloading needs curl or wget on the host" >&2`,
		`  exit 1`,
		`fi`,
		`sum=$(sha256sum < "$tmp")`,
		`sum=${sum%% *}`,
		`if [ -n "$want" ] && [ "$sum" != "$want" ]; then`,
		`  echo "checksum mismatch: the downloaded file has the SHA-256 digest $sum, expected $want" >&2`,
		`  exit 1`,
		`fi`,
	)
	lines = append(lines, attrs.commands(`"$tmp"`)...)
	lines = append(lines,
		`mv -f -- "$tmp" "$p"`,
		"trap - EXIT",
		`echo "$sum"`,
	)
	out, err := c.exec(ctx, script(lines...), nil)
	if err != nil {
		return "", wrap(err, "download "+url+" to", p)
	}
	sum := strings.TrimSpace(string(out))
	if !sha256Pattern.MatchString(sum) {
		return "", fmt.Errorf("download %s to %s: unexpected sha256sum output %q", url, p, out)
	}
	return sum, nil
}

// Archive formats supported by ExtractArchive.
const (
	FormatTar    = "tar"
	FormatTarGz  = "tar.gz"
	FormatTarBz2 = "tar.bz2"
	FormatTarXz  = "tar.xz"
	FormatZip    = "zip"
	FormatGz     = "gz"
)

// ArchiveFormats lists the supported archive formats.
var ArchiveFormats = []string{FormatTar, FormatTarGz, FormatTarBz2, FormatTarXz, FormatZip, FormatGz}

// archiveExtensions maps file name suffixes to formats. Longer suffixes
// come first, so that .tar.gz is not taken for .gz.
var archiveExtensions = []struct{ suffix, format string }{
	{".tar.gz", FormatTarGz}, {".tgz", FormatTarGz},
	{".tar.bz2", FormatTarBz2}, {".tbz2", FormatTarBz2}, {".tbz", FormatTarBz2},
	{".tar.xz", FormatTarXz}, {".txz", FormatTarXz},
	{".tar", FormatTar},
	{".zip", FormatZip},
	{".gz", FormatGz},
}

// ArchiveFormat returns the format of the archive p, from its name.
func ArchiveFormat(p string) (string, bool) {
	name := strings.ToLower(path.Base(p))
	for _, ext := range archiveExtensions {
		if strings.HasSuffix(name, ext.suffix) && len(name) > len(ext.suffix) {
			return ext.format, true
		}
	}
	return "", false
}

// extractCommand returns the command extracting the archive "$src" into
// the current directory. Archive owners are not restored: extracted files
// belong to the user running the command. A gz file is decompressed into
// gzName.
func extractCommand(format, gzName string) (string, error) {
	switch format {
	case FormatTar:
		return `tar -xo -f "$src"`, nil
	case FormatTarGz:
		return `tar -xzo -f "$src"`, nil
	case FormatTarBz2:
		return `tar -xjo -f "$src"`, nil
	case FormatTarXz:
		return `tar -xJo -f "$src"`, nil
	case FormatZip:
		return `unzip -q -o "$src"`, nil
	case FormatGz:
		// Decompress a copy: gzip -d would delete the source.
		return `gzip -dc < "$src" > ` + q(gzName), nil
	}
	return "", fmt.Errorf("unsupported archive format %q", format)
}

// ExtractArchive extracts the archive src into the directory dest, created
// if needed, dropping the first stripComponents components of the paths in
// the archive. Files already in dest are replaced by renaming, so that
// running programs can be upgraded. It returns the paths of the extracted
// files, relative to dest and sorted.
func (c *Client) ExtractArchive(ctx context.Context, src, format, dest string, stripComponents int) ([]string, error) {
	if format == "" {
		f, ok := ArchiveFormat(src)
		if !ok {
			return nil, fmt.Errorf("extract %s: unknown archive format, set it explicitly", src)
		}
		format = f
	}
	// The file in a gz archive is named after the archive.
	gzName := path.Base(src)
	if format == FormatGz {
		if gzName = strings.TrimSuffix(gzName, ".gz"); gzName == path.Base(src) {
			return nil, fmt.Errorf("extract %s: the name of a gz file must end with .gz", src)
		}
	}
	extract, err := extractCommand(format, gzName)
	if err != nil {
		return nil, wrap(err, "extract", src)
	}

	// Each file is moved to its place: mv over an existing file is a
	// rename, which works even when the file is a running program.
	const move = `dest=$1 strip=$2
shift 2
for f; do
  rel=${f#./}
  i=0
  while [ "$i" -lt "$strip" ]; do
    case $rel in
      */*) rel=${rel#*/} ;;
      *) rel= ; break ;;
    esac
    i=$((i + 1))
  done
  [ -n "$rel" ] || continue
  case $rel in */*) mkdir -p -- "$dest/${rel%/*}" ;; esac
  if [ -d "$dest/$rel" ] && [ ! -L "$dest/$rel" ]; then
    echo "$dest/$rel is a directory" >&2
    exit 1
  fi
  mv -f -- "$f" "$dest/$rel"
  printf '%s\0' "$rel"
done`

	out, err := c.exec(ctx, script(
		"set -e",
		"src="+q(src),
		"dest="+q(dest),
		fmt.Sprintf(`[ -f "$src" ] || { echo "$src is not a file" >&2; exit %d; }`, exitNotFound),
		`mkdir -p -- "$dest"`,
		`tmp=$(mktemp -d "$dest/.terraform-provider-linux.XXXXXX")`,
		`trap 'rm -rf -- "$tmp"' EXIT`,
		`cd "$tmp"`,
		extract,
		"find . ! -type d -exec sh -c "+q(move)+" sh \"$dest\" "+strconv.Itoa(stripComponents)+" {} +",
	), nil)
	if err != nil {
		return nil, wrap(err, "extract", src)
	}

	files := splitNul(out)
	if len(files) == 0 {
		return nil, fmt.Errorf("extract %s: no file to extract (strip_components = %d)", src, stripComponents)
	}
	return files, nil
}

// splitNul splits NUL-terminated strings, dropping duplicates, and sorts
// them.
func splitNul(out []byte) []string {
	seen := map[string]bool{}
	var list []string
	for _, b := range bytes.Split(out, []byte{0}) {
		if s := string(b); s != "" && !seen[s] {
			seen[s] = true
			list = append(list, s)
		}
	}
	sort.Strings(list)
	return list
}

// checkRelative reports whether rel is a path the provider may manage
// under a directory: relative, canonical and inside it.
func checkRelative(rel string) error {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("invalid relative path %q", rel)
	}
	return nil
}

// MissingFiles returns the files, relative to dir, that do not exist.
func (c *Client) MissingFiles(ctx context.Context, dir string, files []string) ([]string, error) {
	lines := []string{fmt.Sprintf("cd %s 2>/dev/null || exit %d", q(dir), exitNotFound)}
	for _, f := range files {
		if err := checkRelative(f); err != nil {
			return nil, wrap(err, "check files in", dir)
		}
		lines = append(lines, fmt.Sprintf("[ -e %[1]s ] || [ -L %[1]s ] || printf '%%s\\0' %[1]s", q(f)))
	}
	out, err := c.exec(ctx, script(lines...), nil)
	if IsNotExist(err) {
		return files, nil
	}
	if err != nil {
		return nil, wrap(err, "check files in", dir)
	}
	return splitNul(out), nil
}

// RemoveFiles deletes the files, relative to dir, then the directories
// holding them that are left empty, but not dir itself.
func (c *Client) RemoveFiles(ctx context.Context, dir string, files []string) error {
	if err := CheckPath(dir); err != nil {
		return wrap(err, "remove files in", dir)
	}
	dirs := map[string]bool{}
	lines := []string{"cd " + q(dir) + " 2>/dev/null || exit 0"}
	for _, f := range files {
		if err := checkRelative(f); err != nil {
			return wrap(err, "remove files in", dir)
		}
		lines = append(lines, "rm -f -- "+q(f))
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	// Deepest directories first.
	ordered := make([]string, 0, len(dirs))
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if di, dj := strings.Count(ordered[i], "/"), strings.Count(ordered[j], "/"); di != dj {
			return di > dj
		}
		return ordered[i] < ordered[j]
	})
	for _, d := range ordered {
		lines = append(lines, "rmdir -- "+q(d)+" 2>/dev/null || true")
	}
	_, err := c.exec(ctx, script(lines...), nil)
	return wrap(err, "remove files in", dir)
}
