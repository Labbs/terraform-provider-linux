resource "linux_ssh_authorized_key" "alice" {
  user = linux_user.alice.name
  key  = file("~/.ssh/id_ed25519.pub")
}

# With options.
resource "linux_ssh_authorized_key" "backup" {
  user = "root"
  key  = "from=\"10.0.0.0/8\",no-pty ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKYAXtawxjduXikR6hHbQt+T+A5fnGDqaZFNP3iFfjIC backup@vault"
}
