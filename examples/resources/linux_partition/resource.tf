# One partition using the whole disk. If the disk already has a partition 1,
# it is kept as it is: its data is never touched.
resource "linux_partition" "data" {
  device = "/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol0abc123"
  number = 1
  name   = "data"
}

# A 4 GiB swap partition followed by a partition filling the rest of the
# disk, which grows when the disk is enlarged.
resource "linux_partition" "swap" {
  device = "/dev/sdc"
  number = 1
  size   = "4G"
  type   = "swap"
}

resource "linux_partition" "rest" {
  device = "/dev/sdc"
  number = 2

  depends_on = [linux_partition.swap]
}
