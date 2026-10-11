# Replace the last line matching "match", or add the line.
resource "linux_file_line" "root_login" {
  path  = "/etc/ssh/sshd_config"
  line  = "PermitRootLogin no"
  match = "^#?PermitRootLogin "
}

# Insert a line after another one, creating the file if needed.
resource "linux_file_line" "proxy" {
  path         = "/etc/environment"
  line         = "HTTP_PROXY=http://proxy.internal:3128"
  insert_after = "^PATH="
  create       = true
}
