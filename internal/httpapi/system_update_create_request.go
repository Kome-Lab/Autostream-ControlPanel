package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"strings"
)

type systemUpdateCreateRequest struct {
	PortContractVersion      int
	Mode                     string
	NewLocalListenPort       int
	ExpectedSnapshotID       string
	DesiredRevision          int64
	Fence                    int64
	Operation                string
	TargetID                 string
	Strategy                 string
	NewPort                  int
	NewAdvertisedPort        int
	NewPublishedPort         int
	NewContainerPort         int
	Docker                   bool
	ExpectedEndpointRevision int64
	IdempotencyKey           string
}

func decodeSystemUpdateCreateRequest(r *http.Request) (systemUpdateCreateRequest, error) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxSystemUpdateV2PayloadBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maxSystemUpdateV2PayloadBytes {
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	var operation struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(payload, &operation) != nil {
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	if strings.ToLower(strings.TrimSpace(operation.Operation)) == store.SystemUpdateOperationPortReconfigure {
		return decodeSystemUpdatePortV2CreateRequest(payload)
	}
	var raw struct {
		Operation                json.RawMessage `json:"operation"`
		TargetID                 string          `json:"target_id"`
		Strategy                 json.RawMessage `json:"strategy"`
		NewPort                  json.RawMessage `json:"new_port"`
		NewAdvertisedPort        json.RawMessage `json:"new_advertised_port"`
		NewPublishedPort         json.RawMessage `json:"new_published_port"`
		NewContainerPort         json.RawMessage `json:"new_container_port"`
		ExpectedEndpointRevision json.RawMessage `json:"expected_endpoint_revision"`
		IdempotencyKey           string          `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return systemUpdateCreateRequest{}, err
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	request := systemUpdateCreateRequest{
		TargetID:       strings.TrimSpace(raw.TargetID),
		IdempotencyKey: strings.TrimSpace(raw.IdempotencyKey),
	}
	request.Operation = store.SystemUpdateOperationSoftwareUpdate
	if raw.Operation != nil {
		var operation string
		if json.Unmarshal(raw.Operation, &operation) != nil ||
			strings.TrimSpace(operation) == "" {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
		request.Operation = strings.ToLower(strings.TrimSpace(operation))
	}
	if !validSystemUpdateCapabilityIdentifier(request.TargetID) ||
		request.IdempotencyKey == "" ||
		len(request.IdempotencyKey) > 128 ||
		containsControlText(request.IdempotencyKey) {
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	switch request.Operation {
	case store.SystemUpdateOperationSoftwareUpdate:
		if raw.Strategy == nil ||
			raw.NewPort != nil ||
			raw.NewAdvertisedPort != nil ||
			raw.NewPublishedPort != nil ||
			raw.NewContainerPort != nil ||
			raw.ExpectedEndpointRevision != nil ||
			json.Unmarshal(raw.Strategy, &request.Strategy) != nil {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
		request.Strategy = strings.ToLower(strings.TrimSpace(request.Strategy))
		if request.Strategy != store.SystemUpdateStrategyWhenIdle &&
			request.Strategy != store.SystemUpdateStrategyMaintenance {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
	case store.SystemUpdateOperationPortReconfigure:
		if raw.Strategy != nil ||
			raw.ExpectedEndpointRevision == nil ||
			json.Unmarshal(raw.ExpectedEndpointRevision, &request.ExpectedEndpointRevision) != nil ||
			request.ExpectedEndpointRevision < 1 {
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
		systemdShape := raw.NewPort != nil &&
			raw.NewAdvertisedPort == nil &&
			raw.NewPublishedPort == nil &&
			raw.NewContainerPort == nil
		dockerShape := raw.NewPort == nil &&
			raw.NewAdvertisedPort != nil &&
			raw.NewPublishedPort != nil &&
			raw.NewContainerPort != nil
		switch {
		case systemdShape:
			if json.Unmarshal(raw.NewPort, &request.NewPort) != nil ||
				request.NewPort < 1024 || request.NewPort > 65535 {
				return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
			}
		case dockerShape:
			request.Docker = true
			if json.Unmarshal(raw.NewAdvertisedPort, &request.NewAdvertisedPort) != nil ||
				json.Unmarshal(raw.NewPublishedPort, &request.NewPublishedPort) != nil ||
				json.Unmarshal(raw.NewContainerPort, &request.NewContainerPort) != nil ||
				request.NewAdvertisedPort < 1 || request.NewAdvertisedPort > 65535 ||
				request.NewPublishedPort < 1024 || request.NewPublishedPort > 65535 ||
				request.NewContainerPort < 1024 || request.NewContainerPort > 65535 {
				return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
			}
		default:
			return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
		}
	default:
		return systemUpdateCreateRequest{}, store.ErrInvalidSystemUpdate
	}
	return request, nil
}

func sameSystemUpdateCreateRequest(job store.SystemUpdateJob, request systemUpdateCreateRequest) bool {
	operation := strings.ToLower(strings.TrimSpace(job.Operation))
	if operation == "" {
		operation = store.SystemUpdateOperationSoftwareUpdate
	}
	if job.TargetID != request.TargetID || operation != request.Operation {
		return false
	}
	if operation == store.SystemUpdateOperationPortReconfigure {
		if request.PortContractVersion == 2 {
			return sameSystemUpdatePortV2CreateRequest(job, request)
		}
		if job.PortReconfigure == nil ||
			job.PortReconfigure.ExpectedEndpointRevision != request.ExpectedEndpointRevision {
			return false
		}
		if request.Docker {
			return job.DeploymentMode == "docker" &&
				job.PortReconfigure.Docker != nil &&
				job.PortReconfigure.NewPort == request.NewAdvertisedPort &&
				job.PortReconfigure.Docker.NewPublishedPort == request.NewPublishedPort &&
				job.PortReconfigure.Docker.NewContainerPort == request.NewContainerPort
		}
		return job.DeploymentMode != "docker" &&
			job.PortReconfigure.Docker == nil &&
			job.PortReconfigure.NewPort == request.NewPort
	}
	return job.Strategy == request.Strategy
}
