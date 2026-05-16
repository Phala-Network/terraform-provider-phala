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

	if req["name"] != "demo" || req["instance_type"] != "tdx.small" {
		t.Fatalf("unexpected provision request: %#v", req)
	}
	if req["region"] != "US-WEST-1" || req["image"] != "dstack-dev-0.5.9" {
		t.Fatalf("missing placement fields: %#v", req)
	}
	if req["teepod_id"] != int64(42) || req["disk_size"] != int64(20) {
		t.Fatalf("missing numeric fields: %#v", req)
	}
	if req["listed"] != false || req["kms"] != "PHALA" {
		t.Fatalf("unexpected listed/kms: %#v", req)
	}
	if req["user_config"] == nil {
		t.Fatalf("expected user_config in request: %#v", req)
	}

	composeFile, ok := req["compose_file"].(map[string]any)
	if !ok {
		t.Fatalf("compose_file missing or wrong type: %#v", req["compose_file"])
	}
	if composeFile["name"] != "demo" || composeFile["docker_compose_file"] != "services:\n  app:\n" {
		t.Fatalf("unexpected compose_file identity: %#v", composeFile)
	}
	if composeFile["pre_launch_script"] != "#!/bin/sh\necho ready\n" {
		t.Fatalf("unexpected pre_launch_script: %#v", composeFile)
	}
	if composeFile["public_logs"] != true || composeFile["public_sysinfo"] != false || composeFile["public_tcbinfo"] != true {
		t.Fatalf("unexpected visibility flags: %#v", composeFile)
	}
	if composeFile["gateway_enabled"] != true || composeFile["secure_time"] != false || composeFile["storage_fs"] != "zfs" {
		t.Fatalf("unexpected compose settings: %#v", composeFile)
	}
	keys, ok := composeFile["allowed_envs"].([]string)
	if !ok || len(keys) != 2 || keys[0] != "API" || keys[1] != "ZED" {
		t.Fatalf("unexpected allowed_envs: %#v", composeFile["allowed_envs"])
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
	if req["kms"] != "PHALA" {
		t.Fatalf("default kms = %v", req["kms"])
	}
	if req["listed"] != false {
		t.Fatalf("default listed = %v", req["listed"])
	}
}
