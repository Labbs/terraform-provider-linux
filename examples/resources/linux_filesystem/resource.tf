# Formats the partition only if it is empty. If it already holds an ext4
# file system, it is used as it is, with its data.
resource "linux_filesystem" "data" {
  device = linux_partition.data.path
  type   = "ext4"
  label  = "data"

  # Grow the file system with its partition, after enlarging the disk.
  grow = true
  triggers = {
    size = linux_partition.data.size_bytes
  }
}

resource "linux_filesystem" "swap" {
  device = linux_partition.swap.path
  type   = "swap"
}
