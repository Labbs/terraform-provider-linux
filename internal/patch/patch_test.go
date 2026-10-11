package patch

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sshdConfig = `# sshd config
Port 22
PermitRootLogin yes
PasswordAuthentication yes
UsePAM yes
`

const sshdDiff = `--- a/etc/ssh/sshd_config
+++ b/etc/ssh/sshd_config
@@ -1,5 +1,5 @@
 # sshd config
 Port 22
-PermitRootLogin yes
-PasswordAuthentication yes
+PermitRootLogin prohibit-password
+PasswordAuthentication no
 UsePAM yes
`

const sshdPatched = `# sshd config
Port 22
PermitRootLogin prohibit-password
PasswordAuthentication no
UsePAM yes
`

func mustParse(t *testing.T, diff string) *Patch {
	t.Helper()
	p, err := Parse(diff)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyAndRevert(t *testing.T) {
	p := mustParse(t, sshdDiff)

	got, err := Apply(sshdConfig, p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != sshdPatched {
		t.Fatalf("apply:\n%s", got)
	}
	back, err := Apply(got, p, true)
	if err != nil {
		t.Fatal(err)
	}
	if back != sshdConfig {
		t.Fatalf("revert:\n%s", back)
	}

	if s := Check(sshdConfig, p); s != Applicable {
		t.Errorf("Check(original) = %v", s)
	}
	if s := Check(sshdPatched, p); s != Applied {
		t.Errorf("Check(patched) = %v", s)
	}
	if s := Check("something else\n", p); s != Conflict {
		t.Errorf("Check(other) = %v", s)
	}
}

func TestApplyWithOffset(t *testing.T) {
	p := mustParse(t, sshdDiff)
	header := "# managed by hand\n# another line\n"
	got, err := Apply(header+sshdConfig+"Extra yes\n", p, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := header + sshdPatched + "Extra yes\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestNoNewlineAtEOF(t *testing.T) {
	diff := `--- a
+++ b
@@ -1,2 +1,2 @@
 first
-second
\ No newline at end of file
+second
`
	p := mustParse(t, diff)
	got, err := Apply("first\nsecond", p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "first\nsecond\n" {
		t.Fatalf("got %q", got)
	}
	if s := Check("first\nsecond\n", p); s != Applied {
		t.Errorf("Check = %v", s)
	}
}

func TestInsertIntoEmptyFile(t *testing.T) {
	p := mustParse(t, "@@ -0,0 +1,2 @@\n+hello\n+world\n")
	got, err := Apply("", p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\nworld\n" {
		t.Fatalf("got %q", got)
	}
	if s := Check(got, p); s != Applied {
		t.Errorf("Check = %v", s)
	}
}

func TestStrippedContextLine(t *testing.T) {
	// The blank context line lost its leading space, as editors often do.
	diff := "@@ -1,3 +1,3 @@\n a\n\n-b\n+c\n"
	p := mustParse(t, diff)
	got, err := Apply("a\n\nb\n", p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\n\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"no hunk":        "just text\n",
		"multiple files": "--- a\n+++ a\n@@ -1 +1 @@\n-x\n+y\n--- b\n+++ b\n@@ -1 +1 @@\n-x\n+y\n",
		"short hunk":     "@@ -1,3 +1,3 @@\n a\n-b\n",
		"bad line":       "@@ -1,2 +1,2 @@\n a\n*b\n",
	}
	for name, diff := range tests {
		if _, err := Parse(diff); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// TestAgainstGitDiff checks random edits against the diffs git produces.
// BSD diff is not used as a reference: with no context, it numbers an
// insertion at the top of a file as "-1,0" instead of "-0,0".
func TestAgainstGitDiff(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 200 {
		before := randomLines(rng, rng.IntN(40))
		after := mutate(rng, before)
		oldText, newText := strings.Join(before, ""), strings.Join(after, "")
		if rng.IntN(5) == 0 && newText != "" {
			newText = strings.TrimSuffix(newText, "\n")
		}

		oldFile, newFile := filepath.Join(dir, "old"), filepath.Join(dir, "new")
		if err := os.WriteFile(oldFile, []byte(oldText), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newFile, []byte(newText), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(gitPath, "diff", "--no-index", "--no-color", fmt.Sprintf("-U%d", rng.IntN(4)), oldFile, newFile).Output()
		var exitErr *exec.ExitError
		if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
			t.Fatal(err)
		}
		if len(out) == 0 {
			continue
		}

		p, err := Parse(string(out))
		if err != nil {
			t.Fatalf("case %d: parse: %v\n%s", i, err, out)
		}
		got, err := Apply(oldText, p, false)
		if err != nil || got != newText {
			t.Fatalf("case %d: apply = %q, %v; want %q\n%s", i, got, err, newText, out)
		}
		back, err := Apply(newText, p, true)
		if err != nil || back != oldText {
			t.Fatalf("case %d: revert = %q, %v; want %q\n%s", i, back, err, oldText, out)
		}
	}
}

func randomLines(rng *rand.Rand, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d\n", rng.IntN(8))
	}
	return lines
}

func mutate(rng *rand.Rand, lines []string) []string {
	out := append([]string(nil), lines...)
	for range rng.IntN(5) + 1 {
		switch pos := rng.IntN(len(out) + 1); {
		case rng.IntN(2) == 0 || len(out) == 0 || pos == len(out):
			out = append(out[:pos], append([]string{fmt.Sprintf("new %d\n", rng.IntN(100))}, out[pos:]...)...)
		default:
			out = append(out[:pos], out[pos+1:]...)
		}
	}
	return out
}
