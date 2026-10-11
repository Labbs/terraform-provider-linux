package textedit

import (
	"fmt"
	"strings"
)

// cronMarker precedes each job managed by the provider, the way Ansible
// tags its jobs with "#Ansible: <name>".
const cronMarker = "# Terraform: "

// CronJob is a crontab entry.
type CronJob struct {
	// Name identifies the job in the crontab.
	Name string
	// Schedule holds the five time fields, or a nickname such as @daily.
	Schedule string
	Command  string
	// Disabled jobs are kept commented out.
	Disabled bool
}

// ParseSchedule checks a schedule and returns it with single spaces.
func ParseSchedule(schedule string) (string, error) {
	fields := strings.Fields(schedule)
	switch {
	case len(fields) == 1 && strings.HasPrefix(fields[0], "@"):
	case len(fields) == 5:
		for _, f := range fields {
			if strings.HasPrefix(f, "@") || strings.HasPrefix(f, "#") {
				return "", fmt.Errorf("invalid schedule %q", schedule)
			}
		}
	default:
		return "", fmt.Errorf("invalid schedule %q: expected five fields, such as \"*/5 * * * *\", or a nickname such as \"@daily\"", schedule)
	}
	return strings.Join(fields, " "), nil
}

// find returns the indexes of the marker and job lines of name, or -1.
func findCronJob(l lines, name string) (int, int) {
	marker := l.index(cronMarker + name)
	if marker < 0 || marker+1 >= len(l.items) {
		return -1, -1
	}
	return marker, marker + 1
}

// FindCronJob returns the job called name in crontab, and whether it is
// there.
func FindCronJob(crontab, name string) (CronJob, bool) {
	l := split(crontab)
	_, i := findCronJob(l, name)
	if i < 0 {
		return CronJob{}, false
	}
	job := CronJob{Name: name}
	line := l.items[i]
	if strings.HasPrefix(line, "#") {
		job.Disabled = true
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
	}
	n := 5
	if strings.HasPrefix(line, "@") {
		n = 1
	}
	fields := strings.Fields(line)
	if len(fields) <= n {
		return CronJob{}, false
	}
	job.Schedule = strings.Join(fields[:n], " ")
	// The command keeps its own spacing: drop the schedule fields only.
	rest := line
	for range n {
		rest = strings.TrimLeft(rest, " \t")
		rest = rest[strings.IndexAny(rest, " \t"):]
	}
	job.Command = strings.TrimLeft(rest, " \t")
	return job, true
}

func (j CronJob) line() string {
	line := j.Schedule + " " + j.Command
	if j.Disabled {
		line = "#" + line
	}
	return line
}

// SetCronJob returns crontab with the job added or replaced.
func SetCronJob(crontab string, job CronJob) string {
	l := split(crontab)
	if _, i := findCronJob(l, job.Name); i >= 0 {
		l.items[i] = job.line()
		return l.String()
	}
	l.insert(len(l.items), cronMarker+job.Name, job.line())
	return l.String()
}

// RemoveCronJob returns crontab without the job called name.
func RemoveCronJob(crontab, name string) string {
	l := split(crontab)
	marker, i := findCronJob(l, name)
	if marker < 0 {
		return crontab
	}
	l.items = append(l.items[:marker], l.items[i+1:]...)
	return l.String()
}
