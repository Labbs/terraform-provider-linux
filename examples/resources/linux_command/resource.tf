resource "linux_command" "swap" {
  create = <<-EOT
    fallocate -l "$SIZE" /swapfile
    chmod 600 /swapfile
    mkswap /swapfile
    swapon /swapfile
  EOT
  read   = "swapon --show=NAME,SIZE --noheadings | grep /swapfile"
  delete = "swapoff /swapfile && rm -f /swapfile"

  environment = {
    SIZE = "2G"
  }
}
