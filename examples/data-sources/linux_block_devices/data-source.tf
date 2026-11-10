# A data volume attached after the host started: wait for it, then take
# the only disk that does not hold the root file system.
data "linux_block_devices" "all" {
  wait_for_disks = 2
}

locals {
  data_disk = one([
    for d in data.linux_block_devices.all.devices : d.path
    if d.type == "disk" && d.path != data.linux_block_devices.all.root_disk
  ])
}

# Or select a disk by its serial number, which often holds the volume ID on
# cloud providers.
output "volume" {
  value = [for d in data.linux_block_devices.all.devices : d.path if d.serial == "vol0abc123"]
}
