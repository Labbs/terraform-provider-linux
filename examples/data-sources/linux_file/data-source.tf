data "linux_file" "os_release" {
  path = "/etc/os-release"
}

output "os_release" {
  value = data.linux_file.os_release.content
}
