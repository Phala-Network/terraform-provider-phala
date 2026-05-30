package provider

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &regionsDataSource{}

type regionsDataSource struct {
	client *phala.Client
}

type regionsDataSourceModel struct {
	Regions []regionDataSourceRow `tfsdk:"regions"`
}

type regionDataSourceRow struct {
	Slug      types.String `tfsdk:"slug"`
	Name      types.String `tfsdk:"name"`
	Available types.Bool   `tfsdk:"available"`
}

func NewRegionsDataSource() datasource.DataSource {
	return &regionsDataSource{}
}

func (d *regionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_regions"
}

func (d *regionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Phala Cloud regions, similar to DigitalOcean regions.",
		Attributes: map[string]schema.Attribute{
			"regions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Region catalog.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"slug":      schema.StringAttribute{Computed: true},
						"name":      schema.StringAttribute{Computed: true},
						"available": schema.BoolAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *regionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*phala.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			"Expected *phala.Client while configuring regions data source.",
		)
		return
	}

	d.client = client
}

func (d *regionsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	regions := map[string]bool{}
	loaded := false

	// GetAppFilterOptions returns map[string]any — decode the "regions" key manually.
	filterOpts, err := d.client.GetAppFilterOptions(ctx)
	if err == nil {
		encoded, jsonErr := json.Marshal(filterOpts)
		if jsonErr == nil {
			var filters struct {
				Regions []string `json:"regions"`
			}
			if jsonErr = json.Unmarshal(encoded, &filters); jsonErr == nil {
				for _, region := range filters.Regions {
					key := strings.TrimSpace(region)
					if key == "" {
						continue
					}
					regions[key] = false
				}
				loaded = true
			}
		}
		if !loaded {
			resp.Diagnostics.AddWarning(
				"Could not decode filter-options regions",
				"Unexpected response shape from /apps/filter-options.",
			)
		}
	} else {
		resp.Diagnostics.AddWarning(
			"Could not read filter-options regions",
			err.Error(),
		)
	}

	// GetAvailableNodes returns typed *phala.AvailableNodes.
	availability, err := d.client.GetAvailableNodes(ctx)
	if err == nil {
		for _, node := range availability.Nodes {
			if node.RegionIdentifier == nil {
				continue
			}
			region := strings.TrimSpace(*node.RegionIdentifier)
			if region == "" {
				continue
			}
			regions[region] = true
		}
		loaded = true
	} else {
		resp.Diagnostics.AddWarning(
			"Could not read node region availability",
			err.Error(),
		)
	}

	if !loaded {
		resp.Diagnostics.AddError(
			"Failed to list regions",
			"No region endpoint returned data.",
		)
		return
	}

	names := make([]string, 0, len(regions))
	for region := range regions {
		names = append(names, region)
	}
	sort.Strings(names)

	rows := make([]regionDataSourceRow, 0, len(names))
	for _, region := range names {
		rows = append(rows, regionDataSourceRow{
			Slug:      types.StringValue(region),
			Name:      types.StringValue(region),
			Available: types.BoolValue(regions[region]),
		})
	}

	state := regionsDataSourceModel{Regions: rows}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
