data "linux_systemd_unit" "docker" {
  name = "docker.service"
}

output "docker_running" {
  value = data.linux_systemd_unit.docker.active
}
