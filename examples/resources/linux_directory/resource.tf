resource "linux_directory" "app" {
  path  = "/opt/app/releases"
  mode  = "0750"
  owner = "app"
  group = "app"

  # Delete the directory and its content on destroy.
  force_destroy = true
}
