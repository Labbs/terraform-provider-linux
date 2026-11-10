package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ datasource.DataSourceWithConfigure = (*blockDevicesDataSource)(nil)

const (
	defaultDiskWaitTimeout = 5 * time.Minute
	diskPollInterval       = 5 * time.Second
)

// NewBlockDevicesDataSource returns the linux_block_devices data source.
func NewBlockDevicesDataSource() datasource.DataSource {
	return &blockDevicesDataSource{}
}

type blockDevicesDataSource struct {
	dataSourceBase
}

type blockDevicesModel struct {
	ID           types.String       `tfsdk:"id"`
	WaitForDisks types.Int64        `tfsdk:"wait_for_disks"`
	WaitTimeout  types.String       `tfsdk:"wait_timeout"`
	RootDisk     types.String       `tfsdk:"root_disk"`
	Devices      []blockDeviceModel `tfsdk:"devices"`
}

type blockDeviceModel struct {
	Path       types.String `tfsdk:"path"`
	KernelName types.String `tfsdk:"kernel_name"`
	Type       types.String `tfsdk:"type"`
	Size       types.Int64  `tfsdk:"size"`
	Model      types.String `tfsdk:"model"`
	Serial     types.String `tfsdk:"serial"`
	WWN        types.String `tfsdk:"wwn"`
	FSType     types.String `tfsdk:"fstype"`
	UUID       types.String `tfsdk:"uuid"`
	Label      types.String `tfsdk:"label"`
	PartUUID   types.String `tfsdk:"partuuid"`
	PartLabel  types.String `tfsdk:"partlabel"`
	Mountpoint types.String `tfsdk:"mountpoint"`
	Parent     types.String `tfsdk:"parent"`
	ReadOnly   types.Bool   `tfsdk:"read_only"`
}

func (d *blockDevicesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_block_devices"
}

func (d *blockDevicesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: description}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the disks, partitions and other block devices of the host, as `lsblk` reports them. Device names such as `/dev/sdb` depend on the order disks were found: select disks by `serial`, `wwn` or size, or as the disk that is not `root_disk`.",
		Attributes: map[string]schema.Attribute{
			"id": str("Always `block_devices`."),
			"wait_for_disks": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Wait until the host has at least this many disks, for instance for a volume attached after the host started.",
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"wait_timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: fmt.Sprintf("Maximum time to wait for `wait_for_disks`, as a Go duration such as `5m`. Defaults to `%s`.", shortDuration(defaultDiskWaitTimeout)),
				Validators:          []validator.String{durationValidator{}},
			},
			"root_disk": str("Disk holding the root file system, such as `/dev/sda`, through partitions, LVM or RAID. Empty when it cannot be told, for instance in a container."),
			"devices": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Block devices, disks followed by what they hold.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"path":        str("Device path, such as `/dev/nvme1n1` or `/dev/mapper/vg-data`."),
						"kernel_name": str("Kernel name, such as `nvme1n1` or `dm-0`."),
						"type":        str("`disk`, `part`, `lvm`, `crypt`, `raid1`, `loop`, `rom`..."),
						"size":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Size in bytes."},
						"model":       str("Disk model."),
						"serial":      str("Disk serial number. On cloud providers, it often holds the volume ID."),
						"wwn":         str("World Wide Name of the disk."),
						"fstype":      str("Type of the file system or signature, such as `ext4`, `swap` or `LVM2_member`."),
						"uuid":        str("UUID of the file system."),
						"label":       str("Label of the file system."),
						"partuuid":    str("UUID of the partition."),
						"partlabel":   str("Name of the GPT partition."),
						"mountpoint":  str("Where the device is mounted."),
						"parent":      str("Path of the device holding this one, such as the disk of a partition."),
						"read_only":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the device is read-only."},
					},
				},
			},
		},
	}
}

func (d *blockDevicesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m blockDevicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := defaultDiskWaitTimeout
	if v := m.WaitTimeout.ValueString(); v != "" {
		timeout, _ = time.ParseDuration(v) // Validated by the schema.
	}
	want := int(m.WaitForDisks.ValueInt64())
	deadline := time.Now().Add(timeout)

	for {
		devices, root, err := d.client.BlockDevices(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Cannot list block devices", err.Error())
			return
		}
		var disks []string
		for _, dev := range devices {
			if dev.Type == "disk" {
				disks = append(disks, dev.Path)
			}
		}
		if len(disks) >= want {
			m.ID = types.StringValue("block_devices")
			m.RootDisk = types.StringValue(root)
			m.Devices = blockDeviceModels(devices)
			resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
			return
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("Disks not found",
				fmt.Sprintf("The host has %d disks after %s, expected at least %d: %s.", len(disks), timeout, want, strings.Join(disks, ", ")))
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Disks not found", ctx.Err().Error())
			return
		case <-time.After(diskPollInterval):
		}
	}
}

func blockDeviceModels(devices []system.BlockDevice) []blockDeviceModel {
	models := make([]blockDeviceModel, 0, len(devices))
	for _, dev := range devices {
		models = append(models, blockDeviceModel{
			Path:       types.StringValue(dev.Path),
			KernelName: types.StringValue(dev.KernelName),
			Type:       types.StringValue(dev.Type),
			Size:       types.Int64Value(dev.Size),
			Model:      types.StringValue(dev.Model),
			Serial:     types.StringValue(dev.Serial),
			WWN:        types.StringValue(dev.WWN),
			FSType:     types.StringValue(dev.FSType),
			UUID:       types.StringValue(dev.UUID),
			Label:      types.StringValue(dev.Label),
			PartUUID:   types.StringValue(dev.PartUUID),
			PartLabel:  types.StringValue(dev.PartLabel),
			Mountpoint: types.StringValue(dev.Mountpoint),
			Parent:     types.StringValue(dev.Parent),
			ReadOnly:   types.BoolValue(dev.ReadOnly),
		})
	}
	return models
}

// shortDuration formats d without zero units, such as 5m rather than 5m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	return strings.TrimSuffix(s, "0m")
}
