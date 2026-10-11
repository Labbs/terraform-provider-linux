resource "linux_file" "nginx_site" {
  path  = "/etc/nginx/conf.d/app.conf"
  mode  = "0644"
  owner = "root"
  group = "root"

  content = templatefile("${path.module}/app.conf.tftpl", {
    server_name = "app.example.com"
  })
}

# Binary content.
resource "linux_file" "logo" {
  path           = "/var/www/app/logo.png"
  content_base64 = filebase64("${path.module}/logo.png")
}
