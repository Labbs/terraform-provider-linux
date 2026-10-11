data "linux_sed" "root_login" {
  path        = "/etc/ssh/sshd_config"
  expressions = ["s/^#?PermitRootLogin .*/PermitRootLogin no/"]
  extended    = true
}

output "root_login_needs_change" {
  value = data.linux_sed.root_login.pending
}
