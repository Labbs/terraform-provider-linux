resource "linux_symlink" "current_release" {
  path   = "/opt/app/current"
  target = "/opt/app/releases/v1.4.2"
}
