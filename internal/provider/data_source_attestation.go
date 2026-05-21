package provider

import (
	"context"
	"encoding/json"
	"strings"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &attestationDataSource{}

type attestationDataSource struct {
	client *phala.Client
}

type attestationDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	CVMID            types.String `tfsdk:"cvm_id"`
	IsOnline         types.Bool   `tfsdk:"is_online"`
	IsPublic         types.Bool   `tfsdk:"is_public"`
	Error            types.String `tfsdk:"error"`
	ComposeFile      types.String `tfsdk:"compose_file"`
	TCBInfoJSON      types.String `tfsdk:"tcb_info_json"`
	CertificatesJSON types.String `tfsdk:"app_certificates_json"`
	RawJSON          types.String `tfsdk:"raw_json"`
}

func NewAttestationDataSource() datasource.DataSource {
	return &attestationDataSource{}
}

func (d *attestationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_attestation"
}

func (d *attestationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches CVM attestation data on demand.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable state ID (same as cvm_id).",
			},
			"cvm_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "CVM/App identifier used for attestation lookup.",
			},
			"is_online": schema.BoolAttribute{
				Computed: true,
			},
			"is_public": schema.BoolAttribute{
				Computed: true,
			},
			"error": schema.StringAttribute{
				Computed: true,
			},
			"compose_file": schema.StringAttribute{
				Computed: true,
			},
			"tcb_info_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Raw `tcb_info` object as JSON.",
			},
			"app_certificates_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Raw `app_certificates` array as JSON.",
			},
			"raw_json": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Full attestation response as JSON.",
			},
		},
	}
}

func (d *attestationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*phala.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			"Expected *phala.Client while configuring attestation data source.",
		)
		return
	}

	d.client = client
}

func (d *attestationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config attestationDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.CVMID.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cvm_id"),
			"Unknown cvm_id",
			"cvm_id must be known at plan time.",
		)
		return
	}

	cvmID := strings.TrimSpace(config.CVMID.ValueString())
	if cvmID == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("cvm_id"),
			"Invalid cvm_id",
			"cvm_id cannot be empty.",
		)
		return
	}

	attestation, err := d.client.GetCVMAttestation(ctx, cvmID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch attestation", err.Error())
		return
	}

	// Marshal typed sub-fields to JSON strings for Terraform state.
	tcbInfoJSON, err := marshalNullableToJSON(attestation.TCBInfo)
	if err != nil {
		resp.Diagnostics.AddError("Failed to encode tcb_info", err.Error())
		return
	}

	certsJSON, err := marshalNullableToJSON(attestation.AppCertificates)
	if err != nil {
		resp.Diagnostics.AddError("Failed to encode app_certificates", err.Error())
		return
	}

	rawJSON, err := json.Marshal(attestation)
	if err != nil {
		resp.Diagnostics.AddError("Failed to encode attestation response", err.Error())
		return
	}

	state := attestationDataSourceModel{
		ID:               types.StringValue(cvmID),
		CVMID:            types.StringValue(cvmID),
		IsOnline:         types.BoolValue(attestation.IsOnline),
		IsPublic:         types.BoolValue(attestation.IsPublic),
		Error:            nullableStringPtr(attestation.Error),
		ComposeFile:      nullableStringPtr(attestation.ComposeFile),
		TCBInfoJSON:      tcbInfoJSON,
		CertificatesJSON: certsJSON,
		RawJSON:          types.StringValue(string(rawJSON)),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// marshalNullableToJSON marshals v to a JSON string. Returns types.StringNull()
// if v is nil or marshals to JSON null/empty.
func marshalNullableToJSON(v any) (types.String, error) {
	if v == nil {
		return types.StringNull(), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return types.StringNull(), err
	}
	s := string(b)
	if s == "null" || s == "" || s == "[]" {
		return types.StringNull(), nil
	}
	return types.StringValue(s), nil
}

func nullableJSON(raw json.RawMessage) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	return types.StringValue(string(raw))
}
