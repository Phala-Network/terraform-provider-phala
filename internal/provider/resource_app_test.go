package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAppResourceFetchAppAndCVMsReturnsReplicaListWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test":
			writeJSON(t, w, http.StatusOK, `{"app_id":"app_test","name":"demo","cvms":[]}`)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms":
			writeJSON(t, w, http.StatusServiceUnavailable, `{"message":"replica list unavailable"}`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	resource := &appResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	fetched, err := resource.fetchAppAndCVMs(context.Background(), "app_test")
	if err != nil {
		t.Fatalf("expected transient replica list failure to degrade to warning, got: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected fetch result")
	}
	if fetched.ReplicaListWarning == nil {
		t.Fatal("expected replica list warning")
	}
	apiErr, ok := fetched.ReplicaListWarning.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError warning, got %T: %v", fetched.ReplicaListWarning, fetched.ReplicaListWarning)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status code: got %d want %d", apiErr.StatusCode, http.StatusServiceUnavailable)
	}
	if len(fetched.CVMs) != 0 {
		t.Fatalf("expected no fresh cvm data on warning, got %#v", fetched.CVMs)
	}
}

func TestAppResourcePopulateStateKeepsUnmanagedPreLaunchScriptNull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cvms/cvm123/docker-compose.yml":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "services:\n  app:\n")
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cvms/cvm123/pre-launch-script":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "#!/bin/sh\necho ready\n")
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	resource := &appResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	state := appResourceModel{
		ID:              types.StringValue("app_test"),
		DockerCompose:   types.StringNull(),
		PreLaunchScript: types.StringNull(),
	}
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "demo",
	}
	cvms := []cvmAPIResponse{{
		VMUUID: "cvm123",
		Status: "running",
	}}

	diags := resource.populateState(context.Background(), &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !state.PreLaunchScript.IsNull() {
		t.Fatalf("pre_launch_script should remain null when unmanaged, got %#v", state.PreLaunchScript)
	}
	if state.DockerCompose.IsNull() || state.DockerCompose.ValueString() != "services:\n  app:\n" {
		t.Fatalf("docker_compose was not hydrated from API: %#v", state.DockerCompose)
	}
}

func TestAppResourcePopulateStateRefreshesManagedPreLaunchScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cvms/cvm123/pre-launch-script":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "#!/bin/sh\necho updated\n")
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	resource := &appResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	state := appResourceModel{
		ID:              types.StringValue("app_test"),
		PreLaunchScript: types.StringValue("#!/bin/sh\necho old\n"),
	}
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "demo",
	}
	cvms := []cvmAPIResponse{{
		VMUUID: "cvm123",
		Status: "running",
	}}

	diags := resource.populateState(context.Background(), &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.PreLaunchScript.IsNull() || state.PreLaunchScript.ValueString() != "#!/bin/sh\necho updated\n" {
		t.Fatalf("pre_launch_script was not refreshed from API: %#v", state.PreLaunchScript)
	}
}

func TestAppResourcePopulateStatePreservesReplicaDerivedFieldsWithoutFreshCVMs(t *testing.T) {
	ctx := context.Background()
	cvmIDs, diags := types.ListValueFrom(ctx, types.StringType, []string{"cvm-old"})
	if diags.HasError() {
		t.Fatalf("build cvm ids: %v", diags)
	}

	state := appResourceModel{
		ID:              types.StringValue("app_test"),
		AppID:           types.StringValue("app_test"),
		Name:            types.StringValue("existing"),
		DiskSize:        types.Int64Value(40),
		Status:          types.StringValue("running"),
		Endpoint:        types.StringValue("https://example"),
		PrimaryCVMID:    types.StringValue("cvm-old"),
		CVMIDs:          cvmIDs,
		DockerCompose:   types.StringValue("services:\n  app:\n"),
		PreLaunchScript: types.StringValue("#!/bin/sh\necho existing\n"),
	}
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "renamed",
	}

	resource := &appResource{}
	diags = resource.populateState(ctx, &state, app, nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.Name.ValueString() != "existing" {
		t.Fatalf("configured name should be preserved, got %q", state.Name.ValueString())
	}
	if state.DiskSize.ValueInt64() != 40 {
		t.Fatalf("disk size should be preserved, got %#v", state.DiskSize)
	}
	if state.Status.ValueString() != "running" {
		t.Fatalf("status should be preserved, got %#v", state.Status)
	}
	if state.Endpoint.ValueString() != "https://example" {
		t.Fatalf("endpoint should be preserved, got %#v", state.Endpoint)
	}
	if state.PrimaryCVMID.ValueString() != "cvm-old" {
		t.Fatalf("primary_cvm_id should be preserved, got %#v", state.PrimaryCVMID)
	}
	if state.CVMIDs.IsNull() || state.CVMIDs.IsUnknown() {
		t.Fatalf("cvm_ids should be preserved, got %#v", state.CVMIDs)
	}
	var ids []string
	diags = state.CVMIDs.ElementsAs(ctx, &ids, false)
	if diags.HasError() {
		t.Fatalf("decode cvm ids: %v", diags)
	}
	if len(ids) != 1 || ids[0] != "cvm-old" {
		t.Fatalf("unexpected cvm ids: %#v", ids)
	}
}

func TestAppResourcePopulateStatePrefersCVMMatchingAppName(t *testing.T) {
	ctx := context.Background()
	state := appResourceModel{
		ID:            types.StringValue("app_test"),
		AppID:         types.StringValue("app_test"),
		Name:          types.StringValue("consul-0"),
		DockerCompose: types.StringValue("services:\n  app:\n"),
	}
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "consul-1",
	}
	cvms := []cvmAPIResponse{
		{
			VMUUID: "vm-managed-slot",
			Name:   "consul-1",
			Status: "running",
			Endpoints: []struct {
				App string `json:"app"`
			}{{App: "https://managed.example"}},
		},
		{
			VMUUID: "vm-bootstrap",
			Name:   "consul-0",
			Status: "running",
			Endpoints: []struct {
				App string `json:"app"`
			}{{App: "https://bootstrap.example"}},
		},
	}

	resource := &appResource{}
	diags := resource.populateState(ctx, &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := state.PrimaryCVMID.ValueString(); got != "vm-bootstrap" {
		t.Fatalf("expected primary_cvm_id to prefer app name match, got %q", got)
	}
	if got := state.Name.ValueString(); got != "consul-0" {
		t.Fatalf("expected configured app name to be preserved, got %q", got)
	}
	if got := state.Endpoint.ValueString(); got != "https://bootstrap.example" {
		t.Fatalf("expected primary-derived endpoint from bootstrap CVM, got %q", got)
	}
}

func TestAppResourcePopulateStateBuildsComputedInstances(t *testing.T) {
	ctx := context.Background()
	state := appResourceModel{
		ID:            types.StringValue("app_test"),
		AppID:         types.StringValue("app_test"),
		DockerCompose: types.StringValue("services:\n  app:\n"),
	}
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "demo",
	}
	cvms := []cvmAPIResponse{
		{
			VMUUID:       "vm-b",
			InstanceID:   "inst-b",
			AppID:        "app_test",
			Name:         "demo-b",
			Status:       "running",
			CreatedAt:    "2026-05-02T11:00:00Z",
			InstanceType: "tdx.small",
			NodeInfo: &struct {
				Region string `json:"region"`
			}{Region: "us-west-1"},
			Endpoints: []struct {
				App string `json:"app"`
			}{{App: "https://b.example"}},
		},
		{
			VMUUID:       "vm-a",
			InstanceID:   "inst-a",
			AppID:        "app_test",
			Name:         "demo-a",
			Status:       "running",
			CreatedAt:    "2026-05-02T10:00:00Z",
			InstanceType: "tdx.small",
			NodeInfo: &struct {
				Region string `json:"region"`
			}{Region: "us-west-1"},
			Endpoints: []struct {
				App string `json:"app"`
			}{{App: "https://a.example"}},
		},
	}

	resource := &appResource{}
	diags := resource.populateState(ctx, &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.Instances.IsNull() || state.Instances.IsUnknown() {
		t.Fatalf("expected concrete instances list, got %#v", state.Instances)
	}
	var instances []appInstanceModel
	diags = state.Instances.ElementsAs(ctx, &instances, false)
	if diags.HasError() {
		t.Fatalf("decode instances: %v", diags)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	if got := instances[0].VMUUID.ValueString(); got != "vm-a" {
		t.Fatalf("expected instances sorted by created_at, got first vm_uuid %q", got)
	}
	if got := instances[0].InstanceID.ValueString(); got != "inst-a" {
		t.Fatalf("unexpected first instance_id: %q", got)
	}
	if got := instances[0].Endpoint.ValueString(); got != "https://a.example" {
		t.Fatalf("unexpected first endpoint: %q", got)
	}
	if got := instances[1].VMUUID.ValueString(); got != "vm-b" {
		t.Fatalf("unexpected second vm_uuid: %q", got)
	}
}

// TestAppResourcePopulateStatePopulatesGatewayFields covers the
// gateway_base_domain / gateway_cname fields surfaced in 0.3.0-beta.3.
// Downstream consumers (e.g. service-mesh) use these to compose per-port
// URLs without hardcoding the cloud's DNS suffix; the values must appear
// both on the top-level phala_app and on every computed instance entry.
func TestAppResourcePopulateStatePopulatesGatewayFields(t *testing.T) {
	ctx := context.Background()
	state := appResourceModel{
		ID:            types.StringValue("app_test"),
		AppID:         types.StringValue("app_test"),
		Name:          types.StringValue("demo-0"),
		DockerCompose: types.StringValue("services:\n  app:\n"),
	}
	app := &appAPIResponse{AppID: "app_test", Name: "demo-0"}

	bootstrapBase := "dstack-pha-prod5.phala.network"
	bootstrapCname := "demo.example.com"
	memberBase := "dstack-pha-prod5.phala.network"

	cvms := []cvmAPIResponse{
		{
			VMUUID:    "vm-bootstrap",
			Name:      "demo-0",
			Status:    "running",
			CreatedAt: "2026-05-19T10:00:00Z",
			Endpoints: []struct {
				App string `json:"app"`
			}{{App: "https://bootstrap.example"}},
			Gateway: &struct {
				BaseDomain *string `json:"base_domain"`
				Cname      *string `json:"cname"`
			}{BaseDomain: &bootstrapBase, Cname: &bootstrapCname},
		},
		{
			VMUUID:    "vm-member",
			Name:      "demo-1",
			Status:    "running",
			CreatedAt: "2026-05-19T11:00:00Z",
			Endpoints: []struct {
				App string `json:"app"`
			}{{App: "https://member.example"}},
			Gateway: &struct {
				BaseDomain *string `json:"base_domain"`
				Cname      *string `json:"cname"`
			}{BaseDomain: &memberBase},
		},
	}

	resource := &appResource{}
	diags := resource.populateState(ctx, &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := state.GatewayBaseDomain.ValueString(); got != bootstrapBase {
		t.Fatalf("top-level gateway_base_domain = %q, want %q", got, bootstrapBase)
	}
	if got := state.GatewayCname.ValueString(); got != bootstrapCname {
		t.Fatalf("top-level gateway_cname = %q, want %q", got, bootstrapCname)
	}

	var instances []appInstanceModel
	diags = state.Instances.ElementsAs(ctx, &instances, false)
	if diags.HasError() {
		t.Fatalf("decode instances: %v", diags)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	if got := instances[0].GatewayBaseDomain.ValueString(); got != bootstrapBase {
		t.Fatalf("instances[0] gateway_base_domain = %q, want %q", got, bootstrapBase)
	}
	if got := instances[0].GatewayCname.ValueString(); got != bootstrapCname {
		t.Fatalf("instances[0] gateway_cname = %q, want %q", got, bootstrapCname)
	}
	if got := instances[1].GatewayBaseDomain.ValueString(); got != memberBase {
		t.Fatalf("instances[1] gateway_base_domain = %q, want %q", got, memberBase)
	}
	if !instances[1].GatewayCname.IsNull() {
		t.Fatalf("instances[1] gateway_cname should be null when API omits cname, got %#v", instances[1].GatewayCname)
	}
}

// TestAppResourcePopulateStateReportsCVMIDsInMembersMode replaces the
// pre-0.3 "keep legacy replicas at 1 in members mode" test. With the
// replicas attribute gone, the only post-Read invariant left to check is
// that cvm_ids reflects every CVM currently attached to the app, including
// any that phala_app_instance created.
func TestAppResourcePopulateStateReportsCVMIDsInMembersMode(t *testing.T) {
	ctx := context.Background()
	members, memberDiags := types.ListValueFrom(ctx, types.StringType, []string{"consul-0", "consul-1"})
	if memberDiags.HasError() {
		t.Fatalf("build members: %v", memberDiags)
	}

	state := appResourceModel{
		ID:            types.StringValue("app_test"),
		AppID:         types.StringValue("app_test"),
		Members:       members,
		DockerCompose: types.StringValue("services:\n  app:\n"),
	}
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "consul-0",
	}
	cvms := []cvmAPIResponse{
		{VMUUID: "vm-a", AppID: "app_test", Name: "consul-0", Status: "running"},
		{VMUUID: "vm-b", AppID: "app_test", Name: "consul-1", Status: "running"},
	}

	resource := &appResource{}
	diags := resource.populateState(ctx, &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.CVMIDs.IsNull() || state.CVMIDs.IsUnknown() {
		t.Fatalf("cvm_ids should reflect every CVM, got %#v", state.CVMIDs)
	}
	var ids []string
	diags = state.CVMIDs.ElementsAs(ctx, &ids, false)
	if diags.HasError() {
		t.Fatalf("decode cvm ids: %v", diags)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 CVM ids, got %#v", ids)
	}
}

// TestImageMatchesUserForm covers the cvmAPIResponse helper that decides
// whether a user-supplied `image` value still refers to the same OS image
// the cloud is now reporting. The cloud splits the image identity into
// os.name + os.os_image_hash, but the `phala images` CLI displays the
// combined `<name>-<short-hash>` form and users routinely copy that.
// Both forms must be recognized so populateState can preserve the user's
// input without tripping Terraform's post-apply consistency check.
func TestImageMatchesUserForm(t *testing.T) {
	osHash := "9b6a523983685016c0bf4a8a4ad930f86d283e5308c30e10fc0136db7c85f1fe"
	cvm := cvmAPIResponse{
		OS: &struct {
			Name        string `json:"name"`
			OSImageHash string `json:"os_image_hash"`
		}{Name: "dstack-dev-0.5.7", OSImageHash: osHash},
	}

	cases := []struct {
		name   string
		input  string
		expect bool
	}{
		{"bare name matches", "dstack-dev-0.5.7", true},
		{"combined form with 8-char hash prefix", "dstack-dev-0.5.7-9b6a5239", true},
		{"combined form with 4-char hash prefix", "dstack-dev-0.5.7-9b6a", true},
		{"combined form with full hash", "dstack-dev-0.5.7-" + osHash, true},
		{"uppercase hex tolerated", "dstack-dev-0.5.7-9B6A5239", true},
		{"empty input", "", false},
		{"different image name", "dstack-dev-0.6.0", false},
		{"name prefix without separator", "dstack-dev-0.5.7x", false},
		{"non-hex suffix", "dstack-dev-0.5.7-abcdxyz9", false},
		{"hex prefix that does not match", "dstack-dev-0.5.7-deadbeef", false},
		{"empty suffix after dash", "dstack-dev-0.5.7-", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cvm.imageMatchesUserForm(tc.input); got != tc.expect {
				t.Fatalf("imageMatchesUserForm(%q) = %v, want %v", tc.input, got, tc.expect)
			}
		})
	}

	t.Run("no os hash falls back to name-only match", func(t *testing.T) {
		bare := cvmAPIResponse{
			OS: &struct {
				Name        string `json:"name"`
				OSImageHash string `json:"os_image_hash"`
			}{Name: "dstack-dev-0.5.7"},
		}
		if !bare.imageMatchesUserForm("dstack-dev-0.5.7") {
			t.Fatal("bare-name input should match when hash is absent")
		}
		if bare.imageMatchesUserForm("dstack-dev-0.5.7-9b6a5239") {
			t.Fatal("combined form cannot be verified without os_image_hash; must not claim a match")
		}
	})
}

// TestAppResourcePopulateStatePreservesUserImageForm covers the populateState
// branch that protects users who set `image` to the combined
// "<name>-<short-hash>" form printed by the cloud's image catalog and the
// `phala images` CLI. Before this fix, populateState always overwrote
// state.Image with the bare os.name, which tripped Terraform Core's
// post-apply consistency check on every Create with combined-form input
// ("Provider produced inconsistent result after apply: .image was X but
// now Y").
func TestAppResourcePopulateStatePreservesUserImageForm(t *testing.T) {
	ctx := context.Background()
	hash := "9b6a523983685016c0bf4a8a4ad930f86d283e5308c30e10fc0136db7c85f1fe"
	cvms := []cvmAPIResponse{
		{
			VMUUID: "vm-aaa",
			Name:   "demo-0",
			Status: "running",
			OS: &struct {
				Name        string `json:"name"`
				OSImageHash string `json:"os_image_hash"`
			}{Name: "dstack-dev-0.5.7", OSImageHash: hash},
		},
	}
	app := &appAPIResponse{AppID: "app_test", Name: "demo-0"}

	t.Run("combined form is preserved", func(t *testing.T) {
		state := appResourceModel{
			ID:            types.StringValue("app_test"),
			AppID:         types.StringValue("app_test"),
			Name:          types.StringValue("demo-0"),
			Image:         types.StringValue("dstack-dev-0.5.7-9b6a5239"),
			DockerCompose: types.StringValue("services:\n  app:\n"),
		}
		r := &appResource{}
		if diags := r.populateState(ctx, &state, app, cvms); diags.HasError() {
			t.Fatalf("populateState: %v", diags)
		}
		if got := state.Image.ValueString(); got != "dstack-dev-0.5.7-9b6a5239" {
			t.Fatalf("combined-form image not preserved: got %q", got)
		}
	})

	t.Run("bare name is preserved", func(t *testing.T) {
		state := appResourceModel{
			ID:            types.StringValue("app_test"),
			AppID:         types.StringValue("app_test"),
			Name:          types.StringValue("demo-0"),
			Image:         types.StringValue("dstack-dev-0.5.7"),
			DockerCompose: types.StringValue("services:\n  app:\n"),
		}
		r := &appResource{}
		if diags := r.populateState(ctx, &state, app, cvms); diags.HasError() {
			t.Fatalf("populateState: %v", diags)
		}
		if got := state.Image.ValueString(); got != "dstack-dev-0.5.7" {
			t.Fatalf("bare image not preserved: got %q", got)
		}
	})

	t.Run("unrelated prior value is overwritten with bare name", func(t *testing.T) {
		state := appResourceModel{
			ID:            types.StringValue("app_test"),
			AppID:         types.StringValue("app_test"),
			Name:          types.StringValue("demo-0"),
			Image:         types.StringValue("dstack-dev-0.6.0-deadbeef"),
			DockerCompose: types.StringValue("services:\n  app:\n"),
		}
		r := &appResource{}
		if diags := r.populateState(ctx, &state, app, cvms); diags.HasError() {
			t.Fatalf("populateState: %v", diags)
		}
		if got := state.Image.ValueString(); got != "dstack-dev-0.5.7" {
			t.Fatalf("expected unrelated prior to be overwritten with bare os.name, got %q", got)
		}
	})

	t.Run("null prior gets bare name", func(t *testing.T) {
		state := appResourceModel{
			ID:            types.StringValue("app_test"),
			AppID:         types.StringValue("app_test"),
			Name:          types.StringValue("demo-0"),
			DockerCompose: types.StringValue("services:\n  app:\n"),
		}
		r := &appResource{}
		if diags := r.populateState(ctx, &state, app, cvms); diags.HasError() {
			t.Fatalf("populateState: %v", diags)
		}
		if got := state.Image.ValueString(); got != "dstack-dev-0.5.7" {
			t.Fatalf("expected null prior to receive bare os.name, got %q", got)
		}
	})
}

func TestAppResourceWaitForAppReadyFailsFastOnStoppedReplica(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms":
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"cvm-stopped","status":"stopped","in_progress":false}]`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	resource := &appResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	err := resource.waitForAppReady(context.Background(), "app_test", 1, 2*time.Second)
	if err == nil {
		t.Fatal("expected waitForAppReady to fail on stable stopped replica")
	}
	if !strings.Contains(err.Error(), "cvm-stopped") {
		t.Fatalf("expected error to include replica id, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("expected error to include stopped status, got: %v", err)
	}
}

func TestAppResourceWaitForAppDeletionRetriesTransientVerificationErrors(t *testing.T) {
	var listCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms":
			switch listCalls.Add(1) {
			case 1:
				writeJSON(t, w, http.StatusServiceUnavailable, `{"message":"backend busy"}`)
			case 2:
				writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"cvm123","status":"deleting"}]`)
			default:
				writeJSON(t, w, http.StatusOK, `[]`)
			}
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	resource := &appResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	confirmed, err := resource.waitForAppDeletion(context.Background(), "test", time.Now().Add(2*time.Second), time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected delete wait error: %v", err)
	}
	if !confirmed {
		t.Fatal("expected deletion to be confirmed after retry")
	}
	if calls := listCalls.Load(); calls < 3 {
		t.Fatalf("expected delete wait to continue polling after retryable error, got %d list calls", calls)
	}
}

func TestAppResourcePopulateStatePrefersComposeFileVisibilityFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cvms/cvm123":
			writeJSON(t, w, http.StatusOK, `{
				"vm_uuid":"cvm123",
				"status":"running",
				"public_logs":true,
				"public_sysinfo":true,
				"public_tcbinfo":true,
				"compose_file":{
					"public_logs":false,
					"public_sysinfo":false,
					"public_tcbinfo":false,
					"storage_fs":"zfs"
				}
			}`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	resource := &appResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}
	state := appResourceModel{
		ID:              types.StringValue("app_test"),
		DockerCompose:   types.StringValue("services:\n  app:\n"),
		PreLaunchScript: types.StringNull(),
	}
	trueValue := true
	app := &appAPIResponse{
		AppID: "app_test",
		Name:  "demo",
	}
	cvms := []cvmAPIResponse{{
		VMUUID:        "cvm123",
		Status:        "running",
		PublicLogs:    &trueValue,
		PublicSysinfo: &trueValue,
		PublicTCBInfo: &trueValue,
	}}

	diags := resource.populateState(context.Background(), &state, app, cvms)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.PublicLogs.IsNull() || state.PublicLogs.ValueBool() {
		t.Fatalf("public_logs should prefer compose_file value, got %#v", state.PublicLogs)
	}
	if state.PublicSysinfo.IsNull() || state.PublicSysinfo.ValueBool() {
		t.Fatalf("public_sysinfo should prefer compose_file value, got %#v", state.PublicSysinfo)
	}
	if state.PublicTCBInfo.IsNull() || state.PublicTCBInfo.ValueBool() {
		t.Fatalf("public_tcbinfo should prefer compose_file value, got %#v", state.PublicTCBInfo)
	}
	if state.StorageFS.IsNull() || state.StorageFS.ValueString() != "zfs" {
		t.Fatalf("storage_fs should prefer compose_file value, got %#v", state.StorageFS)
	}
}

func TestComposeEnvKeysFromAttrs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("prefers_env_map", func(t *testing.T) {
		envMap, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{
			"ZED": "1",
			"API": "2",
		})
		if diags.HasError() {
			t.Fatalf("build env map: %v", diags)
		}

		keys, known, diags := composeEnvKeysFromAttrs(ctx, envMap, types.ListNull(types.StringType))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !known {
			t.Fatal("expected env keys to be known")
		}
		if len(keys) != 2 || keys[0] != "API" || keys[1] != "ZED" {
			t.Fatalf("unexpected env keys: %#v", keys)
		}
	})

	t.Run("uses_env_keys_list", func(t *testing.T) {
		envKeys, diags := types.ListValueFrom(ctx, types.StringType, []string{"ZED", "API"})
		if diags.HasError() {
			t.Fatalf("build env_keys list: %v", diags)
		}

		keys, known, diags := composeEnvKeysFromAttrs(ctx, types.MapNull(types.StringType), envKeys)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !known {
			t.Fatal("expected env keys to be known")
		}
		if len(keys) != 2 || keys[0] != "API" || keys[1] != "ZED" {
			t.Fatalf("unexpected env keys: %#v", keys)
		}
	})

	t.Run("handles_explicit_empty_env", func(t *testing.T) {
		envMap, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{})
		if diags.HasError() {
			t.Fatalf("build empty env map: %v", diags)
		}

		keys, known, diags := composeEnvKeysFromAttrs(ctx, envMap, types.ListNull(types.StringType))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !known {
			t.Fatal("expected empty env map to be treated as explicit")
		}
		if len(keys) != 0 {
			t.Fatalf("unexpected env keys: %#v", keys)
		}
	})
}
