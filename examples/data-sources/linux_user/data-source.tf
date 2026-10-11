data "linux_user" "www" {
  name = "www-data"
}

resource "linux_directory" "site" {
  path  = "/var/www/site"
  owner = data.linux_user.www.name
  group = data.linux_user.www.group
}
