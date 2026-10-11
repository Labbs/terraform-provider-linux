package textedit

import (
	"regexp"
	"testing"
)

const sshd = "Port 22\n#PermitRootLogin yes\nPasswordAuthentication yes\n"

func TestLineEnsure(t *testing.T) {
	tests := []struct {
		name    string
		spec    Line
		content string
		want    string
	}{
		{
			name:    "replace last match",
			spec:    Line{Line: "PermitRootLogin no", Match: regexp.MustCompile(`^#?PermitRootLogin `)},
			content: sshd,
			want:    "Port 22\nPermitRootLogin no\nPasswordAuthentication yes\n",
		},
		{
			name:    "already present",
			spec:    Line{Line: "Port 22"},
			content: sshd,
			want:    sshd,
		},
		{
			name:    "append",
			spec:    Line{Line: "UsePAM yes"},
			content: sshd,
			want:    sshd + "UsePAM yes\n",
		},
		{
			name:    "append to a file without final newline",
			spec:    Line{Line: "b"},
			content: "a",
			want:    "a\nb\n",
		},
		{
			name:    "empty file",
			spec:    Line{Line: "a"},
			content: "",
			want:    "a\n",
		},
		{
			name:    "insert after",
			spec:    Line{Line: "ListenAddress 0.0.0.0", Position: Position{After: regexp.MustCompile(`^Port `)}},
			content: sshd,
			want:    "Port 22\nListenAddress 0.0.0.0\n#PermitRootLogin yes\nPasswordAuthentication yes\n",
		},
		{
			name:    "insert before",
			spec:    Line{Line: "# header", Position: Position{Before: regexp.MustCompile(`.*`)}},
			content: sshd,
			want:    "# header\n" + sshd,
		},
		{
			name:    "insert after nothing appends",
			spec:    Line{Line: "x", Position: Position{After: regexp.MustCompile(`^nope`)}},
			content: "a\n",
			want:    "a\nx\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.spec.Ensure(tt.content)
			if got != tt.want {
				t.Fatalf("Ensure = %q, want %q", got, tt.want)
			}
			if !tt.spec.Present(got) {
				t.Error("Present = false after Ensure")
			}
			if again := tt.spec.Ensure(got); again != got {
				t.Errorf("Ensure is not idempotent: %q", again)
			}
		})
	}
}

func TestLinePresent(t *testing.T) {
	spec := Line{Line: "PermitRootLogin no", Match: regexp.MustCompile(`^#?PermitRootLogin `)}
	if spec.Present(sshd) {
		t.Error("the commented line does not count")
	}
	// A later line overriding ours counts as drift.
	if spec.Present("PermitRootLogin no\nPermitRootLogin yes\n") {
		t.Error("the last matching line must be ours")
	}
}

func TestLineRemove(t *testing.T) {
	got := Line{Line: "x"}.Remove("a\nx\nb\nx\n")
	if got != "a\nb\n" {
		t.Errorf("Remove = %q", got)
	}
	got = RemoveLines("a = 1\nb = 2\n", regexp.MustCompile(`^\s*a\s*=`).MatchString)
	if got != "b = 2\n" {
		t.Errorf("RemoveLines = %q", got)
	}
}

func TestBlock(t *testing.T) {
	b := Block{Begin: "# BEGIN app", End: "# END app", Content: "a\nb\n"}
	content := "first\nlast\n"

	got := b.Ensure(content)
	if want := "first\nlast\n# BEGIN app\na\nb\n# END app\n"; got != want {
		t.Fatalf("Ensure = %q", got)
	}
	inner, ok := b.Find(got)
	if !ok || inner != "a\nb\n" {
		t.Fatalf("Find = %q, %v", inner, ok)
	}

	b.Content = "c"
	got = b.Ensure(got)
	if want := "first\nlast\n# BEGIN app\nc\n# END app\n"; got != want {
		t.Fatalf("update = %q", got)
	}
	if again := b.Ensure(got); again != got {
		t.Errorf("Ensure is not idempotent: %q", again)
	}

	if got = b.Remove(got); got != content {
		t.Fatalf("Remove = %q", got)
	}

	b.Position = Position{After: regexp.MustCompile(`^first$`)}
	if got := b.Ensure(content); got != "first\n# BEGIN app\nc\n# END app\nlast\n" {
		t.Fatalf("insert after = %q", got)
	}

	b.Content = ""
	if got := b.Ensure(content); got != "first\n# BEGIN app\n# END app\nlast\n" {
		t.Fatalf("empty block = %q", got)
	}
	if inner, ok := b.Find(b.Ensure(content)); !ok || inner != "" {
		t.Fatalf("Find empty = %q, %v", inner, ok)
	}

	if _, ok := b.Find("# BEGIN app\nno end\n"); ok {
		t.Error("a block without its end marker is not present")
	}
}

func TestCronJobs(t *testing.T) {
	crontab := "MAILTO=admin\n0 1 * * * /usr/bin/other\n"
	job := CronJob{Name: "backup", Schedule: "30 2 * * *", Command: "/usr/local/bin/backup  --all > /dev/null 2>&1"}

	got := SetCronJob(crontab, job)
	if want := crontab + "# Terraform: backup\n30 2 * * * /usr/local/bin/backup  --all > /dev/null 2>&1\n"; got != want {
		t.Fatalf("SetCronJob = %q", got)
	}
	found, ok := FindCronJob(got, "backup")
	if !ok || found != job {
		t.Fatalf("FindCronJob = %+v, %v", found, ok)
	}

	job.Schedule, job.Disabled = "@daily", true
	got = SetCronJob(got, job)
	found, ok = FindCronJob(got, "backup")
	if !ok || found != job {
		t.Fatalf("FindCronJob after update = %+v, %v\n%s", found, ok, got)
	}

	if got = RemoveCronJob(got, "backup"); got != crontab {
		t.Fatalf("RemoveCronJob = %q", got)
	}
	if _, ok := FindCronJob(got, "backup"); ok {
		t.Error("job still found")
	}
}

func TestParseSchedule(t *testing.T) {
	for in, want := range map[string]string{"*/5  * * * *": "*/5 * * * *", "@reboot": "@reboot", " 0 0 1 1 0 ": "0 0 1 1 0"} {
		if got, err := ParseSchedule(in); err != nil || got != want {
			t.Errorf("ParseSchedule(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "* * * *", "* * * * * *", "@daily 1", "# * * * *"} {
		if _, err := ParseSchedule(in); err == nil {
			t.Errorf("ParseSchedule(%q): expected an error", in)
		}
	}
}
