package system

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseBlockDevices(t *testing.T) {
	out := `NAME="/dev/sda" KNAME="/dev/sda" TYPE="disk" SIZE="21474836480" MODEL="QEMU HARDDISK   " SERIAL="vol-0abc" WWN="" FSTYPE="" UUID="" LABEL="" PARTUUID="" PARTLABEL="" MOUNTPOINT="" PKNAME="" RO="0"
NAME="/dev/sda1" KNAME="/dev/sda1" TYPE="part" SIZE="21473787904" MODEL="" SERIAL="" WWN="" FSTYPE="ext4" UUID="1b2c" LABEL="my\x20data" PARTUUID="aa-bb" PARTLABEL="data" MOUNTPOINT="/data" PKNAME="/dev/sda" RO="0"
NAME="/dev/sr0" KNAME="/dev/sr0" TYPE="rom" SIZE="1024" MODEL="" SERIAL="" WWN="" FSTYPE="" UUID="" LABEL="" PARTUUID="" PARTLABEL="" MOUNTPOINT="" PKNAME="" RO="1"
`
	devices := parseBlockDevices(out)
	if len(devices) != 3 {
		t.Fatalf("got %d devices", len(devices))
	}
	want := BlockDevice{
		Path: "/dev/sda1", KernelName: "sda1", Type: "part", Size: 21473787904, FSType: "ext4", UUID: "1b2c",
		Label: "my data", PartUUID: "aa-bb", PartLabel: "data", Mountpoint: "/data", Parent: "/dev/sda",
	}
	if devices[1] != want {
		t.Errorf("got %+v\nwant %+v", devices[1], want)
	}
	if devices[0].Model != "QEMU HARDDISK" || devices[0].Serial != "vol-0abc" || !devices[2].ReadOnly {
		t.Errorf("unexpected %+v %+v", devices[0], devices[2])
	}
}

const sfdiskJSON = `{
   "partitiontable": {
      "label": "gpt",
      "id": "8F1F2B3C-0000-4000-8000-000000000000",
      "device": "/dev/nvme1n1",
      "unit": "sectors",
      "firstlba": 2048,
      "lastlba": 41943006,
      "sectorsize": 512,
      "partitions": [
         {"node": "/dev/nvme1n1p1", "start": 2048, "size": 2097152, "type": "0657FD6D-A4AB-43C4-84E5-0933C84B4F4F", "uuid": "11111111-0000-4000-8000-000000000000", "name": "swap"},
         {"node": "/dev/nvme1n1p2", "start": 2099200, "size": 18872320, "type": "0FC63DAF-8483-4772-8E79-3D69D8477DE4", "uuid": "22222222-0000-4000-8000-000000000000"}
      ]
   }
}
`

func TestParseDeviceInfo(t *testing.T) {
	// The disk was enlarged from 10 GiB to 20 GiB.
	info, err := parseDeviceInfo("path=/dev/nvme1n1\nsize=21474836480\nsectorsize=512\n---blkid---\nPTUUID=8f1f\nPTTYPE=gpt\n---sfdisk---\n" + sfdiskJSON)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != "/dev/nvme1n1" || info.SectorSize != 512 || info.Signature() != "partition table" {
		t.Fatalf("unexpected %+v", info)
	}
	if info.Table == nil || info.Table.Label != "gpt" || len(info.Table.Partitions) != 2 {
		t.Fatalf("unexpected table %+v", info.Table)
	}
	p1, p2 := info.Table.Partition(1), info.Table.Partition(2)
	if p1 == nil || p1.Name != "swap" || p2 == nil || p2.Number() != 2 || info.Table.Partition(3) != nil {
		t.Fatalf("unexpected partitions %+v", info.Table.Partitions)
	}
	// The first partition is followed by the second one.
	if end := info.Table.MaxEnd(p1, info.Size/512); end != 2099199 {
		t.Errorf("max end of partition 1 = %d", end)
	}
	if PartitionGrowable(info, p1) {
		t.Error("partition 1 cannot grow")
	}
	// The last one can use the end of the disk, minus the backup GPT.
	if end := info.Table.MaxEnd(p2, info.Size/512); end != 41943040-34 {
		t.Errorf("max end of partition 2 = %d", end)
	}
	if !PartitionGrowable(info, p2) {
		t.Error("partition 2 can grow")
	}

	empty, err := parseDeviceInfo("path=/dev/sdb\nsize=1073741824\nsectorsize=4096\n---blkid---\n---sfdisk---\n")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Signature() != "" || empty.Table != nil {
		t.Errorf("unexpected %+v", empty)
	}

	formatted, err := parseDeviceInfo("path=/dev/sdc\nsize=1073741824\nsectorsize=512\n---blkid---\nUUID=abcd\nLABEL=data\\x20disk\nTYPE=ext4\n---sfdisk---\n")
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Signature() != "ext4 signature" || formatted.Probe["LABEL"] != "data disk" {
		t.Errorf("unexpected %+v", formatted)
	}

	if _, err := parseDeviceInfo("garbage"); err == nil {
		t.Error("expected an error")
	}
}

func TestPartitionNumber(t *testing.T) {
	for node, want := range map[string]int{"/dev/sda1": 1, "/dev/nvme0n1p12": 12, "/dev/loop3p2": 2, "/dev/mapper/x": 0} {
		p := Partition{Node: node}
		if got := p.Number(); got != want {
			t.Errorf("%s: got %d, want %d", node, got, want)
		}
	}
}

func TestParseSize(t *testing.T) {
	for s, want := range map[string]int64{"512M": 512 << 20, "20G": 20 << 30, "1T": 1 << 40, "64K": 64 << 10} {
		got, err := ParseSize(s)
		if err != nil || got != want {
			t.Errorf("ParseSize(%s) = %d, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "10", "10GB", "1.5G", "0G", "-1G", "100%"} {
		if _, err := ParseSize(s); err == nil {
			t.Errorf("ParseSize(%q): expected an error", s)
		}
	}
}

func TestPartitionTypeGUID(t *testing.T) {
	if got := PartitionTypeGUID("linux"); got != "0FC63DAF-8483-4772-8E79-3D69D8477DE4" {
		t.Errorf("linux = %s", got)
	}
	if got := PartitionTypeGUID("e6d6d379-f507-44c2-a23c-238f2a3df928"); got != PartitionTypes["lvm"] {
		t.Errorf("guid = %s", got)
	}
}

func TestMkfsCommand(t *testing.T) {
	tests := []struct {
		fsType, label string
		options       []string
		overwrite     bool
		want          string
	}{
		{"ext4", "my data", nil, false, `mkfs.ext4 -F -q -L 'my data' -- "$d"`},
		{"xfs", "", []string{"-m", "reflink=1"}, true, `mkfs.xfs -q -f -m 'reflink=1' -- "$d"`},
		{"vfat", "EFI", nil, false, `mkfs.vfat -n EFI -- "$d"`},
		{"swap", "", nil, false, `mkswap -- "$d"`},
	}
	for _, tt := range tests {
		if got := mkfsCommand(tt.fsType, tt.label, tt.options, tt.overwrite); got != tt.want {
			t.Errorf("mkfsCommand(%s) = %s, want %s", tt.fsType, got, tt.want)
		}
	}
}

func TestFstab(t *testing.T) {
	content := `# /etc/fstab
UUID=root / ext4 errors=remount-ro 0 1
/dev/sdb1	/data	ext4	defaults	0	2
UUID=old /data xfs defaults 0 0
/swapfile none swap sw 0 0
//server/share /mnt/my\040share cifs credentials=/root/creds 0 0
`
	e, ok := FindFstabEntry(content, FstabEntry{Path: "/data"})
	if !ok || e.Device != "/dev/sdb1" || e.Pass != 2 || e.Options != "defaults" {
		t.Fatalf("found %+v, %v", e, ok)
	}
	if e, ok := FindFstabEntry(content, FstabEntry{Path: "/mnt/my share"}); !ok || e.FSType != "cifs" {
		t.Errorf("escaped mount point: %+v, %v", e, ok)
	}
	if e, ok := FindFstabEntry(content, FstabEntry{Device: "/swapfile", FSType: "swap"}); !ok || e.Options != "sw" {
		t.Errorf("swap: %+v, %v", e, ok)
	}
	if _, ok := FindFstabEntry(content, FstabEntry{Path: "/none"}); ok {
		t.Error("unexpected entry")
	}

	// Replacing keeps the position and removes duplicates.
	entry := FstabEntry{Device: "UUID=new", Path: "/data", FSType: "ext4", Options: "defaults,noatime,nofail", Pass: 2}
	got := SetFstabEntry(content, entry, false)
	want := `# /etc/fstab
UUID=root / ext4 errors=remount-ro 0 1
UUID=new /data ext4 defaults,noatime,nofail 0 2
/swapfile none swap sw 0 0
//server/share /mnt/my\040share cifs credentials=/root/creds 0 0
`
	if got != want {
		t.Errorf("replace:\n%s\nwant:\n%s", got, want)
	}

	// Appending, with escaping.
	got = SetFstabEntry("UUID=root / ext4 defaults 0 1", FstabEntry{Device: "tmpfs", Path: "/mnt/a b", FSType: "tmpfs"}, false)
	if want := "UUID=root / ext4 defaults 0 1\ntmpfs /mnt/a\\040b tmpfs defaults 0 0\n"; got != want {
		t.Errorf("append: %q", got)
	}
	if e, ok := FindFstabEntry(got, FstabEntry{Path: "/mnt/a b"}); !ok || e.Device != "tmpfs" {
		t.Errorf("round trip: %+v, %v", e, ok)
	}
	if got := SetFstabEntry("", FstabEntry{Device: "/swap.img", FSType: "swap"}, false); got != "/swap.img none swap defaults 0 0\n" {
		t.Errorf("swap entry: %q", got)
	}

	// Removing.
	got = SetFstabEntry(content, FstabEntry{Path: "/data"}, true)
	if strings.Contains(got, "/data") || !strings.Contains(got, "/swapfile") || !strings.HasPrefix(got, "# /etc/fstab\n") {
		t.Errorf("remove:\n%s", got)
	}
	if got := SetFstabEntry("", FstabEntry{Path: "/data"}, true); got != "" {
		t.Errorf("remove from empty: %q", got)
	}
}

func TestParseMounts(t *testing.T) {
	mounts := parseMounts(`/dev/sda1 / ext4 rw,relatime 0 0
tmpfs /mnt/a\040b tmpfs rw 0 0
/dev/sdb1 /data ext4 rw 0 0
/dev/sdc1 /data xfs rw,noatime 0 0
`)
	want := map[string]MountInfo{
		"/":        {Device: "/dev/sda1", FSType: "ext4", Options: "rw,relatime"},
		"/mnt/a b": {Device: "tmpfs", FSType: "tmpfs", Options: "rw"},
		// The last mount hides the previous ones.
		"/data": {Device: "/dev/sdc1", FSType: "xfs", Options: "rw,noatime"},
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("got %+v", mounts)
	}
}
