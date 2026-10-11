package transport

import "strings"

// Quote returns s quoted so that a POSIX shell reads it back as a single word.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, needsQuoting) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Join quotes each argument and joins them into a command line.
func Join(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func needsQuoting(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("@%_-+:,./", r)
}
