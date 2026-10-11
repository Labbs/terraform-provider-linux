resource "linux_patch" "sshd_root_login" {
  path  = "/etc/ssh/sshd_config"
  patch = <<-EOT
    --- a/etc/ssh/sshd_config
    +++ b/etc/ssh/sshd_config
    @@ -1,3 +1,3 @@
     Include /etc/ssh/sshd_config.d/*.conf
    -#PermitRootLogin prohibit-password
    +PermitRootLogin no
     #StrictModes yes
  EOT
}
