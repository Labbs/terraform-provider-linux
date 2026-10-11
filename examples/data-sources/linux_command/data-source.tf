data "linux_command" "kernel" {
  command = "uname -r"
}

output "kernel" {
  value = trimspace(data.linux_command.kernel.stdout)
}
