package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildAppPreflightProvisionReq(t *testing.T) {
	ctx := context.Background()
	envMap, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"ZED": "1",
		"API": "2",
	})
	if diags.HasError() {
		t.Fatalf("build env map: %v", diags)
	}
	sshKeys, diags := types.ListValueFrom(ctx, types.StringType, []string{"ssh-ed25519 AAAA..."})
	if diags.HasError() {
		t.Fatalf("build ssh keys: %v", diags)
	}

	req, reqDiags := buildAppPreflightProvisionReq(ctx, appPreflightDataSourceModel{
		Name:              types.StringValue("demo"),
		Size:              types.StringValue("tdx.small"),
		Region:            types.StringValue("US-WEST-1"),
		Image:             types.StringValue("dstack-dev-0.5.9"),
		KMS:               types.StringValue("phala"),
		NodeID:            types.Int64Value(42),
		DockerCompose:     types.StringValue("services:\n  app:\n"),
		PreLaunchScript:   types.StringValue("#!/bin/sh\necho ready\n"),
		PublicLogs:        types.BoolValue(true),
		PublicSysinfo:     types.BoolValue(false),
		PublicTCBInfo:     types.BoolValue(true),
		GatewayEnabled:    types.BoolValue(true),
		SecureTime:        types.BoolValue(false),
		StorageFS:         types.StringValue("zfs"),
		DiskSize:          types.Int64Value(20),
		Env:               envMap,
		SSHAuthorizedKeys: sshKeys,
		Listed:            types.BoolValue(false),
	})
	if reqDiags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", reqDiags)
	}

	if req.Name != "demo" || req.InstanceType != "tdx.small" {
		t.Fatalf("unexpected provision request: %#v", req)
	}
	if req.Region == nil || *req.Region != "US-WEST-1" {
		t.Fatalf("missing region: %#v", req.Region)
	}
	if req.Image == nil || *req.Image != "dstack-dev-0.5.9" {
		t.Fatalf("missing image: %#v", req.Image)
	}
	if req.TeepodID == nil || *req.TeepodID != 42 {
		t.Fatalf("missing teepod_id: %#v", req.TeepodID)
	}
	if req.DiskSize == nil || *req.DiskSize != 20 {
		t.Fatalf("missing disk_size: %#v", req.DiskSize)
	}
	if req.Listed == nil || *req.Listed {
		t.Fatalf("unexpected listed: %#v", req.Listed)
	}
	if req.KMSType == nil || *req.KMSType != "PHALA" {
		t.Fatalf("unexpected kms_type: %#v", req.KMSType)
	}
	if len(req.SSHAuthorizedKeys) != 1 || req.SSHAuthorizedKeys[0] != "ssh-ed25519 AAAA..." {
		t.Fatalf("missing ssh_authorized_keys: %#v", req.SSHAuthorizedKeys)
	}

	if req.ComposeFile == nil {
		t.Fatalf("compose_file missing")
	}
	cf := req.ComposeFile
	if cf.Name != "demo" || cf.DockerComposeFile != "services:\n  app:\n" {
		t.Fatalf("unexpected compose_file identity: %#v", cf)
	}
	if cf.PreLaunchScript == nil || *cf.PreLaunchScript != "#!/bin/sh\necho ready\n" {
		t.Fatalf("unexpected pre_launch_script: %#v", cf.PreLaunchScript)
	}
	if cf.PublicLogs == nil || !*cf.PublicLogs {
		t.Fatalf("unexpected public_logs: %#v", cf.PublicLogs)
	}
	if cf.PublicSysinfo == nil || *cf.PublicSysinfo {
		t.Fatalf("unexpected public_sysinfo: %#v", cf.PublicSysinfo)
	}
	if cf.PublicTcbinfo == nil || !*cf.PublicTcbinfo {
		t.Fatalf("unexpected public_tcbinfo: %#v", cf.PublicTcbinfo)
	}
	if cf.GatewayEnabled == nil || !*cf.GatewayEnabled {
		t.Fatalf("unexpected gateway_enabled: %#v", cf.GatewayEnabled)
	}
	if cf.SecureTime == nil || *cf.SecureTime {
		t.Fatalf("unexpected secure_time: %#v", cf.SecureTime)
	}
	if cf.StorageFS == nil || *cf.StorageFS != "zfs" {
		t.Fatalf("unexpected storage_fs: %#v", cf.StorageFS)
	}
	if len(cf.AllowedEnvs) != 2 || cf.AllowedEnvs[0] != "API" || cf.AllowedEnvs[1] != "ZED" {
		t.Fatalf("unexpected allowed_envs: %#v", cf.AllowedEnvs)
	}
}

func TestBuildAppPreflightProvisionReqDefaults(t *testing.T) {
	req, diags := buildAppPreflightProvisionReq(context.Background(), appPreflightDataSourceModel{
		Name:          types.StringValue("demo"),
		Size:          types.StringValue("tdx.small"),
		DockerCompose: types.StringValue("services:\n  app:\n"),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if req.KMSType == nil || *req.KMSType != "PHALA" {
		t.Fatalf("default kms_type = %#v", req.KMSType)
	}
	if req.Listed == nil || *req.Listed {
		t.Fatalf("default listed = %#v", req.Listed)
	}
}
