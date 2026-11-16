package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"strings"
	"time"
)

type manualHoldDispatcher interface {
	StartAdmissionIdentity(context.Context, store.RegisteredService) (lifecycle.ProducerRef, error)
	CloseStartAdmission(context.Context, store.RegisteredService, lifecycle.Record) (lifecycle.Record, error)
	ObserveAutoStartCondition(context.Context, store.RegisteredService, string, uint64, string) (servicecall.AutoStartCondition, error)
	ObserveAutoStartPresence(context.Context, store.RegisteredService, string, string) (servicecall.AutoStartCondition, error)
}

// The concrete product dispatcher implements this protocol. Old, untracked
// test/interop dispatchers are not silently upgraded into qualified targets.
func (s *Server) manualAutoStartManaged() bool {
	_, ok := s.dispatcher.(manualHoldDispatcher)
	return ok
}

func (s *Server) validateAutomaticStart(ctx context.Context, stream store.Stream, original autoStartOriginal) error {
	if original.RequestID == "" || original.ExpectedStreamRevision == "" || original.ExpectedSettingsRevision == "" {
		return manualhold.ErrUnknown
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		return manualhold.ErrUnknown
	}
	hold, err := owner.ObserveAutoStartHold(ctx, stream.ID)
	if err != nil {
		return err
	}
	if !hold.Known {
		return manualhold.ErrUnknown
	}
	if hold.Held || hold.PendingAttemptID != "" || hold.Revision != original.ExpectedHoldRevision || stream.UpdatedAt.UTC().Format(time.RFC3339Nano) != original.ExpectedStreamRevision || stream.SettingsRevision != original.ExpectedSettingsRevision || stream.ArchiveRunID != original.ExpectedRunID {
		return lf.ErrConflict
	}
	return nil
}

func (s *Server) bindOriginalStartClaim(r *http.Request, stream store.Stream) (*http.Request, error) {
	if !s.manualAutoStartManaged() {
		return r, nil
	}
	original, automatic := r.Context().Value(autoStartOriginalKey{}).(autoStartOriginal)
	id, operation := original.RequestID, "auto-start"
	if !automatic {
		operation = "manual-start"
		id = strings.TrimSpace(r.Header.Get("X-AutoStream-Request-ID"))
		if id == "" {
			var err error
			id, err = newAutoStartRequestID()
			if err != nil {
				return r, err
			}
		}
	}
	if id == "" || len(id) > 86 {
		return r, manualhold.ErrRefused
	}
	request := store.StartClaimRequestOriginal{RequestID: id, StreamID: stream.ID, ActorUserID: currentFromContext(r.Context()).User.ID, Operation: operation, Automatic: automatic, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedSettingsRevision: stream.SettingsRevision, ExpectedRunID: stream.ArchiveRunID}
	ctx := store.WithStartClaimRequest(r.Context(), request)
	ctx = context.WithValue(ctx, startClaimRequestIDKey{}, id)
	return r.WithContext(ctx), nil
}

func (s *Server) bindStartAdmissions(r *http.Request, stream store.Stream, services []store.RegisteredService) (*http.Request, error) {
	if !s.manualAutoStartManaged() {
		return r, nil
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		return r, manualhold.ErrUnknown
	}
	hold, err := owner.ObserveAutoStartHold(r.Context(), stream.ID)
	if err != nil {
		return r, err
	}
	if !hold.Known || hold.PendingAttemptID != "" {
		return r, manualhold.ErrUnknown
	}
	_, automatic := r.Context().Value(autoStartOriginalKey{}).(autoStartOriginal)
	id, saved := r.Context().Value(startClaimRequestIDKey{}).(string)
	if !saved || id == "" {
		return r, manualhold.ErrUnknown
	}
	operation := "auto-start"
	correlation := lifecycle.Correlation{}
	if parent, resumed := r.Context().Value(resumeStartOriginKey{}).(string); resumed {
		id, automatic = parent+":start", true
		correlation = autoStartCorrelation(id, operation, "resume-start")
		correlation.AttemptID, correlation.ExecutionKey = lifecycle.ID(parent), lifecycle.ID(parent)
	} else if !automatic {
		operation = "manual-start"
	}
	if automatic && hold.Held {
		return r, lf.ErrHeld
	}
	if correlation.RequestID == "" {
		correlation = autoStartCorrelation(id, operation, "start")
	}
	admission := store.AutoStartAdmission{RequestID: id, StreamID: stream.ID, Automatic: automatic, HoldRevision: hold.Revision, StreamUpdatedAt: stream.UpdatedAt, SettingsRevision: stream.SettingsRevision, RunID: stream.ArchiveRunID}
	records := map[string]lifecycle.Record{}
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" && service.ServiceType != "worker" {
			continue
		}
		record, recordErr := s.manualHoldRecord(r.Context(), stream, hold, service, correlation)
		if recordErr != nil {
			return r, recordErr
		}
		admission.Records = append(admission.Records, record)
		records[service.ServiceID] = record
	}
	// Persist only the immutable public G0 correlation, before any provider or
	// target dispatch. A reused request ID never creates a second dispatch.
	if err := owner.SaveAutoStartAdmission(r.Context(), admission); err != nil {
		return r, err
	}
	ctx := servicecall.WithStartAdmissions(r.Context(), records)
	ctx = context.WithValue(ctx, savedStartAdmissionKey{}, admission)
	return r.WithContext(ctx), nil
}

func (s *Server) startAdmissionCurrent(ctx context.Context) error {
	admission, managed := ctx.Value(savedStartAdmissionKey{}).(store.AutoStartAdmission)
	if !managed {
		if s.manualAutoStartManaged() {
			return manualhold.ErrUnknown
		}
		return nil
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		return manualhold.ErrUnknown
	}
	_, err := owner.InspectAutoStartAdmission(ctx, admission.StreamID, admission.RequestID)
	return err
}

func newAutoStartRequestID() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "msr-" + hex.EncodeToString(value[:]), nil
}

func autoStartCorrelation(id, operation, step string) lifecycle.Correlation {
	return lifecycle.Correlation{RequestID: lifecycle.ID(id), AttemptID: lifecycle.ID(id), OperationID: lifecycle.ID(operation), StepID: lifecycle.ID(step), ExecutionKey: lifecycle.ID(id)}
}

func manualHoldTargets(services []store.RegisteredService, streamID string) []lifecycle.ResourceRef {
	var targets []lifecycle.ResourceRef
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" || service.ServiceType == "worker" {
			targets = append(targets, lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: lifecycle.ID(service.ServiceID), ResourceID: lifecycle.ID(streamID)})
		}
	}
	return targets
}

func manualHoldReference(label string, parts ...string) lifecycle.ID {
	// Only public product IDs/revisions are accepted by callers, never a raw
	// Start body or secret-derived commitment. This is a local metadata reference.
	data, _ := json.Marshal(parts)
	digest := sha256.Sum256(data)
	return lifecycle.ID(label + "-" + hex.EncodeToString(digest[:24]))
}

func (s *Server) manualHoldRecord(ctx context.Context, stream store.Stream, hold store.AutoStartHold, service store.RegisteredService, correlation lifecycle.Correlation) (lifecycle.Record, error) {
	client, ok := s.dispatcher.(manualHoldDispatcher)
	if !ok || hold.Revision == 0 || s.services == nil {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, service.ServiceID)
	if err != nil {
		return lifecycle.Record{}, err
	}
	var membership uint64
	for _, assignment := range assignments {
		if assignment.StreamID == stream.ID && normalizeAssignmentRole(assignment.AssignmentRole) == "primary" {
			// Existing assignment CAS timestamp, encoded losslessly at the DB's
			// microsecond precision. This is an equality-only snapshot revision,
			// never a new Run/Generation or a qualified independent epoch.
			if assignment.AssignedAt.IsZero() || assignment.AssignedAt.UnixMicro() <= 0 || membership != 0 {
				return lifecycle.Record{}, manualhold.ErrUnknown
			}
			membership = uint64(assignment.AssignedAt.UTC().UnixMicro())
		}
	}
	if membership == 0 {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	peer, err := client.StartAdmissionIdentity(ctx, service)
	if err != nil {
		return lifecycle.Record{}, err
	}
	producerSource := s.manualAutoStartProducer
	if producerSource == nil {
		producerSource = func() (lifecycle.ProducerRef, error) { return manualhold.LocalProducer("C01", "control-panel") }
	}
	producer, err := producerSource()
	if err != nil {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	resource := lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: lifecycle.ID(service.ServiceID), ResourceID: lifecycle.ID(stream.ID)}
	cr := lifecycle.ContractRef{ContractID: lifecycle.CC02, LogicalRevision: 1, SpecRef: lifecycle.SpecRef, ModuleCommitRef: manualhold.ModuleCommit}
	source := stream.UpdatedAt.UTC().Format(time.RFC3339Nano)
	frame := manualHoldReference("hold-frame", stream.ID, fmt.Sprint(hold.Revision), source, stream.SettingsRevision)
	h := lifecycle.Header{ContractRef: cr, RecordKey: lifecycle.RecordKey{ContractRef: cr, OwnerID: "control-panel", RecordID: manualHoldReference("request", string(correlation.RequestID), service.ServiceID, string(correlation.StepID))}, Correlation: correlation,
		DomainRef: lifecycle.DomainRef{DeploymentID: "control-panel", Realm: "stream-operations", DomainID: lifecycle.ID(stream.ID), MembershipRevision: membership, SnapshotRef: manualHoldReference("selected", service.ServiceID, source, fmt.Sprint(membership)), Resources: []lifecycle.ResourceRef{resource}}, ProducerRef: producer, RecordRevision: hold.Revision, ProfileRef: lifecycle.ProfileRef{ProfileID: lifecycle.ExplicitResume, Revision: 1}}
	vector := lifecycle.RevisionVector{}
	literal := func(axis lifecycle.Axis, value lifecycle.ID) {
		vector = append(vector, lifecycle.RevisionEntry{Key: lifecycle.AxisKey{Axis: axis, OwnerID: resource.OwnerID, Resource: resource}, Incarnation: lifecycle.ID(stream.ID), Value: lifecycle.VersionedValue{Kind: lifecycle.PresentLiteral, Literal: &value}, SourceFrameRef: frame})
	}
	vector = append(vector, lifecycle.RevisionEntry{Key: lifecycle.AxisKey{Axis: lifecycle.Membership, OwnerID: resource.OwnerID, Resource: resource}, Incarnation: lifecycle.ID(stream.ID), Value: lifecycle.VersionedValue{Kind: lifecycle.PresentUint64, Uint64: &membership}, SourceFrameRef: frame})
	literal(lifecycle.Admission, correlation.AttemptID)
	literal(lifecycle.Ownership, manualHoldReference("ownership", stream.ID, service.ServiceID, source, fmt.Sprint(membership), fmt.Sprint(service.OwnershipEpoch), fmt.Sprint(service.EndpointRevision), fmt.Sprint(service.AppliedConfigRevision)))
	literal(lifecycle.Fence, lifecycle.ID(source))
	literal(lifecycle.Boot, peer.BootID)
	literal(lifecycle.Process, peer.ProcessIncarnation)
	run := lifecycle.ID(stream.ArchiveRunID)
	if run == "" {
		run = manualHoldReference("observed-no-archive-run", stream.ID, source)
	}
	literal(lifecycle.RunAxis, run)
	literal(lifecycle.ConfigAxis, lifecycle.ID(stream.SettingsRevision))
	record := lifecycle.Record{Header: h, Vector: &lifecycle.VectorRecord{Vector: vector}}
	if err := manualhold.Validate(record, stream.ID); err != nil {
		return lifecycle.Record{}, err
	}
	return record, nil
}

func (s *Server) closeManualHoldTargets(ctx context.Context, stream store.Stream, attempt store.AutoStartHoldAttempt, services []store.RegisteredService) ([]lifecycle.Record, error) {
	client, ok := s.dispatcher.(manualHoldDispatcher)
	if !ok {
		return nil, manualhold.ErrUnknown
	}
	var evidence []lifecycle.Record
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" && service.ServiceType != "worker" {
			continue
		}
		record, err := s.manualHoldRecord(ctx, stream, attempt.Hold, service, attempt.Request.Correlation)
		if err != nil {
			return evidence, err
		}
		ack, err := client.CloseStartAdmission(ctx, service, record)
		if err != nil {
			return evidence, err
		}
		evidence = append(evidence, ack)
	}
	return evidence, nil
}

func (s *Server) inspectServiceAutomaticStart(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "streams.start")
	if !ok {
		return
	}
	if token.ServiceType != "discord_bot" {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, stream.ID, "discord_bot")
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	if !assigned || stream.NodeSelectionsConfigured && stream.SelectedDiscordID != service.ServiceID {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		writeManualHoldError(w, manualhold.ErrUnknown)
		return
	}
	original, lookupErr := owner.LookupStartClaimRequest(r.Context(), stream.ID, r.PathValue("request_id"))
	if lookupErr != nil {
		writeManualHoldError(w, manualhold.ErrUnknown)
		return
	}
	if !original.Automatic || original.ActorUserID != "service:"+service.ServiceID {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	admission, err := owner.LookupAutoStartAdmission(r.Context(), stream.ID, r.PathValue("request_id"))
	if err != nil {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.Unknown))
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "start_admission_unknown", "request_id": original.RequestID, "stream_id": original.StreamID, "original_source_updated_at": original.ExpectedStreamUpdatedAt, "gate": lifecycle.NotQualified})
		return
	}
	if !admission.Automatic {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"admission": admission, "start_observations": s.inspectStartBoundaries(r.Context(), admission), "gate": lifecycle.NotQualified})
}

func (s *Server) inspectStartBoundaries(ctx context.Context, admission store.AutoStartAdmission) []lifecycle.BoundaryObservation {
	client, ok := s.dispatcher.(interface {
		InspectStartAdmission(context.Context, store.RegisteredService, lifecycle.Record) (servicecall.StartAdmissionInspection, error)
	})
	var observations []lifecycle.BoundaryObservation
	services, err := s.streamAssignments(ctx, admission.StreamID)
	if err != nil {
		services = nil
	}
	for _, record := range admission.Records {
		resource, resourceErr := manualhold.Resource(record)
		if resourceErr != nil {
			continue
		}
		unknown := lifecycle.BoundaryObservation{Address: lifecycle.BoundaryAddress{Kind: lifecycle.BackendMutation, OwnerID: resource.OwnerID, Resource: resource, SubBoundary: lifecycle.ServiceApply}, Knowledge: lifecycle.KnowledgeUnknown, Presence: lifecycle.NotObserved, Outcome: lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Unavailable, Unavailable: &lifecycle.UnavailableReason{Code: "ORIGINAL_TARGET_RESULT_NOT_OBSERVED"}}}
		observation := unknown
		if ok {
			for _, service := range services {
				if service.ServiceID == string(resource.OwnerID) {
					if reply, inspectErr := client.InspectStartAdmission(ctx, service, record); inspectErr == nil && reply.Outcome != nil {
						observation = *reply.Outcome
					}
					break
				}
			}
		}
		observations = append(observations, observation)
	}
	return observations
}

// A target inspects only the CP-owned original admission. It cannot turn an
// arbitrary client vector into permission and this route performs no writes.
func (s *Server) inspectServiceStartAdmission(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	if token.ServiceType != "encoder_recorder" && token.ServiceType != "worker" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, manualhold.MaxRecordBytes+1))
	if err != nil {
		writeManualHoldError(w, manualhold.ErrUnknown)
		return
	}
	original, err := manualhold.DecodeJSON(body)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	resource, err := manualhold.Resource(original)
	if err != nil || string(resource.ResourceID) != r.PathValue("id") {
		writeManualHoldError(w, manualhold.ErrRefused)
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, string(resource.ResourceID), token.ServiceType)
	if err != nil || !assigned || resource.OwnerID != lifecycle.ID(service.ServiceID) {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		writeManualHoldError(w, manualhold.ErrUnknown)
		return
	}
	admission, err := owner.InspectAutoStartAdmission(r.Context(), string(resource.ResourceID), string(original.Header.Correlation.RequestID))
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	stream, err := s.streams.GetStream(r.Context(), string(resource.ResourceID))
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	hold, err := owner.ObserveAutoStartHold(r.Context(), stream.ID)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	current, err := s.manualHoldRecord(r.Context(), stream, hold, service, original.Header.Correlation)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	for _, record := range admission.Records {
		if manualhold.Equal(original, record) && manualhold.Equal(original, current) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, record)
			return
		}
	}
	writeManualHoldError(w, manualhold.ErrRefused)
}
