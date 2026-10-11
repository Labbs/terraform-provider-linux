resource "linux_sysctl" "ip_forward" {
  name  = "net.ipv4.ip_forward"
  value = "1"
}

resource "linux_sysctl" "port_range" {
  name  = "net.ipv4.ip_local_port_range"
  value = "1024 65000"
  file  = "/etc/sysctl.d/90-network.conf"
}
