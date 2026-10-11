data "linux_package" "docker" {
  name = "docker-ce"
}

output "docker_version" {
  value = data.linux_package.docker.installed ? data.linux_package.docker.version : "not installed"
}
