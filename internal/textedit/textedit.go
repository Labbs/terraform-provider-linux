// Package textedit edits the content of text files line by line: single
// lines in the manner of Ansible's lineinfile, blocks between marker lines
// like blockinfile, and crontab entries.
package textedit

import (
	"regexp"
	"strings"
)

// lines is the content of a file split into lines without terminators.
type lines struct {
	items []string
	// eol tells whether the last line ended with a newline.
	eol bool
}

func split(content string) lines {
	if content == "" {
		return lines{eol: true}
	}
	items := strings.Split(content, "\n")
	eol := items[len(items)-1] == ""
	if eol {
		items = items[:len(items)-1]
	}
	return lines{items: items, eol: eol}
}

func (l lines) String() string {
	if len(l.items) == 0 {
		return ""
	}
	s := strings.Join(l.items, "\n")
	if l.eol {
		s += "\n"
	}
	return s
}

func (l *lines) insert(i int, items ...string) {
	if i == len(l.items) {
		// Appending: the previous last line needs its newline back.
		l.eol = true
	}
	l.items = append(l.items[:i], append(items, l.items[i:]...)...)
}

func (l lines) index(line string) int {
	for i, item := range l.items {
		if item == line {
			return i
		}
	}
	return -1
}

func (l lines) firstMatch(re *regexp.Regexp) int {
	for i, item := range l.items {
		if re.MatchString(item) {
			return i
		}
	}
	return -1
}

func (l lines) lastMatch(re *regexp.Regexp) int {
	for i := len(l.items) - 1; i >= 0; i-- {
		if re.MatchString(l.items[i]) {
			return i
		}
	}
	return -1
}

// Position tells where to insert new lines. The zero value appends them.
type Position struct {
	// After inserts after the last line matching it.
	After *regexp.Regexp
	// Before inserts before the first line matching it. It takes precedence
	// over After.
	Before *regexp.Regexp
}

// index returns where to insert in l: the end when nothing matches.
func (p Position) index(l lines) int {
	if p.Before != nil {
		if i := l.firstMatch(p.Before); i >= 0 {
			return i
		}
	}
	if p.After != nil {
		if i := l.lastMatch(p.After); i >= 0 {
			return i + 1
		}
	}
	return len(l.items)
}

// Line describes a line that must be present in a file.
type Line struct {
	Line string
	// Match, when set, selects the line to replace: the last one matching.
	// It should match Line too, so that a replaced line is found again.
	Match *regexp.Regexp
	// Position tells where to insert Line when no line matches Match.
	Position Position
}

// Present reports whether content contains the line: when lines match
// Match, the last of them must be Line; otherwise, any line must be Line.
func (s Line) Present(content string) bool {
	l := split(content)
	if s.Match != nil {
		if i := l.lastMatch(s.Match); i >= 0 {
			return l.items[i] == s.Line
		}
	}
	return l.index(s.Line) >= 0
}

// Ensure returns content with the line present.
func (s Line) Ensure(content string) string {
	l := split(content)
	if s.Match != nil {
		if i := l.lastMatch(s.Match); i >= 0 {
			l.items[i] = s.Line
			return l.String()
		}
	}
	if l.index(s.Line) >= 0 {
		return content
	}
	l.insert(s.Position.index(l), s.Line)
	return l.String()
}

// Remove returns content without the lines equal to s.Line.
func (s Line) Remove(content string) string {
	return RemoveLines(content, func(line string) bool { return line == s.Line })
}

// RemoveLines returns content without the lines for which drop is true.
func RemoveLines(content string, drop func(string) bool) string {
	l := split(content)
	kept := l.items[:0]
	for _, item := range l.items {
		if !drop(item) {
			kept = append(kept, item)
		}
	}
	l.items = kept
	return l.String()
}

// Block is a group of lines kept between two marker lines.
type Block struct {
	Begin string
	End   string
	// Content is the text between the markers.
	Content string
	// Position tells where to insert the block when the markers are absent.
	Position Position
}

// find returns the indexes of the markers, or -1.
func (b Block) find(l lines) (int, int) {
	begin := l.index(b.Begin)
	if begin < 0 {
		return -1, -1
	}
	for i := begin + 1; i < len(l.items); i++ {
		if l.items[i] == b.End {
			return begin, i
		}
	}
	return -1, -1
}

// Find returns the text between the markers, and whether they are present.
func (b Block) Find(content string) (string, bool) {
	l := split(content)
	begin, end := b.find(l)
	if begin < 0 {
		return "", false
	}
	inner := lines{items: l.items[begin+1 : end], eol: true}
	return inner.String(), true
}

// Ensure returns content with the block present and holding b.Content.
func (b Block) Ensure(content string) string {
	l := split(content)
	block := append([]string{b.Begin}, split(b.Content).items...)
	block = append(block, b.End)
	if begin, end := b.find(l); begin >= 0 {
		l.items = append(l.items[:begin], append(block, l.items[end+1:]...)...)
		return l.String()
	}
	l.insert(b.Position.index(l), block...)
	return l.String()
}

// Remove returns content without the block and its markers.
func (b Block) Remove(content string) string {
	l := split(content)
	begin, end := b.find(l)
	if begin < 0 {
		return content
	}
	l.items = append(l.items[:begin], l.items[end+1:]...)
	return l.String()
}

// SameText reports whether two texts are equal, ignoring a final newline.
func SameText(a, b string) bool {
	return strings.TrimSuffix(a, "\n") == strings.TrimSuffix(b, "\n")
}
