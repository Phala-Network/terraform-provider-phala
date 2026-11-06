package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAppHasMembers_LiveCases(t *testing.T) {
	tests := []struct {
		name string
		m    appResourceModel
		want bool
	}{
		{"null", appResourceModel{Members: types.ListNull(types.StringType)}, false},
		{"unknown", appResourceModel{Members: types.ListUnknown(types.StringType)}, false},
		{"empty", appResourceModel{Members: emptyMembersList(t)}, false},
		{"non-empty", appResourceModel{Members: membersListFromValues(t, "consul-0")}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := appHasMembers(tc.m); got != tc.want {
				t.Fatalf("appHasMembers = %v, want %v", got, tc.want)
			}
		})
	}
}

func emptyMembersList(t *testing.T) types.List {
	t.Helper()
	v, diags := types.ListValue(types.StringType, nil)
	if diags.HasError() {
		t.Fatalf("build empty list: %v", diags)
	}
	return v
}

func membersListFromValues(t *testing.T, vals ...string) types.List {
	t.Helper()
	v, diags := types.ListValueFrom(context.Background(), types.StringType, vals)
	if diags.HasError() {
		t.Fatalf("build members list: %v", diags)
	}
	return v
}

func TestCollectVMUUIDs(t *testing.T) {
	emptyStr := ""
	spaceStr := "   "
	cvms := []phala.CVMInfo{
		{CVMInfoFields: phala.CVMInfoFields{VMUUID: strPtr("vm-a")}},
		{CVMInfoFields: phala.CVMInfoFields{VMUUID: &emptyStr}},
		{CVMInfoFields: phala.CVMInfoFields{VMUUID: &spaceStr}}, // whitespace
		{CVMInfoFields: phala.CVMInfoFields{VMUUID: strPtr("vm-c")}},
	}
	got := collectVMUUIDs(cvms)
	if len(got) != 2 || got[0] != "vm-a" || got[1] != "vm-c" {
		t.Fatalf("expected [vm-a vm-c], got %#v", got)
	}
}

// TestProvisionAndFindRevision exercises the provision -> list-revisions
// resolve chain that runs before redeploy in members-mode Update.
func TestProvisionAndFindRevision(t *testing.T) {
	const wantHash = "abc123hash"
	const wantRevID = "rev_42"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cvms/vm-boot/compose_file/provision":
			writeJSON(t, w, http.StatusOK, `{"compose_hash":"`+wantHash+`","app_id":"app_test"}`)
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/apps/test/revisions"):
			writeJSON(t, w, http.StatusOK, `{"revisions":[
				{"revision_id":"rev_41","compose_hash":"oldhash"},
				{"revision_id":"`+wantRevID+`","compose_hash":"`+wantHash+`"}
			],"total":2,"page":1,"page_size":50,"total_pages":1}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "not found")
	}))
	defer srv.Close()

	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	composeHash, err := r.provisionComposeRevision(context.Background(), "vm-boot", map[string]any{
		"name": "demo-coordinator-0",
	})
	if err != nil {
		t.Fatalf("provisionComposeRevision: %v", err)
	}
	if composeHash != wantHash {
		t.Fatalf("compose_hash = %q want %q", composeHash, wantHash)
	}
	revID, err := r.findRevisionIDByComposeHash(context.Background(), "app_test", composeHash)
	if err != nil {
		t.Fatalf("findRevisionIDByComposeHash: %v", err)
	}
	if revID != wantRevID {
		t.Fatalf("revision_id = %q want %q", revID, wantRevID)
	}
}

// TestFindRevisionPaginates walks through page 2 when the target hash isn't
// in page 1.
func TestFindRevisionPaginates(t *testing.T) {
	var pagesServed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/apps/test/revisions") {
			page := r.URL.Query().Get("page")
			pagesServed.Add(1)
			if page == "2" {
				writeJSON(t, w, http.StatusOK, `{"revisions":[
					{"revision_id":"rev_winner","compose_hash":"targethash"}
				],"total":2,"page":2,"page_size":50,"total_pages":2}`)
				return
			}
			writeJSON(t, w, http.StatusOK, `{"revisions":[
				{"revision_id":"rev_other","compose_hash":"unrelated"}
			],"total":2,"page":1,"page_size":50,"total_pages":2}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	revID, err := r.findRevisionIDByComposeHash(context.Background(), "app_test", "targethash")
	if err != nil {
		t.Fatalf("findRevisionIDByComposeHash: %v", err)
	}
	if revID != "rev_winner" {
		t.Fatalf("expected rev_winner from page 2, got %q", revID)
	}
	if pagesServed.Load() != 2 {
		t.Fatalf("expected to fetch 2 pages, got %d", pagesServed.Load())
	}
}

// TestRedeployAcrossCVMsPostsExpectedBody confirms the redeploy POST
// targets the right path and passes every vm_uuid through.
func TestRedeployAcrossCVMsPostsExpectedBody(t *testing.T) {
	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/test/revisions/rev_42/redeploy" {
			b, _ := io.ReadAll(r.Body)
			body.Store(b)
			writeJSON(t, w, http.StatusAccepted, `{"message":"ok"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	err := r.redeployRevisionAcrossCVMs(context.Background(), "app_test", "rev_42",
		[]string{"vm-aaa", "vm-bbb"})
	if err != nil {
		t.Fatalf("redeployRevisionAcrossCVMs: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body.Load().([]byte), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	uuids, ok := got["vm_uuids"].([]any)
	if !ok || len(uuids) != 2 || uuids[0] != "vm-aaa" || uuids[1] != "vm-bbb" {
		t.Fatalf("unexpected redeploy body: %#v", got)
	}
}

// TestRedeploySurfaces465 confirms the on-chain KMS path returns a clear
// "kms = phala only" error, not a raw 465 JSON-decode failure.
func TestRedeploySurfaces465(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/redeploy") {
			writeJSON(t, w, 465, `{"detail":"compose hash registration required"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	err := r.redeployRevisionAcrossCVMs(context.Background(), "app_test", "rev_42", []string{"vm-aaa"})
	if err == nil {
		t.Fatal("expected error from 465")
	}
	if !strings.Contains(err.Error(), "on-chain KMS") || !strings.Contains(err.Error(), "phala") {
		t.Fatalf("expected explanatory message about kms=phala only, got %v", err)
	}
}

// TestPatchEnvAcrossCVMsFansOutSequentially confirms the env fan-out hits
// each CVM in order and stops on the first error.
func TestPatchEnvAcrossCVMsFansOutSequentially(t *testing.T) {
	var (
		mu          sync.Mutex
		patchedPath []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/envs") {
			mu.Lock()
			patchedPath = append(patchedPath, r.URL.Path)
			mu.Unlock()
			writeJSON(t, w, http.StatusOK, `{}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	err := r.patchEnvAcrossCVMs(context.Background(), []string{"vm-a", "vm-b", "vm-c"},
		&phala.UpdateEnvsRequest{EncryptedEnv: "deadbeef"})
	if err != nil {
		t.Fatalf("patchEnvAcrossCVMs: %v", err)
	}
	if len(patchedPath) != 3 ||
		patchedPath[0] != "/api/v1/cvms/vm-a/envs" ||
		patchedPath[1] != "/api/v1/cvms/vm-b/envs" ||
		patchedPath[2] != "/api/v1/cvms/vm-c/envs" {
		t.Fatalf("unexpected fan-out sequence: %#v", patchedPath)
	}
}

func TestPatchEnvAcrossCVMsFailFast(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/envs") {
			mu.Lock()
			attempts = append(attempts, r.URL.Path)
			isSecond := len(attempts) == 2
			mu.Unlock()
			if isSecond {
				writeJSON(t, w, http.StatusBadRequest, `{"detail":"second CVM rejected"}`)
				return
			}
			writeJSON(t, w, http.StatusOK, `{}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	err := r.patchEnvAcrossCVMs(context.Background(), []string{"vm-a", "vm-b", "vm-c"},
		&phala.UpdateEnvsRequest{EncryptedEnv: "deadbeef"})
	if err == nil {
		t.Fatal("expected error from second CVM rejection")
	}
	if !strings.Contains(err.Error(), "vm-b") {
		t.Fatalf("expected error to identify failing CVM, got %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected fail-fast after 2 calls, got %d", len(attempts))
	}
}

// TestWaitForCVMsOnComposeHashSucceedsOnAllSettled verifies the poll
// returns once every CVM reports the expected compose_hash AND is running
// AND is not in-progress.
func TestWaitForCVMsOnComposeHashSucceedsOnAllSettled(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/test/cvms" {
			n := attempts.Add(1)
			// First poll: still updating with old hash.
			if n < 2 {
				writeJSON(t, w, http.StatusOK, `[
					{"vm_uuid":"vm-a","compose_hash":"old","status":"updating","in_progress":true},
					{"vm_uuid":"vm-b","compose_hash":"old","status":"running"}
				]`)
				return
			}
			// Second poll: both on the new hash and running.
			writeJSON(t, w, http.StatusOK, `[
				{"vm_uuid":"vm-a","compose_hash":"NEWHASH","status":"running"},
				{"vm_uuid":"vm-b","compose_hash":"NEWHASH","status":"running"}
			]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &appResource{
		client: newTestPhalaClient(t, srv.URL+"/api/v1"),
	}
	if err := r.waitForCVMsOnComposeHash(context.Background(), "app_test", "newhash", nil,
		time.Now().Add(30*time.Second)); err != nil {
		t.Fatalf("waitForCVMsOnComposeHash: %v", err)
	}
	if attempts.Load() < 2 {
		t.Fatalf("expected at least 2 polls, got %d", attempts.Load())
	}
}

// TestMembersModeComposeUpdatePatchesBootstrapThenRedeploys asserts that a
// members-mode compose change goes to the bootstrap as one unified PATCH
// (keeping compose keys Terraform does not manage) and that the resulting
// revision is redeployed to every other slot.
func TestMembersModeComposeUpdatePatchesBootstrapThenRedeploys(t *testing.T) {
	var mu sync.Mutex
	var writes []string
	var patchBody, redeployBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		if r.Method != http.MethodGet {
			mu.Lock()
			writes = append(writes, r.Method+" "+path)
			mu.Unlock()
		}
		switch {
		case r.Method == http.MethodPatch && path == "/cvms/vm-boot":
			patchBody, _ = io.ReadAll(r.Body)
			writeJSON(t, w, http.StatusAccepted, `{"status":"accepted","correlation_id":"c1"}`)
		case r.Method == http.MethodGet && path == "/cvms/vm-boot":
			writeJSON(t, w, http.StatusOK, `{"vm_uuid":"vm-boot","compose_hash":"newhash","status":"running"}`)
		case r.Method == http.MethodGet && path == "/apps/test/revisions":
			writeJSON(t, w, http.StatusOK, `{"revisions":[{"revision_id":"rev_9","compose_hash":"newhash"}],"total":1,"page":1,"page_size":50,"total_pages":1}`)
		case r.Method == http.MethodPost && path == "/apps/test/revisions/rev_9/redeploy":
			redeployBody, _ = io.ReadAll(r.Body)
			writeJSON(t, w, http.StatusAccepted, `{"message":"ok"}`)
		case r.Method == http.MethodGet && path == "/apps/test/cvms":
			writeJSON(t, w, http.StatusOK, `[
				{"vm_uuid":"vm-boot","compose_hash":"newhash","status":"running"},
				{"vm_uuid":"vm-peer","compose_hash":"newhash","status":"running"}
			]`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := &appResource{client: newTestPhalaClient(t, srv.URL+"/api/v1")}
	swap := "4G"
	diags := r.applyMembersModeUpdate(context.Background(), applyMembersModeArgs{
		appID:       "app_test",
		bootstrapID: "vm-boot",
		vmUUIDs:     []string{"vm-boot", "vm-peer"},
		plan:        appResourceModel{WaitTimeoutSecond: types.Int64Value(30)},
		composeReq:  &phala.PatchCVMRequest{Compose: &phala.ComposePatch{SwapSize: phala.PatchValue(swap)}},
	})
	if diags.HasError() {
		t.Fatalf("applyMembersModeUpdate: %v", diags)
	}
	want := []string{"PATCH /cvms/vm-boot", "POST /apps/test/revisions/rev_9/redeploy"}
	if strings.Join(writes, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v, want %v", writes, want)
	}
	if string(patchBody) != `{"compose":{"swap_size":"4G"}}` {
		t.Fatalf("PATCH body = %s", patchBody)
	}
	if string(redeployBody) != `{"vm_uuids":["vm-peer"]}` {
		t.Fatalf("redeploy body = %s", redeployBody)
	}
}

// TestMembersModeComposeUpdateKeepsStoppedCVMsStopped asserts that a
// members-mode compose update of stopped CVMs completes without error: the
// bootstrap finishes the PATCH still stopped, the peers are redeployed, and a
// peer that stays stopped on the new compose counts as done. A CVM that was
// running and ends stopped is still a failure.
func TestMembersModeComposeUpdateKeepsStoppedCVMsStopped(t *testing.T) {
	for _, tc := range []struct {
		name       string
		peerBefore string
		wantErr    bool
	}{
		{"stopped peer stays stopped", "stopped", false},
		{"running peer ends stopped", "running", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var writes []string
			patched, redeployed := false, false
			bootPolls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				path := strings.TrimPrefix(r.URL.Path, "/api/v1")
				if r.Method != http.MethodGet {
					writes = append(writes, r.Method+" "+path)
				}
				switch {
				case r.Method == http.MethodPatch && path == "/cvms/vm-boot":
					patched = true
					writeJSON(t, w, http.StatusAccepted, `{"status":"in_progress","correlation_id":"c1"}`)
				case r.Method == http.MethodGet && path == "/cvms/vm-boot":
					bootPolls++
					if bootPolls == 1 {
						writeJSON(t, w, http.StatusOK, `{"vm_uuid":"vm-boot","compose_hash":"newhash","status":"updating","in_progress":true}`)
						return
					}
					writeJSON(t, w, http.StatusOK, `{"vm_uuid":"vm-boot","compose_hash":"newhash","status":"stopped"}`)
				case r.Method == http.MethodGet && path == "/apps/test/revisions":
					writeJSON(t, w, http.StatusOK, `{"revisions":[{"revision_id":"rev_9","compose_hash":"newhash"}],"total":1,"page":1,"page_size":50,"total_pages":1}`)
				case r.Method == http.MethodPost && path == "/apps/test/revisions/rev_9/redeploy":
					redeployed = true
					writeJSON(t, w, http.StatusAccepted, `{"message":"ok"}`)
				case r.Method == http.MethodGet && path == "/apps/test/cvms":
					switch {
					case !patched:
						writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-boot","compose_hash":"old","status":"stopped"},
							{"vm_uuid":"vm-peer","compose_hash":"old","status":"`+tc.peerBefore+`"}]`)
					case redeployed:
						writeJSON(t, w, http.StatusOK, `[{"vm_uuid":"vm-boot","compose_hash":"newhash","status":"stopped"},
							{"vm_uuid":"vm-peer","compose_hash":"newhash","status":"stopped"}]`)
					default:
						t.Errorf("app CVMs read between PATCH and redeploy")
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			r := &appResource{client: newTestPhalaClient(t, srv.URL+"/api/v1")}
			diags := r.applyMembersModeUpdate(context.Background(), applyMembersModeArgs{
				appID:       "app_test",
				bootstrapID: "vm-boot",
				vmUUIDs:     []string{"vm-boot", "vm-peer"},
				plan:        appResourceModel{WaitTimeoutSecond: types.Int64Value(30)},
				composeReq:  &phala.PatchCVMRequest{Compose: &phala.ComposePatch{SwapSize: phala.PatchValue("4G")}},
			})
			if diags.HasError() != tc.wantErr {
				t.Fatalf("error = %v, want error %v", diags, tc.wantErr)
			}
			want := []string{"PATCH /cvms/vm-boot", "POST /apps/test/revisions/rev_9/redeploy"}
			if strings.Join(writes, ",") != strings.Join(want, ",") {
				t.Fatalf("writes = %v, want %v (no start)", writes, want)
			}
		})
	}
}
