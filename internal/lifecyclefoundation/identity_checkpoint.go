package lifecyclefoundation

import "context"

// IdentityFixtureCoverage describes opaque references to synthetic fixture data.
// It is deliberately not a production secret-commitment or qualified-clock profile.
const IdentityFixtureCoverage = "identity-fixture-v1"

type IdentityProjection struct {
	Revision            uint64
	OperationalRevision uint64
	AuthorityEpoch      uint64
	Count               uint64
	ContentReference    string
	HistoryReference    string
	Coverage            string
	Qualified           bool
}

func (p IdentityProjection) Validate() error {
	if p.Revision == 0 || p.OperationalRevision == 0 || p.AuthorityEpoch == 0 ||
		!validID(p.ContentReference) || !validID(p.HistoryReference) ||
		p.Coverage != IdentityFixtureCoverage || p.Qualified {
		return ErrHeld
	}
	return nil
}

// ReadIdentityProjection reads stored references only; it does not capture,
// export, hash or qualify password, session, MFA or other secret state.
func ReadIdentityProjection(ctx context.Context, q Executor) (IdentityProjection, error) {
	return readIdentityProjection(ctx, q, false)
}

func readIdentityProjection(ctx context.Context, q Executor, lock bool) (IdentityProjection, error) {
	if ctx == nil || ctx.Err() != nil || missing(q) {
		return IdentityProjection{}, ErrHeld
	}
	if err := checkIdentitySchema(ctx, q); err != nil {
		return IdentityProjection{}, err
	}
	var count int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_identity_projection").Scan(&count); err != nil || count != 1 {
		return IdentityProjection{}, ErrHeld
	}
	query := "SELECT revision,operational_revision,authority_epoch,entity_count,content_reference,history_reference,coverage,qualified FROM v3_cp_identity_projection WHERE singleton=1"
	if lock {
		query += " FOR UPDATE"
	}
	var p IdentityProjection
	if err := q.QueryRowContext(ctx, query).Scan(&p.Revision, &p.OperationalRevision, &p.AuthorityEpoch, &p.Count,
		&p.ContentReference, &p.HistoryReference, &p.Coverage, &p.Qualified); err != nil {
		return IdentityProjection{}, held(err)
	}
	if p.Validate() != nil {
		return IdentityProjection{}, ErrHeld
	}
	return p, nil
}

type identityHistoryRecord struct {
	Attempt string
	Call    IdentityCall
	Binding IdentityBinding
	Before  IdentityProjection
	After   IdentityProjection
}

// advanceIdentityProjection participates exclusively in the owning Gate's SQL
// transaction. It cannot enroll a projection or independently commit an update.
func advanceIdentityProjection(ctx context.Context, q Executor, before, after IdentityProjection, call IdentityCall, binding IdentityBinding) error {
	if ctx == nil || before.Validate() != nil || after.Validate() != nil ||
		before.Revision == ^uint64(0) || before.Revision != binding.Revision ||
		before.Coverage != binding.Coverage || binding.Target != call.Target ||
		after.Revision != before.Revision || after.HistoryReference != before.HistoryReference ||
		after.Coverage != before.Coverage || after.AuthorityEpoch != before.AuthorityEpoch ||
		after.OperationalRevision < before.OperationalRevision {
		return ErrHeld
	}
	p, ok := ctx.Value(participationKey{}).(*participation)
	if !ok || p == nil || p.gate == nil || p.request.Intent.Purpose != "IDENTITY_CHANGE" {
		return ErrHeld
	}
	if err := RequireParticipation(ctx, p.store, p.request); err != nil {
		return err
	}
	// Do not trust a caller-supplied raw pool/executor to perform this write.
	joined, bound, err := SQL(ctx, p.store.db)
	if err != nil || !bound {
		return ErrHeld
	}
	q = joined
	current, err := readIdentityProjection(ctx, q, true)
	if err != nil {
		return err
	}
	if current != before {
		return ErrConflict
	}
	after.Revision++
	// Only opaque references and bounded non-secret participation metadata enter
	// the history reference. Capturing synthetic fixture bytes is test-only work.
	after.HistoryReference = SyntheticReference(struct {
		Previous string
		Revision uint64
		Content  string
		Attempt  string
		Call     IdentityCall
		Binding  IdentityBinding
	}{before.HistoryReference, after.Revision, after.ContentReference, p.request.ID, call, binding})
	result, err := q.ExecContext(ctx, "UPDATE v3_cp_identity_projection SET revision=?,operational_revision=?,authority_epoch=?,entity_count=?,content_reference=?,history_reference=? WHERE singleton=1 AND revision=? AND content_reference=? AND history_reference=? AND coverage=? AND qualified=0",
		after.Revision, after.OperationalRevision, after.AuthorityEpoch, after.Count, after.ContentReference, after.HistoryReference,
		before.Revision, before.ContentReference, before.HistoryReference, before.Coverage)
	if err != nil {
		return held(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	record := identityHistoryRecord{Attempt: p.request.ID, Call: call, Binding: binding, Before: before, After: after}
	if _, err = q.ExecContext(ctx, "INSERT INTO v3_cp_identity_history(revision,attempt,body) VALUES(?,?,?)", after.Revision, p.request.ID, encode(record)); err != nil {
		return held(err)
	}
	return nil
}
