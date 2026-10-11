data "linux_stat" "docker_socket" {
  path = "/var/run/docker.sock"
}

data "linux_stat" "data_dir" {
  path   = "/srv/data"
  follow = true
}

output "docker_installed" {
  value = data.linux_stat.docker_socket.exists
}

output "data_dir_is_directory" {
  value = data.linux_stat.data_dir.type == "directory"
}
