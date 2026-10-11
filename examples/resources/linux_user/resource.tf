resource "linux_group" "app" {
  name = "app"
}

resource "linux_user" "app" {
  name    = "app"
  comment = "Application account"
  shell   = "/usr/sbin/nologin"
  home    = "/srv/app"
  system  = true
  groups  = [linux_group.app.name, "adm"]
}

resource "linux_user" "alice" {
  name          = "alice"
  shell         = "/bin/bash"
  groups        = ["sudo"]
  password_hash = var.alice_password_hash # openssl passwd -6
}
