action "linux_systemctl" "reload_nginx" {
  config {
    command = "reload"
    unit    = "nginx.service"
  }
}

resource "linux_file" "nginx_site" {
  path    = "/etc/nginx/conf.d/app.conf"
  content = file("${path.module}/app.conf")

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.linux_systemctl.reload_nginx]
    }
  }
}
