# Download a release on the host, checking its digest.
resource "linux_download" "node_exporter" {
  url    = "https://github.com/prometheus/node_exporter/releases/download/v1.12.1/node_exporter-1.12.1.linux-amd64.tar.gz"
  sha256 = "b51d8a76aa2a9156a55d501aca6276fae09e262259a5e4e831d2c2222f084e63"
  path   = "/opt/archives/node_exporter-1.12.1.linux-amd64.tar.gz"
}
