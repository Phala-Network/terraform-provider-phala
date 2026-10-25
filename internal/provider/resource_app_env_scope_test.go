package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Bootstrap ownership must prevent writes to per-slot environments, not merely
// filter the resulting state after the cloud has already restarted those slots.
func TestMembersEnvironmentUpdateScope(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope types.String
		want  []string
		fail  bool
	}{
		{"legacy-default", types.StringNull(), []string{"/api/v1/cvms/vm-a/envs", "/api/v1/cvms/vm-b/envs"}, false},
		{"explicit-all", types.StringValue("all"), []string{"/api/v1/cvms/vm-a/envs", "/api/v1/cvms/vm-b/envs"}, false},
		{"bootstrap-only", types.StringValue("bootstrap"), []string{"/api/v1/cvms/vm-a/envs"}, false},
		{"invalid", types.StringValue("other"), nil, true},
		{"unknown-at-apply", types.StringUnknown(), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls = append(calls, r.URL.Path)
				mu.Unlock()
				if r.Method != http.MethodPatch {
					t.Errorf("unexpected method: %s", r.Method)
				}
				writeJSON(t, w, http.StatusOK, `{}`)
			}))
			defer srv.Close()
			r := &appResource{client: newTestPhalaClient(t, srv.URL+"/api/v1")}
			diags := r.applyMembersModeUpdate(context.Background(), applyMembersModeArgs{
				appID: "app_test", bootstrapID: "vm-a", vmUUIDs: []string{"vm-a", "vm-b"},
				plan:       appResourceModel{EnvUpdateScope: tc.scope},
				envCfg:     &envConfig{HasAutoEnv: true, EffectiveEncrypted: "encrypted", EffectiveEnvKeys: []string{"ORDINAL"}},
				envChanged: true,
			})
			if diags.HasError() != tc.fail {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("environment targets: got %v, want %v", calls, tc.want)
			}
		})
	}
}
