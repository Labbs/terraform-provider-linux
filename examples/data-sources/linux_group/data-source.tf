data "linux_group" "sudo" {
  name = "sudo"
}

output "sudoers" {
  value = data.linux_group.sudo.members
}
