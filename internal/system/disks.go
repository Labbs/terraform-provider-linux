package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// deviceWait bounds how long operations wait for a device that is not there
// yet, such as a volume being attached.
const deviceWait = 2 * time.Minute

// BlockDevice describes a disk, a partition or another block device, as
// lsblk reports it.
type BlockDevice struct {
	Path       string
	KernelName string
	Type       string
	Size       int64
	Model      string
	Serial     string
	WWN        string
	FSType     string
	UUID       string
	Label      string
	PartUUID   string
	PartLabel  string
	Mountpoint string
	Parent     string
	ReadOnly   bool
}

const lsblkColumns = "NAME,KNAME,TYPE,SIZE,MODEL,SERIAL,WWN,FSTYPE,UUID,LABEL,PARTUUID,PARTLABEL,MOUNTPOINT,PKNAME,RO"

var lsblkPair = regexp.MustCompile(`([A-Z_:-]+)="([^"]*)"`)

// parseLsblkPairs parses the output of lsblk --pairs.
func parseLsblkPairs(out string) []map[string]string {
	var rows []map[string]string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := map[string]string{}
		for _, m := range lsblkPair.FindAllStringSubmatch(line, -1) {
			row[m[1]] = unescapeHex(m[2])
		}
		rows = append(rows, row)
	}
	return rows
}

// unescapeHex decodes the \xHH escapes lsblk and blkid use.
func unescapeHex(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if n, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseBlockDevices(out string) []BlockDevice {
	var devices []BlockDevice
	for _, row := range parseLsblkPairs(out) {
		size, _ := strconv.ParseInt(row["SIZE"], 10, 64)
		devices = append(devices, BlockDevice{
			Path:       row["NAME"],
			KernelName: path.Base(row["KNAME"]),
			Type:       row["TYPE"],
			Size:       size,
			Model:      strings.TrimSpace(row["MODEL"]),
			Serial:     strings.TrimSpace(row["SERIAL"]),
			WWN:        row["WWN"],
			FSType:     row["FSTYPE"],
			UUID:       row["UUID"],
			Label:      row["LABEL"],
			PartUUID:   row["PARTUUID"],
			PartLabel:  row["PARTLABEL"],
			Mountpoint: row["MOUNTPOINT"],
			Parent:     row["PKNAME"],
			ReadOnly:   row["RO"] == "1",
		})
	}
	return devices
}

// BlockDevices lists the block devices of the host, and returns the disk
// holding the root file system, when it can be told.
func (c *Client) BlockDevices(ctx context.Context) ([]BlockDevice, string, error) {
	out, err := c.exec(ctx, script(
		"set -e",
		"lsblk --bytes --pairs --paths --output "+lsblkColumns,
		"echo ---root---",
		// The disk under /, through partitions, LVM or RAID.
		`src=$(findmnt -n -o SOURCE / 2>/dev/null) || src=`,
		`src=${src%%\[*}`,
		`case $src in /dev/*) lsblk --pairs --paths --inverse --output NAME,TYPE -- "$src" 2>/dev/null || true ;; esac`,
	), nil)
	if err != nil {
		return nil, "", fmt.Errorf("listing block devices: %w", err)
	}
	devicesOut, rootOut, _ := strings.Cut(string(out), "---root---\n")
	root := ""
	for _, row := range parseLsblkPairs(rootOut) {
		if row["TYPE"] == "disk" {
			root = row["NAME"]
			break
		}
	}
	return parseBlockDevices(devicesOut), root, nil
}

// DeviceInfo describes a block device, with what it holds.
type DeviceInfo struct {
	// Path is the canonical path of the device, symbolic links resolved.
	Path       string
	Size       int64
	SectorSize int64
	// Probe holds the signature found on the device by blkid: TYPE, UUID
	// and LABEL for a file system, PTTYPE for a partition table.
	Probe map[string]string
	// Table is the partition table, or nil when there is none.
	Table *PartitionTable
}

// Signature describes what the device holds, or returns "" when blkid
// found nothing on it.
func (d *DeviceInfo) Signature() string {
	switch {
	case d.Probe["TYPE"] != "":
		return d.Probe["TYPE"] + " signature"
	case d.Probe["PTTYPE"] != "" || d.Table != nil:
		return "partition table"
	}
	return ""
}

// PartitionTable is a partition table, as sfdisk --json reports it.
type PartitionTable struct {
	Label      string      `json:"label"`
	Device     string      `json:"device"`
	SectorSize int64       `json:"sectorsize"`
	Partitions []Partition `json:"partitions"`
}

// Partition is an entry of a partition table. Start and Size are in
// sectors.
type Partition struct {
	Node  string `json:"node"`
	Start int64  `json:"start"`
	Size  int64  `json:"size"`
	Type  string `json:"type"`
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
}

var trailingDigits = regexp.MustCompile(`[0-9]+$`)

// Number returns the number of the partition, from its device node.
func (p *Partition) Number() int {
	n, _ := strconv.Atoi(trailingDigits.FindString(p.Node))
	return n
}

// Partition returns the partition with the given number, or nil.
func (t *PartitionTable) Partition(number int) *Partition {
	for i := range t.Partitions {
		if t.Partitions[i].Number() == number {
			return &t.Partitions[i]
		}
	}
	return nil
}

// MaxEnd returns the last sector partition p can grow to: the sector
// before the next partition, or the last usable sector of a disk of
// totalSectors sectors.
func (t *PartitionTable) MaxEnd(p *Partition, totalSectors int64) int64 {
	// A GPT ends with a backup header and 32 sectors of entries.
	end := totalSectors - 34
	for _, other := range t.Partitions {
		if other.Start > p.Start && other.Start-1 < end {
			end = other.Start - 1
		}
	}
	return end
}

func parseDeviceInfo(out string) (*DeviceInfo, error) {
	head, rest, _ := strings.Cut(out, "---blkid---\n")
	probeOut, tableOut, _ := strings.Cut(rest, "---sfdisk---\n")
	info := &DeviceInfo{Probe: map[string]string{}}
	for _, line := range strings.Split(head, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "path":
			info.Path = value
		case "size":
			info.Size, _ = strconv.ParseInt(value, 10, 64)
		case "sectorsize":
			info.SectorSize, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	if info.Path == "" || info.Size <= 0 || info.SectorSize <= 0 {
		return nil, fmt.Errorf("unexpected device information %q", head)
	}
	for _, line := range strings.Split(probeOut, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			info.Probe[key] = unescapeHex(value)
		}
	}
	if strings.TrimSpace(tableOut) != "" {
		var parsed struct {
			PartitionTable *PartitionTable `json:"partitiontable"`
		}
		if err := json.Unmarshal([]byte(tableOut), &parsed); err != nil {
			return nil, fmt.Errorf("parsing sfdisk output: %w", err)
		}
		info.Table = parsed.PartitionTable
	}
	return info, nil
}

// DeviceInfo inspects the block device dev, waiting for it to appear.
func (c *Client) DeviceInfo(ctx context.Context, dev string) (*DeviceInfo, error) {
	return c.deviceInfo(ctx, dev, true)
}

// DeviceInfoNow inspects the block device dev, failing with an error
// IsNotExist recognizes when it does not exist.
func (c *Client) DeviceInfoNow(ctx context.Context, dev string) (*DeviceInfo, error) {
	return c.deviceInfo(ctx, dev, false)
}

func (c *Client) deviceInfo(ctx context.Context, dev string, wait bool) (*DeviceInfo, error) {
	var out []byte
	inspect := func() error {
		var err error
		out, err = c.exec(ctx, script(
			"set -e",
			"d=$(readlink -f -- "+q(dev)+")",
			fmt.Sprintf(`[ -b "$d" ] || exit %d`, exitNotFound),
			`echo "path=$d"`,
			`echo "size=$(blockdev --getsize64 "$d")"`,
			`echo "sectorsize=$(blockdev --getss "$d")"`,
			"echo ---blkid---",
			// blkid exits with 2 when it finds nothing.
			`blkid -p -o export -- "$d" || [ $? -eq 2 ]`,
			"echo ---sfdisk---",
			`sfdisk --json -- "$d" 2>/dev/null || true`,
		), nil)
		return err
	}
	var err error
	if wait {
		err = c.waitForDevice(ctx, inspect)
	} else {
		err = inspect()
	}
	if err != nil {
		return nil, wrap(err, "inspect", dev)
	}
	info, err := parseDeviceInfo(string(out))
	return info, wrap(err, "inspect", dev)
}

// waitForDevice runs fn until it does not report a missing device, for at
// most deviceWait.
func (c *Client) waitForDevice(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(deviceWait)
	for {
		err := fn()
		if !IsNotExist(err) || time.Now().After(deadline) {
			if IsNotExist(err) {
				return fmt.Errorf("not a block device, or did not appear within %s: %w", deviceWait, err)
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Partition type GUIDs, by short name.
var PartitionTypes = map[string]string{
	"linux":     "0FC63DAF-8483-4772-8E79-3D69D8477DE4",
	"swap":      "0657FD6D-A4AB-43C4-84E5-0933C84B4F4F",
	"efi":       "C12A7328-F81F-11D2-BA4B-00A0C93EC93B",
	"lvm":       "E6D6D379-F507-44C2-A23C-238F2A3DF928",
	"raid":      "A19D880F-05FC-4D3B-A006-743F0F84911E",
	"bios_boot": "21686148-6449-6E6F-744E-656564454649",
}

// PartitionTypeGUID returns the GUID of a partition type given by its short
// name or its GUID.
func PartitionTypeGUID(t string) string {
	if guid, ok := PartitionTypes[t]; ok {
		return guid
	}
	return strings.ToUpper(t)
}

var sizePattern = regexp.MustCompile(`^([0-9]+)([KMGT])$`)

// ParseSize parses a size such as 512M or 20G, in powers of 1024.
func ParseSize(s string) (int64, error) {
	m := sizePattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a size such as 512M, 20G or 1T", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%q is not a positive size", s)
	}
	shift := map[string]uint{"K": 10, "M": 20, "G": 30, "T": 40}[m[2]]
	if n > (1<<62)>>shift {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return n << shift, nil
}

// PartitionSpec describes a partition to create.
type PartitionSpec struct {
	Number int
	// Size is in bytes, or 0 for all the free space following the start.
	Size int64
	Type string
	Name string
}

// tellKernel makes the kernel pick up the partition table of "$d", even
// when some of its partitions are in use, and waits for udev.
const tellKernel = `blockdev --rereadpt "$d" 2>/dev/null || { partx -a -- "$d" 2>/dev/null; partx -u -- "$d" 2>/dev/null; } || true
if command -v udevadm >/dev/null 2>&1; then udevadm settle || true; fi`

// sfdiskScript writes a partition table change with sfdisk, reading the
// sfdisk script from the standard input.
func (c *Client) sfdisk(ctx context.Context, d, args, input string) error {
	_, err := c.exec(ctx, script(
		"set -e",
		"d="+q(d),
		"sfdisk --quiet --no-reread "+args+` -- "$d"`+" <<'EOF'",
		input,
		"EOF",
		tellKernel,
	), nil)
	return err
}

// EnsurePartition makes sure the partition spec.Number exists on the disk
// dev. A disk without partition table gets a GPT, unless it holds
// something else, such as a file system: it is never overwritten. An
// existing partition is kept as it is, apart from growing it to the
// requested size. It returns the disk and whether the partition was
// created.
func (c *Client) EnsurePartition(ctx context.Context, dev string, spec PartitionSpec) (*DeviceInfo, bool, error) {
	disk, err := c.DeviceInfo(ctx, dev)
	if err != nil {
		return nil, false, err
	}
	defer c.lock("disk:" + disk.Path)()
	// Read it again under the lock.
	if disk, err = c.DeviceInfo(ctx, disk.Path); err != nil {
		return nil, false, err
	}

	if disk.Table == nil {
		if sig := disk.Signature(); sig != "" {
			return nil, false, fmt.Errorf("%s has a %s but no partition table: refusing to partition it, as it may hold data", dev, sig)
		}
		if err := c.sfdisk(ctx, disk.Path, "", "label: gpt"); err != nil {
			return nil, false, wrap(err, "create partition table on", dev)
		}
		if disk, err = c.DeviceInfo(ctx, disk.Path); err != nil {
			return nil, false, err
		}
		if disk.Table == nil {
			return nil, false, fmt.Errorf("create partition table on %s: no table after writing it", dev)
		}
	}
	if disk.Table.Label != "gpt" {
		return nil, false, fmt.Errorf("%s has a %s partition table: only GPT is supported", dev, disk.Table.Label)
	}
	if disk.Table.Partition(spec.Number) != nil {
		return disk, false, nil
	}

	fields := []string{"type=" + PartitionTypeGUID(spec.Type)}
	if spec.Size > 0 {
		fields = append(fields, "size="+strconv.FormatInt(spec.Size/disk.SectorSize, 10))
	}
	if spec.Name != "" {
		fields = append(fields, "name="+strconv.Quote(spec.Name))
	}
	if err := c.sfdisk(ctx, disk.Path, "-N "+strconv.Itoa(spec.Number), strings.Join(fields, ", ")); err != nil {
		return nil, false, wrap(err, fmt.Sprintf("create partition %d on", spec.Number), dev)
	}
	disk, err = c.DeviceInfo(ctx, disk.Path)
	if err != nil {
		return nil, false, err
	}
	p := disk.Table.Partition(spec.Number)
	if p == nil {
		return nil, false, fmt.Errorf("create partition %d on %s: partition missing after writing it", spec.Number, dev)
	}
	return disk, true, c.waitForNode(ctx, p.Node)
}

// waitForNode waits for the device node of a new partition.
func (c *Client) waitForNode(ctx context.Context, node string) error {
	err := c.waitForDevice(ctx, func() error {
		_, err := c.exec(ctx, fmt.Sprintf("[ -b %s ] || exit %d", q(node), exitNotFound), nil)
		return err
	})
	return wrap(err, "wait for", node)
}

// GrowPartition grows partition number of the disk dev to size bytes, or
// as much as possible when size is 0. Partitions are never shrunk.
func (c *Client) GrowPartition(ctx context.Context, dev string, number int, size int64) error {
	disk, err := c.DeviceInfo(ctx, dev)
	if err != nil {
		return err
	}
	defer c.lock("disk:" + disk.Path)()
	// After the disk was enlarged, the backup GPT is not at its end: move it
	// there first. Older sfdisk versions do not know this option.
	_, _ = c.exec(ctx, "sfdisk --quiet --relocate gpt-bak-std -- "+q(disk.Path)+" 2>/dev/null", nil)
	if disk, err = c.DeviceInfo(ctx, disk.Path); err != nil {
		return err
	}
	if disk.Table == nil {
		return fmt.Errorf("grow partition %d of %s: no partition table", number, dev)
	}
	p := disk.Table.Partition(number)
	if p == nil {
		return fmt.Errorf("grow partition %d of %s: no such partition", number, dev)
	}
	sectors := disk.Size / disk.SectorSize
	newSize := disk.Table.MaxEnd(p, sectors) - p.Start + 1
	if size > 0 {
		newSize = size / disk.SectorSize
	}
	switch {
	case newSize < p.Size:
		return fmt.Errorf("partition %d of %s is larger than %d bytes: shrinking partitions is not supported", number, dev, size)
	case newSize == p.Size:
		return nil
	}
	if err := c.sfdisk(ctx, disk.Path, "-N "+strconv.Itoa(number), ", "+strconv.FormatInt(newSize, 10)); err != nil {
		return wrap(err, fmt.Sprintf("grow partition %d of", number), dev)
	}
	return nil
}

// SetPartitionType changes the type of partition number of the disk dev.
func (c *Client) SetPartitionType(ctx context.Context, dev string, number int, partType string) error {
	return c.setPartitionField(ctx, dev, number, "--part-type", PartitionTypeGUID(partType), "type")
}

// SetPartitionName changes the GPT name of partition number of the disk
// dev.
func (c *Client) SetPartitionName(ctx context.Context, dev string, number int, name string) error {
	return c.setPartitionField(ctx, dev, number, "--part-label", name, "name")
}

func (c *Client) setPartitionField(ctx context.Context, dev string, number int, option, value, what string) error {
	disk, err := c.DeviceInfo(ctx, dev)
	if err != nil {
		return err
	}
	defer c.lock("disk:" + disk.Path)()
	_, err = c.exec(ctx, script(
		"set -e",
		"d="+q(disk.Path),
		fmt.Sprintf(`sfdisk --quiet --no-reread %s -- "$d" %d %s`, option, number, q(value)),
		tellKernel,
	), nil)
	return wrap(err, fmt.Sprintf("set %s of partition %d of", what, number), dev)
}

// PartitionGrowable reports whether partition p of disk can grow by at
// least 1 MiB.
func PartitionGrowable(disk *DeviceInfo, p *Partition) bool {
	if disk.Table == nil {
		return false
	}
	end := p.Start + p.Size - 1
	return (disk.Table.MaxEnd(p, disk.Size/disk.SectorSize)-end)*disk.SectorSize >= 1<<20
}

// Filesystem types supported by MakeFilesystem.
var FilesystemTypes = []string{"ext2", "ext3", "ext4", "xfs", "btrfs", "vfat", "swap"}

// GrowableFilesystemTypes can be grown by GrowFilesystem.
var GrowableFilesystemTypes = []string{"ext2", "ext3", "ext4", "xfs", "btrfs"}

// ErrFormatted reports a device that already holds something.
var ErrFormatted = errors.New("the device is not empty")

// FormatResult tells what EnsureFilesystem did.
type FormatResult struct {
	// Formatted is false when an existing file system was kept.
	Formatted bool
	UUID      string
	Label     string
	Size      int64
}

func mkfsCommand(fsType, label string, options []string, overwrite bool) string {
	var cmd []string
	switch fsType {
	case "ext2", "ext3", "ext4":
		// -F: the device was checked, do not ask for a confirmation.
		cmd = []string{"mkfs." + fsType, "-F", "-q"}
		if label != "" {
			cmd = append(cmd, "-L", q(label))
		}
	case "xfs", "btrfs":
		cmd = []string{"mkfs." + fsType, "-q"}
		if overwrite {
			cmd = append(cmd, "-f")
		}
		if label != "" {
			cmd = append(cmd, "-L", q(label))
		}
	case "vfat":
		cmd = []string{"mkfs.vfat"}
		if label != "" {
			cmd = append(cmd, "-n", q(label))
		}
	case "swap":
		cmd = []string{"mkswap"}
		if overwrite {
			cmd = append(cmd, "-f")
		}
		if label != "" {
			cmd = append(cmd, "-L", q(label))
		}
	}
	for _, o := range options {
		cmd = append(cmd, q(o))
	}
	return strings.Join(append(cmd, `-- "$d"`), " ")
}

// EnsureFilesystem makes sure the device dev holds a file system of type
// fsType. A file system of that type already there is kept, with its data:
// only its label is changed when needed. Anything else on the device makes
// it fail with ErrFormatted, unless overwrite is set: it is then erased.
func (c *Client) EnsureFilesystem(ctx context.Context, dev, fsType, label string, options []string, overwrite bool) (*FormatResult, error) {
	info, err := c.DeviceInfo(ctx, dev)
	if err != nil {
		return nil, err
	}
	defer c.lock("fs:" + info.Path)()

	switch existing := info.Probe["TYPE"]; {
	case existing == fsType && !overwrite:
		if label != "" && info.Probe["LABEL"] != label {
			if err := c.SetFilesystemLabel(ctx, info.Path, fsType, label); err != nil {
				return nil, err
			}
		}
		return c.filesystemResult(ctx, dev, false)
	case info.Signature() != "" && !overwrite:
		what := info.Signature()
		if existing != "" {
			what = fmt.Sprintf("%s file system", existing)
			if l := info.Probe["LABEL"]; l != "" {
				what += fmt.Sprintf(" labelled %q", l)
			}
		}
		return nil, fmt.Errorf("%s already holds a %s: %w. Refusing to format it: set type to match it to use it as it is, or set overwrite to erase it", dev, what, ErrFormatted)
	}

	lines := []string{"set -e", "d=" + q(info.Path)}
	if overwrite {
		lines = append(lines, `wipefs -a -q -- "$d"`)
	}
	lines = append(lines,
		mkfsCommand(fsType, label, options, overwrite)+" </dev/null",
		"if command -v udevadm >/dev/null 2>&1; then udevadm settle || true; fi",
	)
	if _, err := c.exec(ctx, script(lines...), nil); err != nil {
		return nil, wrap(err, "format", dev)
	}
	return c.filesystemResult(ctx, dev, true)
}

func (c *Client) filesystemResult(ctx context.Context, dev string, formatted bool) (*FormatResult, error) {
	info, err := c.DeviceInfo(ctx, dev)
	if err != nil {
		return nil, err
	}
	return &FormatResult{Formatted: formatted, UUID: info.Probe["UUID"], Label: info.Probe["LABEL"], Size: info.Size}, nil
}

// SetFilesystemLabel changes the label of the file system on dev.
func (c *Client) SetFilesystemLabel(ctx context.Context, dev, fsType, label string) error {
	var cmd string
	switch fsType {
	case "ext2", "ext3", "ext4":
		cmd = `e2label "$d" "$l"`
	case "xfs":
		cmd = `xfs_admin -L "$l" "$d"`
	case "btrfs":
		cmd = `btrfs filesystem label "$d" "$l"`
	case "vfat":
		cmd = `fatlabel "$d" "$l"`
	case "swap":
		cmd = `swaplabel -L "$l" "$d"`
	default:
		return fmt.Errorf("set label of %s: unsupported file system %q", dev, fsType)
	}
	_, err := c.exec(ctx, script("set -e", "d="+q(dev), "l="+q(label), cmd), nil)
	return wrap(err, "set label of", dev)
}

// GrowFilesystem grows the file system on dev to the size of the device.
// XFS and Btrfs can only grow while mounted.
func (c *Client) GrowFilesystem(ctx context.Context, dev, fsType string) error {
	var cmd string
	switch fsType {
	case "ext2", "ext3", "ext4":
		cmd = `resize2fs "$d"`
	case "xfs", "btrfs":
		grow := `xfs_growfs "$mp"`
		if fsType == "btrfs" {
			grow = `btrfs filesystem resize max "$mp"`
		}
		cmd = script(
			`mp=$(findmnt -n -o TARGET --source "$d" | head -n 1)`,
			`[ -n "$mp" ] || { echo "$d must be mounted to grow its `+fsType+` file system" >&2; exit 1; }`,
			grow,
		)
	default:
		return fmt.Errorf("grow %s: %s file systems cannot be grown", dev, fsType)
	}
	_, err := c.exec(ctx, script("set -e", "d=$(readlink -f -- "+q(dev)+")", cmd), nil)
	return wrap(err, "grow file system on", dev)
}
