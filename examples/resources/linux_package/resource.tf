resource "linux_package" "nginx" {
  name = "nginx"
}

# Pin a version, as the package manager shows it.
resource "linux_package" "jq" {
  name    = "jq"
  version = "1.6-2.1+deb12u1"
}

# Several packages.
resource "linux_package" "tools" {
  for_each = toset(["curl", "git", "htop"])
  name     = each.key
}
