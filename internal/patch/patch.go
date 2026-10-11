// Package patch applies unified diffs to the content of a single file.
//
// Hunks must match exactly (no fuzz), but may be found away from the line
// numbers they announce, the way patch(1) handles offsets. Applying in
// reverse undoes a patch, which also tells whether a patch is applied.
package patch

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Patch is a parsed unified diff touching a single file.
type Patch struct {
	Hunks []Hunk
}

// Hunk is a contiguous change. Lines keep their line terminator, except a
// last line marked "\ No newline at end of file".
type Hunk struct {
	OldStart int
	NewStart int
	Old      []string
	New      []string
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Parse parses a unified diff. Lines outside hunks, such as file headers,
// are ignored, but the diff must not touch more than one file.
func Parse(diff string) (*Patch, error) {
	lines := splitLines(diff)
	p := &Patch{}
	files := 0
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			files++
			if files > 1 {
				return nil, errors.New("the diff touches more than one file")
			}
			i += 2
		case strings.HasPrefix(line, "@@ "):
			hunk, n, err := parseHunk(lines[i:])
			if err != nil {
				return nil, fmt.Errorf("hunk #%d: %w", len(p.Hunks)+1, err)
			}
			p.Hunks = append(p.Hunks, hunk)
			i += n
		default:
			i++
		}
	}
	if len(p.Hunks) == 0 {
		return nil, errors.New("no hunk found: expected a unified diff (diff -u)")
	}
	return p, nil
}

func parseHunk(lines []string) (Hunk, int, error) {
	m := hunkHeader.FindStringSubmatch(lines[0])
	if m == nil {
		return Hunk{}, 0, fmt.Errorf("malformed header %q", strings.TrimRight(lines[0], "\r\n"))
	}
	h := Hunk{OldStart: atoi(m[1]), NewStart: atoi(m[3])}
	oldCount, newCount := 1, 1
	if m[2] != "" {
		oldCount = atoi(m[2])
	}
	if m[4] != "" {
		newCount = atoi(m[4])
	}

	// lastOld and lastNew tell which blocks the previous line went to, for
	// the "\ No newline at end of file" marker.
	var lastOld, lastNew bool
	n := 1
	for ; n < len(lines); n++ {
		line := lines[n]
		if strings.HasPrefix(line, `\`) {
			if lastOld {
				h.Old[len(h.Old)-1] = strings.TrimSuffix(h.Old[len(h.Old)-1], "\n")
			}
			if lastNew {
				h.New[len(h.New)-1] = strings.TrimSuffix(h.New[len(h.New)-1], "\n")
			}
			lastOld, lastNew = false, false
			continue
		}
		if len(h.Old) == oldCount && len(h.New) == newCount {
			break
		}

		var text string
		var op byte = ' '
		if line != "\n" && line != "\r\n" {
			// Editors often strip the space of empty context lines.
			op, text = line[0], line[1:]
		} else {
			text = line
		}
		// Every line ends with a newline unless marked otherwise, even the
		// last line of a diff that lacks one.
		text = strings.TrimSuffix(text, "\n") + "\n"

		switch op {
		case ' ':
			h.Old = append(h.Old, text)
			h.New = append(h.New, text)
			lastOld, lastNew = true, true
		case '-':
			h.Old = append(h.Old, text)
			lastOld, lastNew = true, false
		case '+':
			h.New = append(h.New, text)
			lastOld, lastNew = false, true
		default:
			return Hunk{}, 0, fmt.Errorf("unexpected line %q", strings.TrimRight(line, "\r\n"))
		}
		if len(h.Old) > oldCount || len(h.New) > newCount {
			return Hunk{}, 0, errors.New("more lines than announced in the header")
		}
	}
	if len(h.Old) != oldCount || len(h.New) != newCount {
		return Hunk{}, 0, errors.New("fewer lines than announced in the header")
	}
	return h, n, nil
}

// Apply applies p to content, or reverts it when reverse is true.
func Apply(content string, p *Patch, reverse bool) (string, error) {
	lines := splitLines(content)
	var out []string
	pos := 0    // lines before pos have been copied to out
	offset := 0 // shift between announced and actual positions so far
	for i, h := range p.Hunks {
		from, to, start := h.Old, h.New, h.OldStart
		if reverse {
			from, to, start = h.New, h.Old, h.NewStart
		}
		// A hunk without old lines inserts after line start; otherwise its
		// first line is line start.
		expected := start
		if len(from) > 0 {
			expected--
		}
		idx := find(lines, from, expected+offset, pos)
		if idx < 0 {
			return "", fmt.Errorf("hunk #%d (line %d) does not match", i+1, start)
		}
		offset = idx - expected
		out = append(out, lines[pos:idx]...)
		out = append(out, to...)
		pos = idx + len(from)
	}
	out = append(out, lines[pos:]...)
	return strings.Join(out, ""), nil
}

// Status describes how a patch relates to some content.
type Status int

const (
	// Conflict means the patch can neither be applied nor reverted.
	Conflict Status = iota
	// Applied means the content already contains the patch.
	Applied
	// Applicable means the patch can be applied to the content.
	Applicable
)

// Check tells whether p is applied to content, can be applied, or neither.
func Check(content string, p *Patch) Status {
	if _, err := Apply(content, p, true); err == nil {
		return Applied
	}
	if _, err := Apply(content, p, false); err == nil {
		return Applicable
	}
	return Conflict
}

// find returns the index of block in lines closest to want, not before
// first, or -1.
func find(lines, block []string, want, first int) int {
	last := len(lines) - len(block)
	if last < first {
		return -1
	}
	want = max(first, min(want, last))
	for delta := 0; ; delta++ {
		before, after := want-delta, want+delta
		if before < first && after > last {
			return -1
		}
		if after <= last && matches(lines[after:], block) {
			return after
		}
		if delta > 0 && before >= first && matches(lines[before:], block) {
			return before
		}
	}
}

func matches(lines, block []string) bool {
	for i, line := range block {
		if lines[i] != line {
			return false
		}
	}
	return true
}

// splitLines splits s after each newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
