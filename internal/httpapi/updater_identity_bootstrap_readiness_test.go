package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBootstrapCreateRequiresSettledUpdaterIdentityAndFreshTokenHeartbeat(t *testing.T) {
	t.Run("runtime token rotation", func(t *testing.T) {
		fixture := newBootstrapIdentityReadinessFixture(t)
		before, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}

		rotatedRuntimeToken := fixture.rotateRuntimeIdentity(t)
		registerPayload, err := json.Marshal(store.ServiceRegistration{
			ServiceID:   fixture.serviceID,
			ServiceType: "update_agent",
			ServiceName: "Updater",
			Version:     "v1.0.1",
		})
		if err != nil {
			t.Fatal(err)
		}
		registerRequest := httptest.NewRequest(
			http.MethodPost,
			"/services/register",
			bytes.NewReader(registerPayload),
		)
		registerRequest.Header.Set("Authorization", "Bearer "+rotatedRuntimeToken)
		register := httptest.NewRecorder()
		fixture.server.ServeHTTP(register, registerRequest)
		if register.Code != http.StatusAccepted {
			t.Fatalf("post-rotation service registration status=%d body=%s", register.Code, register.Body.String())
		}
		after, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if after.NodeTokenRotatedAt == nil ||
			before.LastHeartbeatAt == nil ||
			after.NodeTokenRotatedAt.Before(*before.LastHeartbeatAt) {
			t.Fatalf("rotation timestamps do not establish a new identity generation: before=%#v after=%#v", before, after)
		}

		create := fixture.createBootstrapRequest(t, "post-runtime-rotation")
		if create.Code != http.StatusConflict ||
			!strings.Contains(create.Body.String(), `"code":"updater_offline"`) {
			t.Fatalf(
				"pre-rotation heartbeat authorized bootstrap: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}

		fixture.heartbeatAfterRotation(t, rotatedRuntimeToken)
		create = fixture.createBootstrapRequest(t, "post-runtime-rotation")
		if create.Code != http.StatusAccepted {
			t.Fatalf(
				"new-token heartbeat did not restore bootstrap readiness: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}
	})

	t.Run("staged configuration activation", func(t *testing.T) {
		fixture := newBootstrapIdentityReadinessFixture(t)
		rawConfigureToken := "configure-bootstrap-readiness"
		if _, err := fixture.auth.SetServiceConfigureToken(
			t.Context(),
			fixture.serviceID,
			security.HashToken(rawConfigureToken),
			time.Now().UTC().Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		stageBody, err := json.Marshal(map[string]any{
			"nodeId":          fixture.serviceID,
			"configureToken":  rawConfigureToken,
			"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
			"agentUid":        updaterIdentityFixtureAgentUID,
			"agentGid":        updaterIdentityFixtureAgentGID,
		})
		if err != nil {
			t.Fatal(err)
		}
		stage := fixture.publicRequest(
			t,
			http.MethodPost,
			"/services/host-agent/runtime-identity/stage",
			string(stageBody),
		)
		if stage.Code != http.StatusOK {
			t.Fatalf("configuration stage status=%d body=%s", stage.Code, stage.Body.String())
		}
		var staged updateradapter.UpdaterStagedConfiguration
		if err := json.NewDecoder(stage.Body).Decode(&staged); err != nil {
			t.Fatal(err)
		}
		if staged.ConfigurationID == "" ||
			staged.ActivationToken == "" ||
			staged.Config.RuntimeToken == "" ||
			staged.LocalExecutorPolicy == nil {
			t.Fatalf("configuration stage response incomplete: %#v", staged)
		}

		create := fixture.createBootstrapRequest(t, "pending-configuration")
		if create.Code != http.StatusConflict ||
			!strings.Contains(create.Body.String(), `"code":"updater_configuration_pending"`) {
			t.Fatalf(
				"pending updater configuration authorized bootstrap: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}

		activateBody, err := json.Marshal(hostAgentConfigureActivationPayload(
			staged,
			updaterIdentityFixtureAgentUID,
			updaterIdentityFixtureAgentGID,
			*staged.LocalExecutorPolicy,
		))
		if err != nil {
			t.Fatal(err)
		}
		activate := fixture.publicRequest(
			t,
			http.MethodPost,
			"/services/host-agent/runtime-identity/activate",
			string(activateBody),
		)
		if activate.Code != http.StatusOK {
			t.Fatalf("configuration activation status=%d body=%s", activate.Code, activate.Body.String())
		}

		create = fixture.createBootstrapRequest(t, "pending-configuration")
		if create.Code != http.StatusConflict ||
			!strings.Contains(create.Body.String(), `"code":"updater_offline"`) {
			t.Fatalf(
				"pre-activation heartbeat authorized bootstrap: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}

		fixture.heartbeatAfterRotation(t, staged.Config.RuntimeToken)
		create = fixture.createBootstrapRequest(t, "pending-configuration")
		if create.Code != http.StatusAccepted {
			t.Fatalf(
				"activated-token heartbeat did not restore bootstrap readiness: status=%d body=%s",
				create.Code,
				create.Body.String(),
			)
		}
	})
}

func TestBootstrapCreateRequiresCurrentRuntimeReportedEncryptionIdentity(t *testing.T) {
	fixture := newBootstrapIdentityReadinessFixture(t)
	rotatedRuntimeToken := fixture.rotateRuntimeIdentity(t)
	registerPayload, err := json.Marshal(store.ServiceRegistration{
		ServiceID:   fixture.serviceID,
		ServiceType: "update_agent",
		ServiceName: "Updater",
		Version:     "v1.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/register",
		bytes.NewReader(registerPayload),
	)
	registerRequest.Header.Set("Authorization", "Bearer "+rotatedRuntimeToken)
	register := httptest.NewRecorder()
	fixture.server.ServeHTTP(register, registerRequest)
	if register.Code != http.StatusAccepted {
		t.Fatalf("post-rotation registration status=%d body=%s", register.Code, register.Body.String())
	}

	reportedWithoutEncryptionIdentity := make(map[string]any, len(fixture.capabilities))
	for key, value := range fixture.capabilities {
		reportedWithoutEncryptionIdentity[key] = value
	}
	delete(reportedWithoutEncryptionIdentity, "bootstrap_encryption_public_key")
	delete(reportedWithoutEncryptionIdentity, "bootstrap_encryption_key_fingerprint")
	delete(reportedWithoutEncryptionIdentity, "bootstrap_encryption_public_key_fingerprint")
	heartbeatPayload, err := json.Marshal(store.ServiceHeartbeat{
		ServiceID:    fixture.serviceID,
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: reportedWithoutEncryptionIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	heartbeatRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/heartbeat",
		bytes.NewReader(heartbeatPayload),
	)
	heartbeatRequest.Header.Set("Authorization", "Bearer "+rotatedRuntimeToken)
	heartbeat := httptest.NewRecorder()
	fixture.server.ServeHTTP(heartbeat, heartbeatRequest)
	if heartbeat.Code != http.StatusAccepted {
		t.Fatalf("post-rotation heartbeat status=%d body=%s", heartbeat.Code, heartbeat.Body.String())
	}
	service, err := fixture.auth.GetService(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if capabilityString(service.Capabilities["bootstrap_encryption_public_key"]) == "" ||
		capabilityString(service.ReportedCapabilities["bootstrap_encryption_public_key"]) != "" {
		t.Fatalf("test fixture did not retain only the stale configured key: %#v", service)
	}

	create := fixture.createBootstrapRequest(t, "missing-current-runtime-encryption-identity")
	if create.Code != http.StatusConflict ||
		!strings.Contains(create.Body.String(), `"code":"bootstrap_encryption_key_unavailable"`) {
		t.Fatalf("stale configured bootstrap key authorized create: status=%d body=%s", create.Code, create.Body.String())
	}
	jobs, err := fixture.server.updateHostBootstrapJobs.List(fixture.serviceID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("missing current runtime key queued bootstrap jobs=%#v err=%v", jobs, err)
	}
}

func TestSystemUpdateAgentAvailabilityAcceptsSamePrecisionHeartbeatAfterAtomicReset(t *testing.T) {
	t.Setenv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", "3m")
	now := time.Now().UTC()
	rotatedAt := now.Add(-time.Minute)
	if systemUpdateAgentAvailable(store.RegisteredService{
		Status:             "online",
		NodeTokenRotatedAt: &rotatedAt,
	}, now) {
		t.Fatal("updater without a post-reset heartbeat was treated as current")
	}
	heartbeatAt := rotatedAt
	if !systemUpdateAgentAvailable(store.RegisteredService{
		Status:             "online",
		LastHeartbeatAt:    &heartbeatAt,
		NodeTokenRotatedAt: &rotatedAt,
	}, now) {
		t.Fatal("same-precision post-reset heartbeat was treated as stale")
	}
}

func TestBootstrapCreateRejectsCustomManagedHostUser(t *testing.T) {
	fixture := newBootstrapIdentityReadinessFixture(t)
	policies, ok := fixture.server.updaterPolicies.(*store.MemoryUpdaterPolicyStore)
	if !ok {
		t.Fatalf("unexpected updater policy store %T", fixture.server.updaterPolicies)
	}
	current, err := policies.GetUpdaterPolicy(t.Context(), fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := current
	replacement.Hosts = append([]store.UpdaterPolicyHost(nil), current.Hosts...)
	replacement.Hosts[0].User = "custom-deploy-user"
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		store.NewMemorySystemUpdateStore(),
		fixture.serviceID,
		current.Revision,
		0,
		replacement,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.policyRevision = saved.Revision
	fixture.capabilities["policy_revision"] = saved.ProjectionRevision
	fixture.capabilities["policy_desired_revision"] = saved.ProjectionRevision
	if _, err := fixture.auth.Heartbeat(t.Context(), fixture.runtimeToken, store.ServiceHeartbeat{
		ServiceID:    fixture.serviceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: fixture.capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	result := fixture.createBootstrapRequest(t, "custom-managed-user")
	if result.Code != http.StatusConflict ||
		!strings.Contains(result.Body.String(), `"code":"unsupported_bootstrap_profile"`) {
		t.Fatalf("custom managed user bootstrap status=%d body=%s", result.Code, result.Body.String())
	}
	jobs, err := fixture.server.updateHostBootstrapJobs.List(fixture.serviceID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("custom managed user queued bootstrap jobs=%#v err=%v", jobs, err)
	}
}
