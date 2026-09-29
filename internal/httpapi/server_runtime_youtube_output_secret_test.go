package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type outputSecretStores struct {
	auth     store.AuthStore
	services store.ServiceRegistryStore
	audit    store.AuditStore
	streams  store.StreamStore
	profiles store.ProfileStore
	secrets  store.SecretStore
	leases   store.RuntimeSecretLeaseStore
}

type outputSecretFixture struct {
	outputSecretStores
	handler             *Server
	cookie              *http.Cookie
	csrf                string
	stream, otherStream store.Stream
	output              store.Profile
	secret, value       string
	token, otherToken   store.ServiceToken
	serviceID           string
}

func TestSelectedYouTubeOutputSecretMemory(t *testing.T) {
	runSelectedYouTubeOutputSecretCases(t, func(t *testing.T) outputSecretStores {
		auth := store.NewMemoryAuthStore()
		return outputSecretStores{auth, auth, auth, store.NewMemoryStreamStore(), store.NewMemoryProfileStore(), store.NewMemorySecretStore(), store.NewMemoryRuntimeSecretLeaseStore()}
	})
}

func newOutputSecretFixture(t *testing.T, st outputSecretStores) *outputSecretFixture {
	t.Helper()
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	t.Setenv("AUTOSTREAM_ENV", "development")
	f := &outputSecretFixture{outputSecretStores: st, serviceID: "selected-encoder"}
	password := outputSecretRandom(t)
	if _, err := st.auth.CreateFirstAdmin(t.Context(), "output-admin", password, security.DefaultPermissions); err != nil {
		t.Fatal(err)
	}
	f.handler = newOutputSecretServer(st)
	f.cookie, f.csrf = loginForTest(t, f.handler, "output-admin", password)
	f.value = outputSecretRandom(t)
	f.output = f.createOutput(t, "selected", f.value)
	f.secret = configString(f.output.Config, "stream_key_secret_name")
	if f.secret == "" {
		t.Fatal("normal HTTP Output did not persist its secret reference")
	}
	if _, ok := f.output.Config["service_id"]; ok {
		t.Fatal("positive fixture has service binding")
	}
	if _, ok := f.output.Config["service_ids"]; ok {
		t.Fatal("positive fixture has service bindings")
	}
	f.stream = f.createStream(t, "selected stream", f.output.ID)
	f.otherStream = f.createStream(t, "unrelated stream", "")
	f.token = f.register(t, f.serviceID, "encoder_recorder")
	f.otherToken = f.register(t, "other-encoder", "encoder_recorder")
	assignServiceForTest(t, f.handler, f.cookie, f.csrf, f.serviceID, f.stream.ID)
	return f
}

func newOutputSecretServer(st outputSecretStores) *Server {
	return NewServer(st.streams, WithAuthStore(st.auth), WithServiceRegistryStore(st.services), WithAuditStore(st.audit), WithProfileStore(st.profiles), WithSecretStore(st.secrets), WithRuntimeSecretLeaseStore(st.leases))
}

func outputSecretRandom(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func (f *outputSecretFixture) request(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.AddCookie(f.cookie)
	req.Header.Set("X-CSRF-Token", f.csrf)
	res := httptest.NewRecorder()
	f.handler.ServeHTTP(res, req)
	return res
}

func outputSecretStatus(t *testing.T, res *httptest.ResponseRecorder, want int) {
	t.Helper()
	if res.Code != want {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &body)
		t.Fatalf("status=%d want=%d code=%s", res.Code, want, body.Code)
	}
}

func (f *outputSecretFixture) createOutput(t *testing.T, name, value string) store.Profile {
	t.Helper()
	res := f.request(t, http.MethodPost, "/youtube/outputs", map[string]any{"name": name, "mode": "stream_key", "stream_key": value, "watch_url": "https://www.youtube.com/watch?v=output-test"})
	outputSecretStatus(t, res, http.StatusCreated)
	if bytes.Contains(res.Body.Bytes(), []byte(value)) {
		t.Fatal("Output response exposed secret")
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	profile, err := f.profiles.GetProfile(t.Context(), store.ProfileYouTubeOutput, out.ID)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func (f *outputSecretFixture) createStream(t *testing.T, name, outputID string) store.Stream {
	t.Helper()
	res := f.request(t, http.MethodPost, "/streams", map[string]any{"name": name, "youtube_output_id": outputID, "visual_settings": map[string]any{"background_mode": "default", "header_title_mode": "custom", "header_title_value": "Output authorization regression", "cover_source": "none", "cover_start_active": false}})
	outputSecretStatus(t, res, http.StatusCreated)
	var stream store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &stream); err != nil {
		t.Fatal(err)
	}
	return stream
}

func (f *outputSecretFixture) register(t *testing.T, id, kind string) store.ServiceToken {
	t.Helper()
	token, err := f.services.CreateServiceToken(t.Context(), kind, []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: id, PublicURL: "https://" + id + ".example.com", Version: "v2.0.0", Capabilities: map[string]any{"output_relay_mode": "direct"}}
	if _, err = f.services.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(registration)
	req := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	f.handler.ServeHTTP(res, req)
	outputSecretStatus(t, res, http.StatusAccepted)
	return token
}

func (f *outputSecretFixture) resolve(t *testing.T, token store.ServiceToken, serviceID, streamID, secret string, secure bool) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(serviceRuntimeSecretResolveRequest{ServiceID: serviceID, StreamID: streamID, SecretName: secret})
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", bytes.NewReader(data))
	req.RemoteAddr = "198.51.100.10:1234"
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	if secure {
		req.TLS = &tls.ConnectionState{}
	}
	res := httptest.NewRecorder()
	f.handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK && bytes.Contains(res.Body.Bytes(), []byte(f.value)) {
		t.Fatal("denial exposed secret")
	}
	return res
}

func (f *outputSecretFixture) updateConfig(t *testing.T, change func(map[string]any)) {
	t.Helper()
	p, err := f.profiles.GetProfile(t.Context(), store.ProfileYouTubeOutput, f.output.ID)
	if err != nil {
		t.Fatal(err)
	}
	change(p.Config)
	if _, err = f.profiles.UpdateProfile(t.Context(), p.Kind, p.ID, p.Name, p.Config); err != nil {
		t.Fatal(err)
	}
}

func runSelectedYouTubeOutputSecretCases(t *testing.T, makeStores func(*testing.T) outputSecretStores) {
	t.Run("AUTH08_selected_lookup_without_profile_enumeration", func(t *testing.T) {
		f := newOutputSecretFixture(t, makeStores(t))
		st := f.outputSecretStores
		st.profiles = outputSecretLookupProfiles{ProfileStore: f.profiles, listErr: errors.New("unrelated list must not be needed")}
		f.handler = newOutputSecretServer(st)
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true), 200)
	})
	for _, stage := range []string{"stream", "output", "assignment"} {
		t.Run("AUTH08_store_failure_"+stage, func(t *testing.T) {
			f := newOutputSecretFixture(t, makeStores(t))
			failure := errors.New("injected store failure")
			st := f.outputSecretStores
			switch stage {
			case "stream":
				st.streams = outputSecretLookupStreams{StreamStore: f.streams, err: failure}
			case "output":
				st.profiles = outputSecretLookupProfiles{ProfileStore: f.profiles, getErr: failure}
			case "assignment":
				st.services = outputSecretLookupAssignments{ServiceRegistryStore: f.services, err: failure}
			}
			// Publish a separately configured server; the original may still have
			// an audit notification goroutine using its immutable store references.
			f.handler = newOutputSecretServer(st)
			outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true), 500)
		})
	}
	cases := []struct {
		name   string
		change func(*testing.T, *outputSecretFixture)
		want   int
	}{
		{"AUTH01_normal_HTTP_unbound_output", nil, 200},
		{"AUTH02_other_token", func(t *testing.T, f *outputSecretFixture) { f.token = f.otherToken }, 403},
		{"AUTH02_other_node", func(t *testing.T, f *outputSecretFixture) { f.serviceID = "other-encoder" }, 403},
		{"AUTH02_wrong_service_type", func(t *testing.T, f *outputSecretFixture) {
			f.serviceID = "worker-secret"
			f.token = f.register(t, f.serviceID, "worker")
			assignServiceForTest(t, f.handler, f.cookie, f.csrf, f.serviceID, f.stream.ID)
		}, 403},
		{"AUTH02_revoked_token", func(t *testing.T, f *outputSecretFixture) {
			if err := f.services.RevokeServiceToken(t.Context(), f.token.ID); err != nil {
				t.Fatal(err)
			}
		}, 401},
		{"AUTH03_standby", func(t *testing.T, f *outputSecretFixture) {
			assignServiceWithRoleForTest(t, f.handler, f.cookie, f.csrf, f.serviceID, f.stream.ID, "standby")
		}, 403},
		{"AUTH03_unassigned", func(t *testing.T, f *outputSecretFixture) {
			if _, err := f.services.UnassignServiceFromStream(t.Context(), f.serviceID, "output-admin"); err != nil {
				t.Fatal(err)
			}
		}, 403},
		{"AUTH03_other_stream", func(t *testing.T, f *outputSecretFixture) { f.stream = f.otherStream }, 403},
		{"AUTH03_missing_stream", func(t *testing.T, f *outputSecretFixture) { f.stream.ID = "missing-stream" }, 403},
		{"AUTH04_other_output", func(t *testing.T, f *outputSecretFixture) {
			p := f.createOutput(t, "other", outputSecretRandom(t))
			f.secret = configString(p.Config, "stream_key_secret_name")
		}, 403},
		{"AUTH04_missing_output", func(t *testing.T, f *outputSecretFixture) {
			if err := f.profiles.DeleteProfile(t.Context(), store.ProfileYouTubeOutput, f.output.ID); err != nil {
				t.Fatal(err)
			}
		}, 403},
		{"AUTH04_secret_prefix_only", func(t *testing.T, f *outputSecretFixture) { f.secret += "_different" }, 403},
		{"AUTH04_non_youtube_secret", func(t *testing.T, f *outputSecretFixture) {
			f.secret = "discord_bot_token_unrelated"
			f.updateConfig(t, func(c map[string]any) { c["stream_key_secret_name"] = f.secret })
		}, 403},
		{"AUTH05_explicit_owner", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_id"] = f.serviceID })
		}, 200},
		{"AUTH05_explicit_other", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_id"] = "other-encoder" })
		}, 403},
		{"AUTH05_explicit_owner_list", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_ids"] = []any{f.serviceID} })
		}, 200},
		{"AUTH05_explicit_other_list", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_ids"] = []any{"other-encoder"} })
		}, 403},
		{"AUTH05_invalid_binding_type", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_id"] = 17 })
		}, 403},
		{"AUTH05_invalid_bindings_type", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_ids"] = f.serviceID })
		}, 403},
		{"AUTH05_empty_explicit_binding", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_id"] = "" })
		}, 403},
		{"AUTH05_null_explicit_binding", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["service_id"] = nil })
		}, 403},
		{"AUTH07_relay_static_not_newly_authorized", func(t *testing.T, f *outputSecretFixture) {
			f.updateConfig(t, func(c map[string]any) { c["mode"] = "live_api_relay_static" })
		}, 403},
		{"AUTH07_dynamic_reference_not_newly_authorized", func(t *testing.T, f *outputSecretFixture) {
			f.secret = "youtube_stream_key_runtime_unrelated"
			f.updateConfig(t, func(c map[string]any) { c["stream_key_secret_name"] = f.secret })
		}, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOutputSecretFixture(t, makeStores(t))
			if tc.change != nil {
				tc.change(t, f)
			}
			res := f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true)
			outputSecretStatus(t, res, tc.want)
			if tc.want == 200 {
				var body serviceRuntimeSecretResolveResponse
				if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Value != f.value || body.SecretName != f.secret || body.ExpiresInSec <= 0 {
					t.Fatal("wrong secret or lease result")
				}
				if res.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("secret response is cacheable")
				}
			}
			events, err := f.audit.ListAudit(t.Context(), store.AuditFilter{})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(events)
			if bytes.Contains(data, []byte(f.value)) {
				t.Fatal("audit exposed secret")
			}
		})
	}
	t.Run("AUTH06_TLS_lease_and_nonexposure", func(t *testing.T) {
		f := newOutputSecretFixture(t, makeStores(t))
		t.Setenv("AUTOSTREAM_ENV", "production")
		t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "")
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, false), 403)
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true), 200)
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true), 409)
	})
	t.Run("AUTH08_current_selection_and_unassignment", func(t *testing.T) {
		f := newOutputSecretFixture(t, makeStores(t))
		otherValue := outputSecretRandom(t)
		other := f.createOutput(t, "later-selected", otherValue)
		// A bound but unrelated profile must not confer access to the wrong Output.
		if _, err := f.profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "unrelated-bound", map[string]any{"service_id": f.serviceID, "stream_key_secret_name": f.secret}); err != nil {
			t.Fatal(err)
		}
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true), 200)
		outputSecretStatus(t, f.request(t, http.MethodPut, "/streams/"+f.stream.ID+"/settings", map[string]any{"youtube_output_id": other.ID}), 200)
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, f.secret, true), 403)
		otherSecret := configString(other.Config, "stream_key_secret_name")
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, otherSecret, true), 200)
		if _, err := f.services.UnassignServiceFromStream(t.Context(), f.serviceID, "output-admin"); err != nil {
			t.Fatal(err)
		}
		outputSecretStatus(t, f.resolve(t, f.token, f.serviceID, f.stream.ID, otherSecret, true), 403)
	})
}

// Fault injection exercises error propagation without changing the actual stores
// used for the positive and negative authorization cases.
type outputSecretLookupProfiles struct {
	store.ProfileStore
	getErr, listErr error
}

func (s outputSecretLookupProfiles) GetProfile(ctx context.Context, kind store.ProfileKind, id string) (store.Profile, error) {
	if s.getErr != nil {
		return store.Profile{}, s.getErr
	}
	return s.ProfileStore.GetProfile(ctx, kind, id)
}
func (s outputSecretLookupProfiles) ListProfiles(ctx context.Context, kind store.ProfileKind) ([]store.Profile, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.ProfileStore.ListProfiles(ctx, kind)
}

type outputSecretLookupStreams struct {
	store.StreamStore
	err error
}

func (s outputSecretLookupStreams) GetStream(context.Context, string) (store.Stream, error) {
	return store.Stream{}, s.err
}

type outputSecretLookupAssignments struct {
	store.ServiceRegistryStore
	err error
}

func (s outputSecretLookupAssignments) ListServiceAssignmentsForService(context.Context, string) ([]store.StreamServiceAssignment, error) {
	return nil, s.err
}
