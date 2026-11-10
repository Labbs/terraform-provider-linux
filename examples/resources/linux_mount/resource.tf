# Mount a data disk at boot, by UUID: device names may change.
resource "linux_mount" "data" {
  path    = "/data"
  device  = "UUID=${linux_filesystem.data.uuid}"
  fstype  = "ext4"
  options = "defaults,noatime,nofail"
  pass    = 2
}

# An NFS share.
resource "linux_mount" "shared" {
  path    = "/mnt/shared"
  device  = "nfs.example.com:/exports/shared"
  fstype  = "nfs"
  options = "defaults,_netdev"
}

# A swap area.
resource "linux_mount" "swap" {
  device = "UUID=${linux_filesystem.swap.uuid}"
  fstype = "swap"
}
