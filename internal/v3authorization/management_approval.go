package v3authorization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	approval "github.com/example/autostream-control-panel/internal/bootstrapapproval"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

// ApprovalManagementService is explicit fixture-only composition. Neither the
// HTTP server nor normal startup constructs it. It cannot qualify a Trust Root,
// independent Origin or production bootstrap from a human/super_admin session.
type ApprovalManagementService struct {
	service *ManagementService
	core    *approval.Core
}
type approvalReferenceKey struct{}
type approvalAuthorizer struct{ core *approval.Core }

func NewApprovalManagementService(a *SQLAuthority, core *approval.Core) (*ApprovalManagementService, error) {
	if a == nil || core == nil || !core.UsesDB(a.db) || core.ProductionQualified() || a.lifecycle != nil {
		return nil, ErrUnavailable
	}
	return &ApprovalManagementService{service: NewManagementService(a, &approvalAuthorizer{core: core}), core: core}, nil
}

func (s *ApprovalManagementService) Bootstrap(ctx context.Context, a ManagementActor, m ManagementMetadata, g ManagementGrant, ref approval.Reference) ManagementResult {
	if s == nil || s.service == nil || ctx == nil {
		return managementFailure(ErrUnavailable)
	}
	return s.service.Bootstrap(context.WithValue(ctx, approvalReferenceKey{}, ref), a, m, g)
}

// Inspect observes the original immutable attempt only. In particular it does
// not call ManagementService.Inspect (which writes an audit), clear process
// uncertainty, or invoke Prepare/VerifyAndConsume.
func (s *ApprovalManagementService) Inspect(ctx context.Context, a ManagementActor, m ManagementMetadata, g ManagementGrant, ref approval.Reference) (approval.Inspection, error) {
	if s == nil || s.core == nil {
		return approval.Inspection{}, ErrUnavailable
	}
	_, grants, err := freezeManagementChanges(nil, []ManagementGrant{g})
	if err != nil || !validManagementMetadata(m) {
		return approval.Inspection{}, ErrInvalid
	}
	// Authenticate the current human locally, then close every SQL lock before
	// contacting the origin. Session material never enters the origin request.
	v, err := s.service.begin(ctx, a, m)
	if err != nil {
		return approval.Inspection{}, err
	}
	if v.tx.Rollback() != nil {
		return approval.Inspection{}, ErrUnavailable
	}
	return s.core.Inspect(ctx, approvalRequest(a, m, grants[0], ref))
}

func approvalRequest(a ManagementActor, m ManagementMetadata, g ManagementGrant, ref approval.Reference) approval.Request {
	ops := make([]string, len(g.Operations))
	for i, op := range g.Operations {
		ops[i] = string(op)
	}
	encoded, _ := encodeManagementGrant(g)
	digest := sha256.Sum256(encoded)
	return approval.Request{Reference: ref, EventID: m.EventID, CorrelationID: m.CorrelationID, Scope: m.Scope, Action: m.Action,
		Principal: a.Principal.ID, State: g.State, GrantBinding: hex.EncodeToString(digest[:]), ExpectedRevision: m.ExpectedRevision, Operations: ops, Subjects: append([]string(nil), g.Subjects...), ProvisionTargets: g.ProvisionTargets}
}
func approvalError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, approval.ErrUnknown):
		return ErrStoreUnknown
	case errors.Is(err, approval.ErrConflict):
		return ErrConflict
	case errors.Is(err, approval.ErrDenied):
		return ErrDenied
	default:
		return ErrUnavailable
	}
}

// The legacy interface remains compatible. The opted-in implementation cannot
// authorize outside its private same-transaction entry point.
func (a *approvalAuthorizer) AuthorizeBootstrap(context.Context, ManagementActor, ManagementMetadata, ManagementGrant) error {
	return ErrDenied
}

func (s *ManagementService) prepareBootstrapApproval(ctx context.Context, a ManagementActor, m ManagementMetadata, g ManagementGrant) (context.Context, error) {
	adapter, ok := s.bootstrap.(*approvalAuthorizer)
	if !ok {
		return ctx, nil
	}
	ref, present := ctx.Value(approvalReferenceKey{}).(approval.Reference)
	if !present || adapter == nil || adapter.core == nil || !adapter.core.UsesDB(s.authority.db) {
		return nil, ErrDenied
	}
	prepared, err := adapter.core.Prepare(ctx, approvalRequest(a, m, g, ref))
	return prepared, approvalError(err)
}

func (s *ManagementService) beginBootstrapApproval(ctx context.Context, a ManagementActor, m ManagementMetadata, g ManagementGrant) (*authorityView, error) {
	adapter, ok := s.bootstrap.(*approvalAuthorizer)
	if !ok {
		return s.begin(ctx, a, m)
	}
	if s.schema(ctx) != nil || !validManagementMetadata(m) || s.authority.schema(ctx) != nil {
		return nil, ErrUnavailable
	}
	ref, present := ctx.Value(approvalReferenceKey{}).(approval.Reference)
	if !present || adapter == nil || adapter.core == nil || !adapter.core.UsesDB(s.authority.db) {
		return nil, ErrDenied
	}
	request := approvalRequest(a, m, g, ref)
	tx, err := lf.BeginApprovalSQL(ctx, s.authority.db, approval.Binding(request))
	if err != nil {
		return nil, ErrUnavailable
	}
	v := &authorityView{ctx: ctx, tx: tx, locking: true}
	// Same existing C-management prefix: C epoch before User and Session. No
	// lifecycle prefix/last-admin protection is reordered or redesigned here.
	if tx.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE").Scan(&v.generation) != nil ||
		tx.QueryRowContext(ctx, "SELECT CAST(UTC_TIMESTAMP(6) AS CHAR)").Scan(&v.observedAt) != nil {
		tx.Rollback()
		return nil, ErrUnavailable
	}
	if err = s.identity(v, a); err != nil {
		tx.Rollback()
		return nil, err
	}
	return v, nil
}
func (s *ManagementService) consumeBootstrapApproval(ctx context.Context, v *authorityView, a ManagementActor, m ManagementMetadata, g ManagementGrant) error {
	if adapter, ok := s.bootstrap.(*approvalAuthorizer); ok {
		ref, present := ctx.Value(approvalReferenceKey{}).(approval.Reference)
		if !present || adapter == nil || adapter.core == nil {
			return ErrDenied
		}
		return approvalError(adapter.core.Consume(ctx, s.authority.db, v.tx, approvalRequest(a, m, g, ref), v.generation+1))
	}
	return s.bootstrap.AuthorizeBootstrap(ctx, ManagementActor{Principal: a.Principal}, m, g)
}
