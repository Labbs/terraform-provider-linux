resource "linux_cron_job" "backup" {
  name     = "nightly backup"
  user     = "root"
  schedule = "30 2 * * *"
  command  = "/usr/local/bin/backup > /var/log/backup.log 2>&1"
}

resource "linux_cron_job" "cleanup" {
  name     = "tmp cleanup"
  schedule = "@daily"
  command  = "find /tmp -type f -mtime +7 -delete"
}
