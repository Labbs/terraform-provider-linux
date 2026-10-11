resource "linux_file_block" "deploy_match" {
  path    = "/etc/ssh/sshd_config"
  marker  = "# {mark} deploy user"
  content = <<-EOT
    Match User deploy
        PasswordAuthentication no
        X11Forwarding no
  EOT
}
