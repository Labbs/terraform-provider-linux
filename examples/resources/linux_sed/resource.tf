resource "linux_sed" "sshd" {
  path     = "/etc/ssh/sshd_config"
  extended = true

  # Idempotent expressions: running them again leaves the file unchanged.
  expressions = [
    "s/^#?PermitRootLogin .*/PermitRootLogin no/",
    "s/^#?PasswordAuthentication .*/PasswordAuthentication no/",
  ]
}
