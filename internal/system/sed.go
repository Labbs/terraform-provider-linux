package system

import (
	"context"
	"fmt"
	"strings"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// SedResult tells what applying sed expressions to a file did.
type SedResult struct {
	// Changed is true when the file content changed.
	Changed bool
	// Idempotent is false when applying the expressions again would change
	// the file again, e.g. s/a/aa/.
	Idempotent bool
}

// sedPrologue runs sed on $p into $tmp and leaves the script if that
// changes nothing.
func sedPrologue(p string, expressions []string, extended bool) []string {
	sed := sedCommand(expressions, extended) + ` -- "$p" > "$tmp"`
	return []string{
		"set -e",
		"p=" + q(p),
		fmt.Sprintf(`[ -f "$p" ] || exit %d`, exitNotFound),
		`tmp=$(mktemp)`,
		`trap 'rm -f -- "$tmp"' EXIT`,
		sed,
		`if [ "$(sha256sum < "$tmp")" = "$(sha256sum < "$p")" ]; then echo unchanged; exit 0; fi`,
	}
}

func sedCommand(expressions []string, extended bool) string {
	args := []string{"sed"}
	if extended {
		args = append(args, "-E")
	}
	for _, expr := range expressions {
		args = append(args, "-e", expr)
	}
	return transport.Join(args...)
}

// SedPending reports whether applying the expressions would change p.
func (c *Client) SedPending(ctx context.Context, p string, expressions []string, extended bool) (bool, error) {
	lines := append(sedPrologue(p, expressions, extended), "echo changed")
	out, err := c.exec(ctx, script(lines...), nil)
	if err != nil {
		return false, wrap(err, "sed", p)
	}
	return strings.TrimSpace(string(out)) == "changed", nil
}

// Sed applies the expressions to p in place. The file keeps its inode, so
// its ownership, permissions and hard links are preserved.
func (c *Client) Sed(ctx context.Context, p string, expressions []string, extended bool) (SedResult, error) {
	defer c.lock("file:" + p)()
	lines := append(sedPrologue(p, expressions, extended),
		`cat -- "$tmp" > "$p"`,
		sedCommand(expressions, extended)+` -- "$p" > "$tmp"`,
		`if [ "$(sha256sum < "$tmp")" = "$(sha256sum < "$p")" ]; then echo changed; else echo changed-again; fi`,
	)
	out, err := c.exec(ctx, script(lines...), nil)
	if err != nil {
		return SedResult{}, wrap(err, "sed", p)
	}
	switch strings.TrimSpace(string(out)) {
	case "unchanged":
		return SedResult{Idempotent: true}, nil
	case "changed":
		return SedResult{Changed: true, Idempotent: true}, nil
	default:
		return SedResult{Changed: true}, nil
	}
}

// SedPreview returns the content p would have after applying the
// expressions, without changing it.
func (c *Client) SedPreview(ctx context.Context, p string, expressions []string, extended bool) ([]byte, error) {
	out, err := c.exec(ctx, script(
		"p="+q(p),
		fmt.Sprintf(`[ -f "$p" ] || exit %d`, exitNotFound),
		"exec "+sedCommand(expressions, extended)+` -- "$p"`,
	), nil)
	return out, wrap(err, "sed", p)
}
