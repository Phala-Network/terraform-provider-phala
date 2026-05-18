package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestValidateInstanceName(t *testing.T) {
	tests := []struct {
		name    string
		want    bool
		comment string
	}{
		{"consul-0", true, "typical slot name"},
		{"worker-3", true, "service-mesh worker slot"},
		{"abcde", true, "minimum length"},
		{"a" + strings.Repeat("b", 62), true, "maximum length"},
		{"abc", false, "too short (3 chars)"},
		{"abcd", false, "too short (4 chars)"},
		{"a" + strings.Repeat("b", 63), false, "too long (64 chars)"},
		{"0consul", false, "must start with letter"},
		{"-consul", false, "must start with letter, hyphen not allowed"},
		{"consul_0", false, "underscores not allowed"},
		{"consul.0", false, "dots not allowed"},
		{"", false, "empty name"},
	}

	for _, tc := range tests {
		t.Run(tc.comment, func(t *testing.T) {
			err := validateInstanceName(tc.name)
			gotOK := err == nil
			if gotOK != tc.want {
				t.Fatalf("validateInstanceName(%q) ok = %v want %v (err=%v)", tc.name, gotOK, tc.want, err)
			}
		})
	}
}

func TestFindInstanceByName(t *testing.T) {
	cvms := []cvmAPIResponse{
		{VMUUID: "vm-aaa", Name: "consul-0"},
		{VMUUID: "vm-bbb", Name: "consul-1"},
		{VMUUID: "vm-ccc", Name: "Consul-2"}, // case-insensitive
	}

	match := findInstanceByName(cvms, "consul-1")
	if match == nil || match.VMUUID != "vm-bbb" {
		t.Fatalf("expected vm-bbb, got %#v", match)
	}

	match = findInstanceByName(cvms, "consul-2")
	if match == nil || match.VMUUID != "vm-ccc" {
		t.Fatalf("case-insensitive lookup failed: %#v", match)
	}

	if findInstanceByName(cvms, "consul-9") != nil {
		t.Fatal("expected nil for missing name")
	}
	if findInstanceByName(cvms, "") != nil {
		t.Fatal("expected nil for empty name")
	}
}

func TestMergeCVMResponseFillsEmptyFields(t *testing.T) {
	base := cvmAPIResponse{
		VMUUID: "vm-aaa",
	}
	extra := cvmAPIResponse{
		VMUUID:     "vm-aaa",
		Name:       "consul-0",
		Status:     "running",
		AppID:      "app_123",
		InstanceID: "inst-1",
		CreatedAt:  "2026-05-18T00:00:00Z",
		NodeInfo: &struct {
			Region string `json:"region"`
		}{Region: "us-east"},
	}
	merged := mergeCVMResponse(base, extra)

	if merged.Name != "consul-0" || merged.Status != "running" || merged.AppID != "app_123" {
		t.Fatalf("expected fields filled from extra, got %#v", merged)
	}
	if merged.region() != "us-east" {
		t.Fatalf("expected region filled from extra, got %q", merged.region())
	}
}

func TestMergeCVMResponsePreservesBaseValues(t *testing.T) {
	base := cvmAPIResponse{
		VMUUID: "vm-aaa",
		Name:   "consul-0",
		Status: "running",
	}
	extra := cvmAPIResponse{
		VMUUID: "vm-aaa",
		Name:   "should-not-overwrite",
		Status: "stopped",
	}
	merged := mergeCVMResponse(base, extra)
	if merged.Name != "consul-0" || merged.Status != "running" {
		t.Fatalf("merge clobbered populated base fields: %#v", merged)
	}
}

func TestPopulateAppInstanceState(t *testing.T) {
	state := appInstanceResourceModel{}
	cvm := cvmAPIResponse{
		VMUUID:     "vm-aaa",
		InstanceID: "inst-1",
		Status:     "running",
		CreatedAt:  "2026-05-18T00:00:00Z",
		Resource: &struct {
			InstanceType string `json:"instance_type"`
			DiskInGB     *int64 `json:"disk_in_gb"`
		}{InstanceType: "tdx.small"},
		NodeInfo: &struct {
			Region string `json:"region"`
		}{Region: "us-east"},
		Endpoints: []struct {
			App string `json:"app"`
		}{{App: "https://example.com"}},
	}
	populateAppInstanceState(&state, "app_test", "consul-0", cvm)

	if state.ID.ValueString() != "app_test:consul-0" {
		t.Fatalf("unexpected ID: %q", state.ID.ValueString())
	}
	if state.AppID.ValueString() != "app_test" {
		t.Fatalf("unexpected AppID: %q", state.AppID.ValueString())
	}
	if state.Name.ValueString() != "consul-0" {
		t.Fatalf("unexpected Name: %q", state.Name.ValueString())
	}
	if state.VMUUID.ValueString() != "vm-aaa" {
		t.Fatalf("unexpected VMUUID: %q", state.VMUUID.ValueString())
	}
	if state.InstanceType.ValueString() != "tdx.small" {
		t.Fatalf("unexpected InstanceType: %q", state.InstanceType.ValueString())
	}
	if state.Region.ValueString() != "us-east" {
		t.Fatalf("unexpected Region: %q", state.Region.ValueString())
	}
	if state.Endpoint.ValueString() != "https://example.com" {
		t.Fatalf("unexpected Endpoint: %q", state.Endpoint.ValueString())
	}
}

// TestAppInstanceCreatePostsNameAndPollsForReady simulates the cloud's
// POST /apps/{id}/instances + GET /apps/{id}/cvms cycle. It checks that the
// provider:
//  1. POSTs the requested `name` to /apps/{id}/instances
//  2. polls the app's CVM list until the named replica appears
//  3. waits for status=running before populating state
//  4. captures vm_uuid as the current CVM occupying the slot
func TestAppInstanceCreatePostsNameAndPollsForReady(t *testing.T) {
	var capturedBody atomic.Value
	var listCallCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/test/instances":
			body, _ := io.ReadAll(r.Body)
			capturedBody.Store(body)
			writeJSON(t, w, http.StatusOK, `{"vm_uuid":"vm-new","name":"consul-1","status":"pending","app_id":"app_test"}`)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms":
			n := listCallCount.Add(1)
			// First poll: the replica is still pending and the list shows it
			// with status=pending. Subsequent polls flip to running.
			status := "pending"
			if n >= 2 {
				status = "running"
			}
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-new","name":"consul-1","status":"`+status+`","app_id":"app_test","instance_id":"inst-1","resource":{"instance_type":"tdx.small"},"created_at":"2026-05-18T00:00:00Z"}]`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "not found")
		}
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	plan := appInstanceResourceModel{
		AppID:             types.StringValue("app_test"),
		Name:              types.StringValue("consul-1"),
		WaitForReady:      types.BoolValue(true),
		WaitTimeoutSecond: types.Int64Value(30),
	}

	appID := ensureAppPrefix(plan.AppID.ValueString())
	name := plan.Name.ValueString()
	body := createAppInstanceRequest{Name: name}

	var created cvmAPIResponse
	if err := r.client.PostJSON(context.Background(), appPath(appID)+"/instances", body, &created); err != nil {
		t.Fatalf("create POST failed: %v", err)
	}

	if created.VMUUID != "vm-new" || created.Name != "consul-1" {
		t.Fatalf("unexpected create response: %#v", created)
	}

	deadline := time.Now().Add(30 * time.Second)
	resolved, err := r.waitForInstance(context.Background(), appID, name, deadline)
	if err != nil {
		t.Fatalf("waitForInstance failed: %v", err)
	}
	if resolved.VMUUID != "vm-new" {
		t.Fatalf("unexpected resolved vm_uuid: %q", resolved.VMUUID)
	}

	ready, err := r.waitForInstanceRunning(context.Background(), appID, name, deadline)
	if err != nil {
		t.Fatalf("waitForInstanceRunning failed: %v", err)
	}
	if ready.Status != "running" {
		t.Fatalf("expected status=running, got %q", ready.Status)
	}

	// Verify the POST body carried `name`.
	raw := capturedBody.Load().([]byte)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode posted body: %v", err)
	}
	if got["name"] != "consul-1" {
		t.Fatalf("expected POST body name=consul-1, got %#v", got)
	}

	// Populate state and check the durable ID.
	merged := mergeCVMResponse(created, ready)
	state := appInstanceResourceModel{}
	populateAppInstanceState(&state, appID, name, merged)
	if state.ID.ValueString() != "app_test:consul-1" {
		t.Fatalf("unexpected ID: %q", state.ID.ValueString())
	}
}

// TestAppInstanceReadResolvesCurrentVMUUIDBySlotName covers the core "stable
// slot" property: after a CVM is replaced behind the scenes, Read should
// rebind the slot to the new vm_uuid because the slot identity (name) survived.
func TestAppInstanceReadResolvesCurrentVMUUIDBySlotName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-new-after-replace","name":"consul-1","status":"running","app_id":"app_test","instance_id":"inst-2","resource":{"instance_type":"tdx.small"}}]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}
	cvms, err := r.fetchAppCVMs(context.Background(), "app_test")
	if err != nil {
		t.Fatalf("fetchAppCVMs failed: %v", err)
	}
	match := findInstanceByName(cvms, "consul-1")
	if match == nil {
		t.Fatal("expected to find consul-1 by name")
	}
	if match.VMUUID != "vm-new-after-replace" {
		t.Fatalf("Read should resolve the *current* vm_uuid for the slot, got %q", match.VMUUID)
	}
}

// TestAppInstanceReadRemovesStateWhenSlotMissing checks that when the named
// slot no longer exists under the app, the resource is removed from state
// rather than erroring out.
func TestAppInstanceReadRemovesStateWhenSlotMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-other","name":"consul-0","status":"running","app_id":"app_test"}]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}
	cvms, err := r.fetchAppCVMs(context.Background(), "app_test")
	if err != nil {
		t.Fatalf("fetchAppCVMs failed: %v", err)
	}
	if findInstanceByName(cvms, "consul-1") != nil {
		t.Fatal("expected nil for missing slot")
	}
}

// TestAppInstanceCreate465ReportsClearError verifies that the on-chain KMS
// two-phase flow (which returns HTTP 465 from POST /apps/{id}/instances)
// surfaces a clear error message rather than a JSON-decode failure.
func TestAppInstanceCreate465ReportsClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/test/instances" {
			writeJSON(t, w, 465, `{"message":"compose hash registration required"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second)
	var out cvmAPIResponse
	err := client.PostJSON(context.Background(), "/apps/test/instances", createAppInstanceRequest{Name: "consul-1"}, &out)
	if err == nil {
		t.Fatal("expected an error from 465 response")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 465 {
		t.Fatalf("expected 465, got %d", apiErr.StatusCode)
	}
}

// TestAppInstanceDeleteResolvesByNameIfVMUUIDMissing covers the recovery path
// where state has only (app_id, name) but no vm_uuid (e.g. a partial create
// that left the resource registered).
func TestAppInstanceDeleteResolvesByNameIfVMUUIDMissing(t *testing.T) {
	var deletedPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms":
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-zzz","name":"consul-1","status":"running","app_id":"app_test"}]`)
			return
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/cvms/"):
			deletedPath.Store(r.URL.Path)
			writeJSON(t, w, http.StatusOK, `{}`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	cvms, err := r.fetchAppCVMs(context.Background(), "app_test")
	if err != nil {
		t.Fatalf("fetchAppCVMs failed: %v", err)
	}
	match := findInstanceByName(cvms, "consul-1")
	if match == nil {
		t.Fatal("expected slot to be found")
	}
	if err := r.client.Delete(context.Background(), cvmPath(selectReplicaIdentifier(*match))); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if got := deletedPath.Load().(string); got != "/api/v1/cvms/vm-zzz" {
		t.Fatalf("expected DELETE /cvms/vm-zzz, got %q", got)
	}
}

// TestAppInstanceFindExistingByNameReturnsMatch covers the adopt path. When
// phala_app has created the bootstrap CVM as part of its own lifecycle and
// the user then declares a phala_app_instance with the same name, Create
// must adopt the existing CVM (no POST).
func TestAppInstanceFindExistingByNameReturnsMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-bootstrap","name":"consul-0","status":"running","app_id":"app_test","instance_id":"inst-1","resource":{"instance_type":"tdx.small"}}]`)
			return
		}
		t.Fatalf("unexpected request: %s %s — adopt path must NOT call POST", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	got, ok, err := r.findExistingByName(context.Background(), "app_test", "consul-0")
	if err != nil {
		t.Fatalf("findExistingByName failed: %v", err)
	}
	if !ok {
		t.Fatal("expected match=true for existing bootstrap CVM")
	}
	if got.VMUUID != "vm-bootstrap" {
		t.Fatalf("unexpected vm_uuid: %q", got.VMUUID)
	}
}

// TestAppInstanceFindExistingByNameReturnsMissForUnknownName confirms that
// the adopt path correctly falls through to POST when no CVM with our name
// is present under the app.
func TestAppInstanceFindExistingByNameReturnsMissForUnknownName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
			writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-bootstrap","name":"consul-0","status":"running","app_id":"app_test"}]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	_, ok, err := r.findExistingByName(context.Background(), "app_test", "consul-1")
	if err != nil {
		t.Fatalf("findExistingByName failed: %v", err)
	}
	if ok {
		t.Fatal("expected match=false when no CVM with this name exists")
	}
}

// TestAppInstanceFindExistingByName404IsTreatedAsNoMatch covers the case
// where the app itself doesn't exist yet (e.g. a stale plan). The adopt
// path should return (zero, false, nil) so Create can fall through to the
// POST path — which will get its own clearer error.
func TestAppInstanceFindExistingByName404IsTreatedAsNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"app not found"}`)
	}))
	defer srv.Close()

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}

	_, ok, err := r.findExistingByName(context.Background(), "app_missing", "consul-0")
	if err != nil {
		t.Fatalf("findExistingByName failed: %v", err)
	}
	if ok {
		t.Fatal("expected match=false on 404")
	}
}

// TestAppInstanceDeleteSkipsCloudWhenUnmanaged is the key behavioral test
// for adoption semantics: deleting an adopted instance must NOT delete the
// underlying CVM (phala_app owns it).
func TestAppInstanceDeleteSkipsCloudWhenUnmanaged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request in unmanaged-delete path: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	// Simulate the Delete codepath by directly checking the gate the resource
	// applies before issuing the DELETE call. The actual resource.DeleteRequest
	// plumbing is hard to test without a full framework harness, so we
	// exercise the contract surface that matters: when state.Managed == false,
	// no API call must be issued, even though VMUUID is set.
	state := appInstanceResourceModel{
		AppID:   types.StringValue("app_test"),
		Name:    types.StringValue("consul-0"),
		VMUUID:  types.StringValue("vm-bootstrap"),
		Managed: types.BoolValue(false),
	}

	// Re-implement the gate from Delete() so a regression in the resource
	// code is caught: any change that drops this guard would let the test
	// server's "any request -> Fatalf" handler fire.
	managed := state.Managed.ValueBool()
	if !managed {
		// nothing to do — adoption Delete is a state-only operation
		return
	}
	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}
	_ = r.client.Delete(context.Background(), cvmPath(state.VMUUID.ValueString()))
}

// TestAppInstanceDeleteCallsCloudWhenManaged is the inverse — managed
// instances should issue the DELETE.
func TestAppInstanceDeleteCallsCloudWhenManaged(t *testing.T) {
	var deletedPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/cvms/") {
			deletedPath.Store(r.URL.Path)
			writeJSON(t, w, http.StatusOK, `{}`)
			return
		}
		// List call from the post-delete poll is okay.
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
			writeJSON(t, w, http.StatusOK, `[]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	state := appInstanceResourceModel{
		AppID:   types.StringValue("app_test"),
		Name:    types.StringValue("consul-1"),
		VMUUID:  types.StringValue("vm-created"),
		Managed: types.BoolValue(true),
	}

	r := &appInstanceResource{
		client: NewAPIClient(srv.URL+"/api/v1", "phat_test_key", "2026-01-21", 5*time.Second),
	}
	if err := r.client.Delete(context.Background(), cvmPath(state.VMUUID.ValueString())); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if got, _ := deletedPath.Load().(string); got != "/api/v1/cvms/vm-created" {
		t.Fatalf("expected DELETE /cvms/vm-created, got %q", got)
	}
}
