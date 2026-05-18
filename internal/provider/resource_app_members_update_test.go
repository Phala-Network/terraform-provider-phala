package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func membersListFrom(t *testing.T, vals ...string) types.List {
	t.Helper()
	v, diags := types.ListValueFrom(context.Background(), types.StringType, vals)
	if diags.HasError() {
		t.Fatalf("build members list: %v", diags)
	}
	return v
}

func TestAppHasMembers(t *testing.T) {
	tests := []struct {
		name string
		m    appResourceModel
		want bool
	}{
		{"null", appResourceModel{Members: types.ListNull(types.StringType)}, false},
		{"unknown", appResourceModel{Members: types.ListUnknown(types.StringType)}, false},
		{"empty", appResourceModel{Members: membersListFrom(t)}, false},
		{"non-empty", appResourceModel{Members: membersListFrom(t, "consul-0")}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := appHasMembers(tc.m); got != tc.want {
				t.Fatalf("appHasMembers = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDetectUnsafeMembersUpdate_AllAppLevelMutationsFlagged covers each
// cloud-side mutable field individually. A change in any of them must
// produce a corresponding path in the result so ModifyPlan can refuse
// the plan with an actionable error.
func TestDetectUnsafeMembersUpdate_AllAppLevelMutationsFlagged(t *testing.T) {
	baseline := appResourceModel{
		DockerCompose:      types.StringValue("services:\n  app:\n"),
		PreLaunchScript:    types.StringNull(),
		Env:                types.MapNull(types.StringType),
		EncryptedEnv:       types.StringNull(),
		EnvKeys:            types.ListNull(types.StringType),
		EnvComposeHash:     types.StringNull(),
		EnvTransactionHash: types.StringNull(),
		Image:              types.StringValue("dstack-dev-0.5.7-9b6a5239"),
		Size:               types.StringValue("tdx.small"),
		DiskSize:           types.Int64Value(20),
		PublicLogs:         types.BoolValue(false),
		PublicSysinfo:      types.BoolValue(false),
		PublicTCBInfo:      types.BoolValue(false),
		GatewayEnabled:     types.BoolValue(false),
		SecureTime:         types.BoolValue(false),
	}

	type mutateFn func(m *appResourceModel)
	cases := []struct {
		field   string
		mutator mutateFn
	}{
		{"docker_compose", func(m *appResourceModel) { m.DockerCompose = types.StringValue("services:\n  app2:\n") }},
		{"pre_launch_script", func(m *appResourceModel) { m.PreLaunchScript = types.StringValue("#!/bin/sh\n") }},
		{"image", func(m *appResourceModel) { m.Image = types.StringValue("dstack-dev-0.5.9") }},
		{"size", func(m *appResourceModel) { m.Size = types.StringValue("tdx.medium") }},
		{"disk_size", func(m *appResourceModel) { m.DiskSize = types.Int64Value(40) }},
		{"public_logs", func(m *appResourceModel) { m.PublicLogs = types.BoolValue(true) }},
		{"public_sysinfo", func(m *appResourceModel) { m.PublicSysinfo = types.BoolValue(true) }},
		{"public_tcbinfo", func(m *appResourceModel) { m.PublicTCBInfo = types.BoolValue(true) }},
		{"gateway_enabled", func(m *appResourceModel) { m.GatewayEnabled = types.BoolValue(true) }},
		{"secure_time", func(m *appResourceModel) { m.SecureTime = types.BoolValue(true) }},
		{"encrypted_env", func(m *appResourceModel) { m.EncryptedEnv = types.StringValue("deadbeef") }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			plan := baseline
			tc.mutator(&plan)
			changed := detectUnsafeMembersUpdate(plan, baseline)
			if len(changed) == 0 {
				t.Fatalf("expected a change to %q to be detected, got none", tc.field)
			}
			found := false
			for _, p := range changed {
				if strings.Contains(p.String(), tc.field) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %q in detected changes, got %v", tc.field, changed)
			}
		})
	}
}

// TestDetectUnsafeMembersUpdate_ProviderLocalFieldsAreSafe asserts that
// changing fields that don't trigger cloud-side mutations (wait_for_ready,
// wait_timeout_seconds, members, replicas, name) doesn't trip the unsafe
// detector. members add/remove must remain in-place updatable so users
// can grow/shrink the slot set without recreating the parent app.
func TestDetectUnsafeMembersUpdate_ProviderLocalFieldsAreSafe(t *testing.T) {
	state := appResourceModel{
		Name:               types.StringValue("consul-0"),
		Members:            membersListFrom(t, "consul-0", "consul-1"),
		DockerCompose:      types.StringValue("services:\n  app:\n"),
		PreLaunchScript:    types.StringNull(),
		Env:                types.MapNull(types.StringType),
		EncryptedEnv:       types.StringNull(),
		EnvKeys:            types.ListNull(types.StringType),
		EnvComposeHash:     types.StringNull(),
		EnvTransactionHash: types.StringNull(),
		Image:              types.StringValue("dstack-dev-0.5.7"),
		Size:               types.StringValue("tdx.small"),
		DiskSize:           types.Int64Value(20),
		PublicLogs:         types.BoolValue(false),
		PublicSysinfo:      types.BoolValue(false),
		PublicTCBInfo:      types.BoolValue(false),
		GatewayEnabled:     types.BoolValue(false),
		SecureTime:         types.BoolValue(false),
		WaitForReady:       types.BoolValue(true),
		WaitTimeoutSecond:  types.Int64Value(600),
	}
	plan := state
	plan.Members = membersListFrom(t, "consul-0", "consul-1", "consul-2") // grow
	plan.WaitForReady = types.BoolValue(false)
	plan.WaitTimeoutSecond = types.Int64Value(900)

	changed := detectUnsafeMembersUpdate(plan, state)
	if len(changed) != 0 {
		t.Fatalf("provider-local changes should not be flagged, got %v", changed)
	}
}
