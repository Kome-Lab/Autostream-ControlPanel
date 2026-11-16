package servicecall

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/store"
)

const ManualAutoStartHoldCapability = "manual_auto_start_hold_v1"

type startAdmissionsKey struct{}

// WithStartAdmissions carries non-authority G0 records, separately from the
// legacy private Start body. Neither a credential nor a payload digest enters it.
func WithStartAdmissions(ctx context.Context, records map[string]lifecycle.Record) context.Context {
	frozen := make(map[string]lifecycle.Record, len(records))
	for key, record := range records {
		body, _ := json.Marshal(record)
		var copy lifecycle.Record
		_ = json.Unmarshal(body, &copy)
		frozen[key] = copy
	}
	return context.WithValue(ctx, startAdmissionsKey{}, frozen)
}

func attachStartAdmission(ctx context.Context, request *http.Request, service store.RegisteredService, endpoint string) error {
	records, present := ctx.Value(startAdmissionsKey{}).(map[string]lifecycle.Record)
	if !present || (service.ServiceType != "encoder_recorder" && service.ServiceType != "worker") {
		return nil
	}
	if !strings.Contains(endpoint, "start") {
		return nil
	}
	record, found := records[service.ServiceID]
	if !found {
		return manualhold.ErrUnknown
	}
	encoded, err := manualhold.Encode(record)
	if err != nil {
		return err
	}
	request.Header.Set(manualhold.Header, encoded)
	return nil
}

// admissionJSON uses the existing selected-service URL policy and private runtime
// credential resolver. It never follows a URL from a client or retries a request.
func (c Client) admissionJSON(ctx context.Context, service store.RegisteredService, method, endpoint string, input any, output any) error {
	if !c.Enabled() || c.Config.URLPolicy.ValidateURL(service.PublicURL) != nil {
		return manualhold.ErrUnknown
	}
	token, err := c.authToken(service)
	if err != nil {
		return manualhold.ErrUnknown
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil || len(data) > manualhold.MaxRecordBytes {
			return manualhold.ErrRefused
		}
		body = bytes.NewReader(data)
	}
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, joinURL(service.PublicURL, endpoint), body)
	if err != nil {
		return manualhold.ErrUnknown
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return manualhold.ErrUnknown
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
		return manualhold.ErrRefused
	}
	if response.StatusCode != http.StatusOK {
		return manualhold.ErrUnknown
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, manualhold.MaxRecordBytes+1))
	if err != nil || len(data) > manualhold.MaxRecordBytes {
		return manualhold.ErrUnknown
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return manualhold.ErrUnknown
	}
	if decoder.Decode(new(any)) != io.EOF {
		return manualhold.ErrUnknown
	}
	if reader, ok := output.(interface{ readAdmissionHeaders(http.Header) error }); ok {
		return reader.readAdmissionHeaders(response.Header)
	}
	return nil
}

type StartAdmissionInspection struct {
	lifecycle.Record
	Outcome *lifecycle.BoundaryObservation `json:"-"`
}

func (reply *StartAdmissionInspection) readAdmissionHeaders(header http.Header) error {
	encoded := header.Get("X-AutoStream-Start-Outcome")
	if encoded == "" || len(encoded) > manualhold.MaxRecordBytes {
		return manualhold.ErrUnknown
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return manualhold.ErrRefused
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var outcome lifecycle.BoundaryObservation
	if decoder.Decode(&outcome) != nil || decoder.Decode(new(any)) != io.EOF {
		return manualhold.ErrRefused
	}
	kind := lifecycle.ValidateBoundaryObservation(outcome, manualhold.Limits()).Kind
	if kind != lifecycle.StructurallyCoherent && kind != lifecycle.Unknown {
		return manualhold.ErrRefused
	}
	reply.Outcome = &outcome
	return nil
}

// InspectStartAdmission performs only a bounded result lookup. It cannot Start,
// retry, consume, finalize, or release a target reservation.
func (c Client) InspectStartAdmission(ctx context.Context, service store.RegisteredService, original lifecycle.Record) (StartAdmissionInspection, error) {
	var reply StartAdmissionInspection
	resource, err := manualhold.Resource(original)
	if err != nil || resource.OwnerID != lifecycle.ID(service.ServiceID) {
		return reply, manualhold.ErrRefused
	}
	err = c.admissionJSON(ctx, service, http.MethodPost, "/streams/"+url.PathEscape(string(resource.ResourceID))+"/start-admission/inspect", original, &reply)
	if err != nil {
		return reply, err
	}
	if !manualhold.SameSource(original, reply.Record) || reply.Outcome == nil || reply.Outcome.Address.Kind != lifecycle.BackendMutation || reply.Outcome.Address.SubBoundary != lifecycle.ServiceApply || reply.Outcome.Address.OwnerID != resource.OwnerID || reply.Outcome.Address.Resource != resource {
		return StartAdmissionInspection{}, manualhold.ErrRefused
	}
	if observed := reply.Outcome.Outcome.Observation; observed != nil {
		if observed.Scope != resource.ResourceID || observed.SourceRevision != original.Header.RecordRevision || observed.FrameRef != original.Header.RecordKey.RecordID {
			return StartAdmissionInspection{}, manualhold.ErrRefused
		}
	}
	return reply, nil
}

func (c Client) StartAdmissionIdentity(ctx context.Context, service store.RegisteredService) (lifecycle.ProducerRef, error) {
	var producer lifecycle.ProducerRef
	if err := c.admissionJSON(ctx, service, http.MethodGet, "/start-admission/identity", nil, &producer); err != nil {
		return producer, err
	}
	expected := lifecycle.ComponentID("C04")
	if service.ServiceType == "worker" {
		expected = "C06"
	}
	if producer.ComponentID != expected || producer.ServiceID != lifecycle.ID(service.ServiceID) || producer.HostID == "" || producer.BootID == "" || producer.ProcessIncarnation == "" || producer.WriterID == "" {
		return lifecycle.ProducerRef{}, manualhold.ErrRefused
	}
	return producer, nil
}

func (c Client) CloseStartAdmission(ctx context.Context, service store.RegisteredService, record lifecycle.Record) (lifecycle.Record, error) {
	resource, err := manualhold.Resource(record)
	if err != nil || resource.OwnerID != lifecycle.ID(service.ServiceID) {
		return lifecycle.Record{}, manualhold.ErrRefused
	}
	var acknowledgement lifecycle.Record
	err = c.admissionJSON(ctx, service, http.MethodPost, "/streams/"+url.PathEscape(string(resource.ResourceID))+"/start-admission/close", record, &acknowledgement)
	if err != nil {
		return lifecycle.Record{}, err
	}
	expectedComponent := lifecycle.ComponentID("C04")
	if service.ServiceType == "worker" {
		expectedComponent = "C06"
	}
	producer := acknowledgement.Header.ProducerRef
	if manualhold.Validate(acknowledgement, string(resource.ResourceID)) != nil || !manualhold.SameSource(record, acknowledgement) || acknowledgement.Header.RecordKey.OwnerID != lifecycle.ID(service.ServiceID) || producer.ServiceID != lifecycle.ID(service.ServiceID) || producer.ComponentID != expectedComponent {
		return lifecycle.Record{}, manualhold.ErrRefused
	}
	for _, axis := range record.Vector.Vector {
		if axis.Key.Axis == lifecycle.Boot && (axis.Value.Literal == nil || *axis.Value.Literal != producer.BootID) {
			return lifecycle.Record{}, manualhold.ErrRefused
		}
		if axis.Key.Axis == lifecycle.Process && (axis.Value.Literal == nil || *axis.Value.Literal != producer.ProcessIncarnation) {
			return lifecycle.Record{}, manualhold.ErrRefused
		}
	}
	return acknowledgement, nil
}

// AutoStartCondition is a product presence observation, not an admission or
// permit. An unavailable current SDK view is different from known absence.
type AutoStartCondition struct {
	StreamID         string `json:"stream_id"`
	AssignmentEpoch  string `json:"assignment_epoch"`
	HoldRevision     uint64 `json:"hold_revision"`
	Known            bool   `json:"known"`
	Present          bool   `json:"present"`
	StreamRevision   string `json:"stream_revision"`
	SettingsRevision string `json:"settings_revision"`
	RunID            string `json:"run_id"`
}

func (c Client) ObserveAutoStartCondition(ctx context.Context, service store.RegisteredService, streamID string, revision uint64, assignment string) (AutoStartCondition, error) {
	var result AutoStartCondition
	if service.ServiceType != "discord_bot" || revision == 0 || assignment == "" {
		return result, manualhold.ErrRefused
	}
	endpoint := "/streams/" + url.PathEscape(streamID) + "/auto-start-condition?hold_revision=" + strconv.FormatUint(revision, 10) + "&assignment_epoch=" + url.QueryEscape(assignment)
	if err := c.admissionJSON(ctx, service, http.MethodGet, endpoint, nil, &result); err != nil {
		return result, err
	}
	if result.StreamID != streamID || result.HoldRevision != revision || result.AssignmentEpoch != assignment {
		return AutoStartCondition{}, manualhold.ErrRefused
	}
	if !result.Known {
		return result, manualhold.ErrUnknown
	}
	return result, nil
}

// Presence belongs to the Bot's actual SDK/assignment view; the durable Hold
// belongs to CP. A pending Resume can change Hold revision without changing
// who is present. This pure lookup neither observes an unheld value nor gives
// permission to Start; CP separately validates its pending Hold and source CAS.
func (c Client) ObserveAutoStartPresence(ctx context.Context, service store.RegisteredService, streamID, assignment string) (AutoStartCondition, error) {
	var result AutoStartCondition
	if service.ServiceType != "discord_bot" || streamID == "" || assignment == "" {
		return result, manualhold.ErrRefused
	}
	endpoint := "/streams/" + url.PathEscape(streamID) + "/auto-start-presence?assignment_epoch=" + url.QueryEscape(assignment)
	if err := c.admissionJSON(ctx, service, http.MethodGet, endpoint, nil, &result); err != nil {
		return result, err
	}
	if result.StreamID != streamID || result.AssignmentEpoch != assignment {
		return AutoStartCondition{}, manualhold.ErrRefused
	}
	if !result.Known {
		return result, manualhold.ErrUnknown
	}
	return result, nil
}
