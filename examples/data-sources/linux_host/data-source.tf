data "linux_host" "this" {}

# Install the right package for the distribution.
resource "linux_package" "web_server" {
  name = data.linux_host.this.os_id == "alpine" ? "nginx" : "nginx-light"
}

output "os" {
  value = "${data.linux_host.this.os_pretty_name} (${data.linux_host.this.architecture}, ${data.linux_host.this.cpu_count} CPUs)"
}
