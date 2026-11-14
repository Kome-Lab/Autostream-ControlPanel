package v3authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// CanonicalBindingResolver translates the Contracts-owned request into the CP's
// business-authorization view. It owns no alternate wire schema, normalization,
// hash, caller identity, permission decision, qualified clock or signing key.
type CanonicalBindingResolver struct{}

var _ BindingResolver = CanonicalBindingResolver{}

// NewCanonicalBindingResolver is an explicit product composition dependency:
// Dependencies{Resolver: NewCanonicalBindingResolver(), ...}. NewService and the
// default v2 server remain unchanged. This parser cannot enable real issuance
// without independently configured Authority/Integrity/Validity/Applicability.
func NewCanonicalBindingResolver() BindingResolver { return CanonicalBindingResolver{} }

func (CanonicalBindingResolver) Resolve(ctx context.Context, original json.RawMessage) (Intent, error) {
	if err := ctx.Err(); err != nil {
		return Intent{}, err
	}
	if len(original) == 0 || len(original) > evidence.MaxRequestBytes {
		return Intent{}, ErrInvalid
	}
	// Do not retain or rewrite caller transport bytes. The shared Decoder owns
	// exact fields, duplicates/null/versions/numbers/size/depth and binding checks.
	raw := append([]byte(nil), original...)
	request, err := evidence.DecodeRequest(raw)
	if err != nil {
		return Intent{}, canonicalRequestError(err)
	}
	command := request.Command
	if command.Schema != evidence.CanonicalRequestVersion {
		return Intent{}, ErrInvalid
	}
	if err = evidence.ValidateRequest(request); err != nil {
		return Intent{}, canonicalRequestError(err)
	}
	canonical, err := evidence.CanonicalBytes(command)
	if err != nil {
		return Intent{}, canonicalRequestError(err)
	}
	binding, err := evidence.ComputeBinding(command)
	if err != nil {
		return Intent{}, canonicalRequestError(err)
	}
	if binding != request.Binding || !bytes.Equal(canonical, []byte(request.Binding)) {
		return Intent{}, ErrConflict
	}
	if err = evidence.VerifyBinding(command, request.Binding); err != nil {
		return Intent{}, canonicalRequestError(err)
	}
	// Contracts has Expected.Revision, not a second nested Expected.Reference.
	// All semantic fields are copied exactly; no Principal/Issuer substitution,
	// field omission, coercion or normalization is allowed here.
	intent := Intent{
		Scope: command.Scope, Execution: command.Execution, Binding: request.Binding, Action: command.Operation,
		RequestingPrincipal: command.RequestingPrincipal, AuthorizationIssuer: command.AuthorizationIssuer,
		CandidateRevision: command.CandidateRevision, CandidateContentPin: command.CandidateContentPin,
		ExpectedRevision: command.Expected.Revision, ExpectedStateVersion: command.Expected.StateVersion,
		Required: GuardRequirement{Policy: command.RequiredGuard.Policy, Trust: command.RequiredGuard.Trust,
			Membership: command.RequiredGuard.Membership, OwnershipEpoch: command.RequiredGuard.OwnershipEpoch,
			FenceEpoch: command.RequiredGuard.FenceEpoch, Generation: command.RequiredGuard.Generation,
			BootID: command.RequiredGuard.BootID, ProcessIncarnation: command.RequiredGuard.ProcessIncarnation},
		TimeBase: command.Timing.TimeBase, DeadlineTick: command.Timing.DeadlineTick, OriginalBudget: command.OriginalBudget,
	}
	if !validIntent(intent) {
		return Intent{}, ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return Intent{}, err
	}
	// This is a bound self-description only. Service.Issue MUST still compare
	// RequestingPrincipal with the server-authenticated Principal and call both
	// Authority.Read and Authority.WithCurrent. No authorization is returned here.
	return intent, nil
}

func canonicalRequestError(err error) error {
	var contract *evidence.Error
	if errors.As(err, &contract) && contract.Code == "request_binding" {
		return ErrConflict
	}
	// Do not leak the original request, Codec payload, path or dependency error.
	return ErrInvalid
}
