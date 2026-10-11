resource "linux_file" "app_unit" {
  path    = "/etc/systemd/system/app.service"
  content = <<-EOT
    [Unit]
    Description=My application

    [Service]
    ExecStart=/opt/app/bin/app
    Restart=on-failure

    [Install]
    WantedBy=multi-user.target
  EOT
}

resource "linux_systemd_unit" "app" {
  name          = "app.service"
  enabled       = true
  active        = true
  daemon_reload = true

  # Restart the service when its unit file changes.
  triggers = {
    unit = linux_file.app_unit.sha256
  }
}

# Reload nginx when its configuration changes.
resource "linux_systemd_unit" "nginx" {
  name           = "nginx.service"
  enabled        = true
  active         = true
  trigger_action = "reload"

  triggers = {
    config = linux_file.nginx_site.sha256
  }
}
