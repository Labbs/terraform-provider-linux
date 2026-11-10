# Install node_exporter from its release tarball, and run it with systemd.
locals {
  node_exporter_version = "1.12.1"
  node_exporter_sha256  = "b51d8a76aa2a9156a55d501aca6276fae09e262259a5e4e831d2c2222f084e63"
}

resource "linux_download" "node_exporter" {
  url    = "https://github.com/prometheus/node_exporter/releases/download/v${local.node_exporter_version}/node_exporter-${local.node_exporter_version}.linux-amd64.tar.gz"
  sha256 = local.node_exporter_sha256
  path   = "/opt/archives/node_exporter.tar.gz"
}

# The tarball holds node_exporter-1.12.1.linux-amd64/node_exporter: with
# strip_components = 1, the binary lands in /opt/node_exporter/node_exporter.
resource "linux_archive_extract" "node_exporter" {
  source           = linux_download.node_exporter.path
  destination      = "/opt/node_exporter"
  strip_components = 1

  # Extract again when a new version is downloaded.
  triggers = {
    sha256 = linux_download.node_exporter.sha256
  }
}

resource "linux_file" "node_exporter_unit" {
  path    = "/etc/systemd/system/node_exporter.service"
  content = <<-EOT
    [Unit]
    Description=Prometheus node exporter
    After=network-online.target

    [Service]
    User=nobody
    ExecStart=/opt/node_exporter/node_exporter
    Restart=on-failure

    [Install]
    WantedBy=multi-user.target
  EOT
}

resource "linux_systemd_unit" "node_exporter" {
  name          = "node_exporter.service"
  enabled       = true
  active        = true
  daemon_reload = true

  # Restart on upgrades and unit changes.
  triggers = {
    binary = linux_download.node_exporter.sha256
    unit   = linux_file.node_exporter_unit.sha256
  }

  depends_on = [linux_archive_extract.node_exporter]
}
