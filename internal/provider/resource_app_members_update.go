package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// This file implements the slot-preserving update path for phala_app
// resources that are in members (MIG) mode. Two complementary mechanisms,
// determined by what the user changed:
//
//  1. Compose-body changes (docker_compose, pre_launch_script, public_*,
//     gateway_enabled, secure_time, allowed_envs / env_keys) need a new
//     revision because the compose hash is part of the cloud's app
//     identity. We provision a new revision against the bootstrap CVM,
//     resolve its revision_id from the app's revisions list, then call
//     POST /apps/{id}/revisions/{rev}/redeploy with vm_uuids=[every CVM].
//     The backend (see teehouse/cvms/actions/activate_revision.py) locks
//     each CVM row, sets compose_hash in place, and enqueues the per-CVM
//     update task — vm_uuid and name are preserved.
//
//  2. Per-CVM mutable fields the cloud doesn't (yet) expose at the app
//     level — env values via PATCH /cvms/{uuid}/envs, image via
//     /os-image, size/disk via /resources — get a simple sequential
//     fan-out over the known vm_uuids list. The fan-out is fail-fast
//     because there's no partial-rollback story here yet.

// provisionComposeRevision provisions a new compose revision against the
// bootstrap CVM and returns the new compose_hash. The cloud writes the
// revision to its history at the same time, where findRevisionIDByComposeHash
// can pick it up. The bootstrap is chosen because the provision endpoint is
// per-CVM and any CVM in the app can serve as the provision source (the
// new revision is app-scoped, not CVM-scoped).
func (r *appResource) provisionComposeRevision(
	ctx context.Context,
	bootstrapID string,
	composeReq map[string]any,
) (string, error) {
	// Convert the map[string]any compose request to the SDK's typed
	// request. JSON round-trip keeps the field naming in sync with the
	// untyped helpers in resource_shared.go without having to maintain
	// a parallel struct here.
	body, err := json.Marshal(composeReq)
	if err != nil {
		return "", fmt.Errorf("marshal compose provision request: %w", err)
	}
	sdkReq := &phala.ProvisionComposeUpdateRequest{}
	if err := json.Unmarshal(body, sdkReq); err != nil {
		return "", fmt.Errorf("convert compose provision request: %w", err)
	}
	resp, err := r.client.ProvisionCVMComposeFileUpdate(ctx, bootstrapID, sdkReq)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.ComposeHash) == "" {
		return "", fmt.Errorf("compose_file/provision returned empty compose_hash")
	}
	return resp.ComposeHash, nil
}

// findRevisionIDByComposeHash scans the app's revisions and returns the
// revision_id matching the given compose_hash. Revisions are paginated
// newest-first; the entry we just provisioned should be at or near the
// top. Pagination is followed in case the app has accumulated many
// revisions on prior updates.
func (r *appResource) findRevisionIDByComposeHash(
	ctx context.Context,
	appID string,
	composeHash string,
) (string, error) {
	target := strings.ToLower(strings.TrimSpace(composeHash))
	if target == "" {
		return "", fmt.Errorf("findRevisionIDByComposeHash: empty compose_hash")
	}
	const pageSize = 50
	for page := 1; ; page++ {
		p := page
		ps := pageSize
		listResp, err := r.client.GetAppRevisions(ctx, appIDWithoutPrefix(appID), &phala.PaginationOptions{Page: &p, PageSize: &ps})
		if err != nil {
			return "", fmt.Errorf("list app revisions: %w", err)
		}
		if listResp == nil {
			return "", fmt.Errorf("list app revisions: nil response")
		}
		for _, rev := range listResp.Revisions {
			if strings.EqualFold(strings.TrimSpace(rev.ComposeHash), target) {
				if strings.TrimSpace(rev.RevisionID) == "" {
					return "", fmt.Errorf("revision matching compose_hash %s has empty revision_id", composeHash)
				}
				return rev.RevisionID, nil
			}
		}
		if len(listResp.Revisions) == 0 || page >= listResp.TotalPages {
			return "", fmt.Errorf("no revision with compose_hash %s found (scanned %d page(s))", composeHash, page)
		}
	}
}

// redeployRevisionAcrossCVMs schedules an async redeploy of the named
// revision against every CVM in vmUUIDs. Returns when the cloud accepts
// the request (HTTP 202); each CVM transitions through "updating" back
// to "running" with the new compose_hash on its own schedule. Use
// waitForCVMsOnComposeHash to wait for completion.
//
// HTTP 465 (on-chain KMS compose-hash registration required) is converted
// to a clear error — the provider supports kms = phala only for now and
// does not auto-register compose hashes on chain.
func (r *appResource) redeployRevisionAcrossCVMs(
	ctx context.Context,
	appID string,
	revisionID string,
	vmUUIDs []string,
) error {
	if len(vmUUIDs) == 0 {
		return fmt.Errorf("redeploy: no vm_uuids to target")
	}
	req := &phala.RedeployAppRevisionRequest{VMUUIDs: vmUUIDs}
	if err := r.client.RedeployAppRevision(ctx, appIDWithoutPrefix(appID), revisionID, req); err != nil {
		if apiErr, ok := err.(*phala.APIError); ok && apiErr.StatusCode == 465 {
			return fmt.Errorf("on-chain KMS compose-hash registration required (HTTP 465); this provider supports kms = phala only for now")
		}
		return err
	}
	return nil
}

// waitForCVMReadyByID polls a single CVM until it reaches a stable
// running state (not in-progress). Used after PATCH /compose_file to
// confirm the bootstrap finished applying the new compose before we
// look up its compose_hash and fan out to peers.
func (r *appResource) waitForCVMReadyByID(
	ctx context.Context,
	cvmID string,
	deadline time.Time,
) error {
	for {
		cvm, err := r.fetchCVM(ctx, cvmID)
		if err != nil && !isRetryable(err) && !isNotFound(err) {
			return err
		}
		if cvm != nil {
			if strings.EqualFold(strings.TrimSpace(cvm.Status), "running") && !cvmInfoInProgress(cvm) {
				return nil
			}
			if stablePowerState(cvm.Status) == "stopped" && !cvmInfoInProgress(cvm) {
				return fmt.Errorf("CVM %s entered terminal stopped state: %s", cvmID, describeReplicaState(cvm))
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for CVM %s to become running", cvmID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval(3 * time.Second)):
		}
	}
}

// waitForCVMsOnComposeHash polls the app's CVM list until every CVM
// reports `compose_hash == expectedHash` AND `status == running` AND
// not in-progress. Returns nil on success, an error on timeout or an
// unrecoverable stoppage.
func (r *appResource) waitForCVMsOnComposeHash(
	ctx context.Context,
	appID string,
	expectedHash string,
	deadline time.Time,
) error {
	want := strings.ToLower(strings.TrimSpace(expectedHash))
	for {
		cvms, err := r.fetchAppCVMs(ctx, appID)
		if err != nil && !isRetryable(err) && !isNotFound(err) {
			return err
		}
		if len(cvms) > 0 {
			settled := true
			for i := range cvms {
				c := &cvms[i]
				haveHash := ""
				if c.ComposeHash != nil {
					haveHash = strings.ToLower(strings.TrimSpace(*c.ComposeHash))
				}
				if haveHash != want {
					settled = false
					break
				}
				if !strings.EqualFold(strings.TrimSpace(c.Status), "running") || cvmInfoInProgress(c) {
					settled = false
					break
				}
			}
			if settled {
				return nil
			}
			if failed, summary := stoppedReplicaFailure(cvms); failed {
				return fmt.Errorf("redeploy failed: %s", summary)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for all CVMs in app %q to reach compose_hash %s", appID, expectedHash)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval(3 * time.Second)):
		}
	}
}

// ---------------------------------------------------------------------------
// Per-CVM fan-out helpers
// ---------------------------------------------------------------------------
//
// Sequential, fail-fast. We don't parallelize because the cloud's update
// tasks may serialize on the underlying teepod anyway, and partial-apply
// recovery in Terraform is much cleaner when a single PATCH failed at a
// known position than when several are in flight.

// patchEnvAcrossCVMs pushes the same envReq to every CVM by vm_uuid.
// The app-rooted KMS public key is shared across all CVMs in one app, so
// the same encrypted_env bytes are accepted by every CVM.
func (r *appResource) patchEnvAcrossCVMs(
	ctx context.Context,
	vmUUIDs []string,
	envReq *phala.UpdateEnvsRequest,
) error {
	if envReq == nil {
		return fmt.Errorf("patchEnvAcrossCVMs: nil envReq")
	}
	for _, vmUUID := range vmUUIDs {
		id := strings.TrimSpace(vmUUID)
		if id == "" {
			continue
		}
		if _, err := r.client.UpdateCVMEnvs(ctx, id, envReq); err != nil {
			if apiErr, ok := err.(*phala.APIError); ok && apiErr.StatusCode == 465 {
				return fmt.Errorf(
					"encrypted env update on CVM %s requires on-chain compose-hash registration (HTTP 465); this provider supports kms = phala only for now",
					id,
				)
			}
			return fmt.Errorf("patch envs on CVM %s: %w", id, err)
		}
	}
	return nil
}

// patchOSImageAcrossCVMs switches every CVM in vmUUIDs to imageName.
// The cloud has no app-level /os-image endpoint; this is the manual
// fan-out.
func (r *appResource) patchOSImageAcrossCVMs(
	ctx context.Context,
	vmUUIDs []string,
	imageName string,
) error {
	req := &phala.UpdateOSImageRequest{OSImageName: imageName}
	for _, vmUUID := range vmUUIDs {
		id := strings.TrimSpace(vmUUID)
		if id == "" {
			continue
		}
		if err := r.client.UpdateOSImage(ctx, id, req); err != nil {
			return fmt.Errorf("patch os-image on CVM %s: %w", id, err)
		}
	}
	return nil
}

// patchResourcesAcrossCVMs applies the same instance_type / disk_size
// change to every CVM in vmUUIDs. Manual fan-out for the same reason as
// patchOSImageAcrossCVMs.
func (r *appResource) patchResourcesAcrossCVMs(
	ctx context.Context,
	vmUUIDs []string,
	resourceReq *phala.UpdateResourcesRequest,
) error {
	if resourceReq == nil {
		return fmt.Errorf("patchResourcesAcrossCVMs: nil resourceReq")
	}
	for _, vmUUID := range vmUUIDs {
		id := strings.TrimSpace(vmUUID)
		if id == "" {
			continue
		}
		if err := r.client.UpdateCVMResources(ctx, id, resourceReq); err != nil {
			return fmt.Errorf("patch resources on CVM %s: %w", id, err)
		}
	}
	return nil
}

// collectVMUUIDs extracts the vm_uuid of every CVM in the slice, skipping
// empties. Order matches the input.
func collectVMUUIDs(cvms []phala.CVMInfo) []string {
	out := make([]string, 0, len(cvms))
	for i := range cvms {
		if v := cvmInfoVMUUID(&cvms[i]); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Update orchestration
// ---------------------------------------------------------------------------

// Shared arguments for the two Update branches. Kept as a struct so the
// signature doesn't drift between callers as new optional inputs land.
type applyMembersModeArgs struct {
	appID           string
	bootstrapID     string
	vmUUIDs         []string
	plan            appResourceModel
	state           appResourceModel
	envCfg          *envConfig
	envChanged      bool
	imageChanged    bool
	diskSizeChanged bool
	composeEnvKeys  []string
	composeHasKeys  bool
	updateEnvKeys   bool
	settingsChanged bool
}

type applySingleCVMArgs struct {
	bootstrapID     string
	plan            appResourceModel
	state           appResourceModel
	envCfg          *envConfig
	envChanged      bool
	imageChanged    bool
	diskSizeChanged bool
	composeEnvKeys  []string
	composeHasKeys  bool
	updateEnvKeys   bool
	settingsChanged bool
}

// applyMembersModeUpdate is the slot-preserving update path for apps in
// members (MIG) mode. Compose-body changes flow through one
// provision+redeploy revision so every slot lands on the new compose
// atomically; env value / image / resource changes fan out per-CVM.
func (r *appResource) applyMembersModeUpdate(ctx context.Context, a applyMembersModeArgs) diag.Diagnostics {
	var diags diag.Diagnostics
	if len(a.vmUUIDs) == 0 {
		diags.AddError("No CVMs to update", "members mode update path requires at least one CVM under the app.")
		return diags
	}

	composeChanged := a.settingsChanged ||
		!a.plan.DockerCompose.Equal(a.state.DockerCompose) ||
		!a.plan.PreLaunchScript.Equal(a.state.PreLaunchScript) ||
		a.updateEnvKeys

	if composeChanged {
		composeReq := buildComposeFileUpdateRequest(composeFileFields{
			Name:            a.plan.Name.ValueString(),
			DockerCompose:   a.plan.DockerCompose.ValueString(),
			PreLaunchScript: a.plan.PreLaunchScript,
			PublicLogs:      a.plan.PublicLogs,
			PublicSysinfo:   a.plan.PublicSysinfo,
			PublicTCBInfo:   a.plan.PublicTCBInfo,
			GatewayEnabled:  a.plan.GatewayEnabled,
			SecureTime:      a.plan.SecureTime,
			StorageFS:       a.plan.StorageFS,
			EnvKeys:         a.composeEnvKeys,
			HasEnvKeys:      a.composeHasKeys,
		}, a.updateEnvKeys)

		// The cloud's `compose_file/provision` endpoint only caches a
		// compose_hash — it does NOT create a revision row in the app's
		// revision history. The revision row is created when the compose
		// actually gets applied to a CVM (via PATCH /compose_file). So
		// the slot-preserving rollout is: apply to the bootstrap first
		// (this creates the revision and updates the bootstrap CVM), then
		// redeploy the same revision to every other slot.
		if err := provisionAndApplyComposeFileUpdate(ctx, r.client, a.bootstrapID, composeReq); err != nil {
			if apiErr, ok := err.(*phala.APIError); ok && apiErr.StatusCode == 465 {
				diags.AddError(
					"On-chain KMS not supported in members mode",
					"PATCH /compose_file returned HTTP 465 (compose hash registration required); this provider supports kms = phala only for now.",
				)
				return diags
			}
			diags.AddError("Failed to apply new compose to bootstrap CVM", err.Error())
			return diags
		}

		// Wait for the bootstrap to land on the new compose_hash. Its
		// CVM row carries the hash, so once it's `running` and not
		// in_progress we can read it back to resolve the revision_id.
		deadline := time.Now().Add(waitTimeout(a.plan.WaitTimeoutSecond))
		if err := r.waitForCVMReadyByID(ctx, a.bootstrapID, deadline); err != nil {
			diags.AddError("Bootstrap CVM did not settle after compose apply", err.Error())
			return diags
		}
		bootstrap, err := r.fetchCVM(ctx, a.bootstrapID)
		if err != nil {
			diags.AddError("Failed to read bootstrap CVM after compose apply", err.Error())
			return diags
		}
		newComposeHash := ""
		if bootstrap.ComposeHash != nil {
			newComposeHash = strings.TrimSpace(*bootstrap.ComposeHash)
		}
		if newComposeHash == "" {
			diags.AddError(
				"Bootstrap CVM is missing compose_hash after apply",
				"Cannot resolve the new revision_id without compose_hash on the bootstrap CVM; cloud response did not include it.",
			)
			return diags
		}
		revID, err := r.findRevisionIDByComposeHash(ctx, a.appID, newComposeHash)
		if err != nil {
			diags.AddError("Failed to resolve revision_id for new compose", err.Error())
			return diags
		}

		// Fan out the same revision to every other CVM. The bootstrap
		// already has it from the PATCH above — skip it explicitly.
		others := make([]string, 0, len(a.vmUUIDs))
		bootstrapUUID := cvmInfoVMUUID(bootstrap)
		for _, vmUUID := range a.vmUUIDs {
			if strings.TrimSpace(vmUUID) == bootstrapUUID {
				continue
			}
			others = append(others, vmUUID)
		}
		if len(others) > 0 {
			if err := r.redeployRevisionAcrossCVMs(ctx, a.appID, revID, others); err != nil {
				diags.AddError("Failed to schedule redeploy across slot CVMs", err.Error())
				return diags
			}
			if err := r.waitForCVMsOnComposeHash(ctx, a.appID, newComposeHash, deadline); err != nil {
				diags.AddError("Slot CVMs did not complete redeploy in time", err.Error())
				return diags
			}
		}
	}

	if a.envChanged {
		envPayload, err := a.envCfg.buildEnvUpdateReq(a.plan.EnvKeys)
		if err != nil {
			diags.AddError("Missing encrypted_env", err.Error())
			return diags
		}
		envReq := buildUpdateEnvsRequest(envPayload)
		if err := r.patchEnvAcrossCVMs(ctx, a.vmUUIDs, envReq); err != nil {
			diags.AddError("Failed to update app encrypted env", err.Error())
			return diags
		}
	}

	if a.imageChanged {
		if a.plan.Image.IsNull() || a.plan.Image.IsUnknown() || strings.TrimSpace(a.plan.Image.ValueString()) == "" {
			diags.AddError("Invalid image update", "image must be set to a target OS image name when updating.")
			return diags
		}
		if err := r.patchOSImageAcrossCVMs(ctx, a.vmUUIDs, a.plan.Image.ValueString()); err != nil {
			diags.AddError("Failed to update app OS image", err.Error())
			return diags
		}
	}

	if !a.plan.Size.Equal(a.state.Size) || a.diskSizeChanged {
		resReq := &phala.UpdateResourcesRequest{AllowRestart: boolPtr(true)}
		if !a.plan.Size.Equal(a.state.Size) {
			s := a.plan.Size.ValueString()
			resReq.InstanceType = &s
		}
		if a.diskSizeChanged {
			ds := int(a.plan.DiskSize.ValueInt64())
			resReq.DiskSize = &ds
		}
		if err := r.patchResourcesAcrossCVMs(ctx, a.vmUUIDs, resReq); err != nil {
			diags.AddError("Failed to update app resources", err.Error())
			return diags
		}
	}

	return diags
}

// applySingleCVMUpdate is the legacy single-CVM update path: each changed
// field is PATCH'd directly against the bootstrap (and only) CVM. This is
// what runs when phala_app.members is unset.
func (r *appResource) applySingleCVMUpdate(ctx context.Context, a applySingleCVMArgs) diag.Diagnostics {
	var diags diag.Diagnostics

	if !a.plan.Size.Equal(a.state.Size) || a.diskSizeChanged {
		resReq := &phala.UpdateResourcesRequest{AllowRestart: boolPtr(true)}
		if !a.plan.Size.Equal(a.state.Size) {
			s := a.plan.Size.ValueString()
			resReq.InstanceType = &s
		}
		if a.diskSizeChanged {
			ds := int(a.plan.DiskSize.ValueInt64())
			resReq.DiskSize = &ds
		}
		if err := r.client.UpdateCVMResources(ctx, a.bootstrapID, resReq); err != nil {
			diags.AddError("Failed to update app resources", err.Error())
			return diags
		}
	}

	if a.settingsChanged {
		composeReq := buildComposeFileUpdateRequest(composeFileFields{
			Name:            a.plan.Name.ValueString(),
			DockerCompose:   a.plan.DockerCompose.ValueString(),
			PreLaunchScript: a.plan.PreLaunchScript,
			PublicLogs:      a.plan.PublicLogs,
			PublicSysinfo:   a.plan.PublicSysinfo,
			PublicTCBInfo:   a.plan.PublicTCBInfo,
			GatewayEnabled:  a.plan.GatewayEnabled,
			SecureTime:      a.plan.SecureTime,
			StorageFS:       a.plan.StorageFS,
			EnvKeys:         a.composeEnvKeys,
			HasEnvKeys:      a.composeHasKeys,
		}, a.updateEnvKeys)
		if err := provisionAndApplyComposeFileUpdate(ctx, r.client, a.bootstrapID, composeReq); err != nil {
			diags.AddError("Failed to update app compose settings", err.Error())
			return diags
		}
	}

	if a.imageChanged {
		if a.plan.Image.IsNull() || a.plan.Image.IsUnknown() || strings.TrimSpace(a.plan.Image.ValueString()) == "" {
			diags.AddError("Invalid image update", "image must be set to a target OS image name when updating.")
			return diags
		}
		if err := r.client.UpdateOSImage(ctx, a.bootstrapID, &phala.UpdateOSImageRequest{OSImageName: a.plan.Image.ValueString()}); err != nil {
			diags.AddError("Failed to update app OS image", err.Error())
			return diags
		}
	}

	if !a.plan.DockerCompose.Equal(a.state.DockerCompose) {
		if _, err := r.client.UpdateDockerCompose(ctx, a.bootstrapID, a.plan.DockerCompose.ValueString(), nil); err != nil {
			diags.AddError("Failed to update app docker compose", err.Error())
			return diags
		}
	}

	if !a.plan.PreLaunchScript.Equal(a.state.PreLaunchScript) {
		script := ""
		if !a.plan.PreLaunchScript.IsNull() && !a.plan.PreLaunchScript.IsUnknown() {
			script = a.plan.PreLaunchScript.ValueString()
		}
		if _, err := r.client.UpdatePreLaunchScript(ctx, a.bootstrapID, script, nil); err != nil {
			diags.AddError("Failed to update app pre-launch script", err.Error())
			return diags
		}
	}

	if a.envChanged {
		envPayload, err := a.envCfg.buildEnvUpdateReq(a.plan.EnvKeys)
		if err != nil {
			diags.AddError("Missing encrypted_env", err.Error())
			return diags
		}
		envReq := buildUpdateEnvsRequest(envPayload)
		if _, err := r.client.UpdateCVMEnvs(ctx, a.bootstrapID, envReq); err != nil {
			if apiErr, ok := err.(*phala.APIError); ok && apiErr.StatusCode == 465 {
				diags.AddError(
					"Encrypted env update requires on-chain verification",
					"API returned HTTP 465 (compose hash registration required). Register compose_hash on-chain and retry with env_compose_hash and env_transaction_hash.",
				)
				return diags
			}
			diags.AddError("Failed to update app encrypted env", err.Error())
			return diags
		}
	}

	return diags
}
