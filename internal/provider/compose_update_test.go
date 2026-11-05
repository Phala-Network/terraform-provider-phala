package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func assertJSONEqual(t *testing.T, label string, got any, wantJSON string) {
	t.Helper()
	var want any
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("%s: bad expectation: %v", label, err)
	}
	gotJSON, _ := json.Marshal(got)
	var gotNorm any
	_ = json.Unmarshal(gotJSON, &gotNorm)
	if !reflect.DeepEqual(gotNorm, want) {
		t.Fatalf("%s:\n got  %s\n want %s", label, gotJSON, wantJSON)
	}
}

// TestBuildComposeUpdateRequest asserts that a compose update sends only the
// changed fields, a removed pre-launch script as empty, and nothing at all
// when the compose does not change.
func TestBuildComposeUpdateRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := func() appResourceModel {
		return appResourceModel{
			DockerCompose:   types.StringValue("services: {}"),
			PreLaunchScript: types.StringValue("echo boot"),
			PublicLogs:      types.BoolValue(true),
			PublicSysinfo:   types.BoolValue(true),
			PublicTCBInfo:   types.BoolValue(true),
			GatewayEnabled:  types.BoolValue(true),
			SecureTime:      types.BoolValue(false),
		}
	}
	for _, tc := range []struct {
		name          string
		mutate        func(*appResourceModel)
		envKeys       []string
		updateEnvKeys bool
		want          string
	}{
		{"unchanged", func(*appResourceModel) {}, nil, false, ""},
		{"docker compose and removed script", func(m *appResourceModel) {
			m.DockerCompose = types.StringValue("services: {a: {}}")
			m.PreLaunchScript = types.StringNull()
		}, nil, false, `{"docker_compose_file":"services: {a: {}}","pre_launch_script":""}`},
		{"visibility, gateway and secure time", func(m *appResourceModel) {
			m.PublicLogs = types.BoolValue(false)
			m.GatewayEnabled = types.BoolValue(false)
			m.SecureTime = types.BoolValue(true)
		}, nil, false, `{"public_logs":false,"compose":{"gateway_enabled":false,"secure_time":true}}`},
		{"env keys", func(*appResourceModel) {}, []string{"A", "B"}, true, `{"allowed_envs":["A","B"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := base()
			tc.mutate(&plan)
			req, diags := buildComposeUpdateRequest(ctx, plan, base(), tc.envKeys, tc.updateEnvKeys)
			if diags.HasError() {
				t.Fatalf("diags: %v", diags)
			}
			if tc.want == "" {
				if req != nil {
					body, _ := json.Marshal(req)
					t.Fatalf("unchanged compose sent %s", body)
				}
				return
			}
			if req == nil {
				t.Fatal("changed compose sent nothing")
			}
			assertJSONEqual(t, "PATCH body", req, tc.want)
		})
	}
}
