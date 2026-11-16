package lifecyclefoundation

import "context"

// DoExplicit joins the existing B0 machine with an owner-persisted request ID.
// It does not generate an Attempt, retry, repair, or release a quarantine. The
// caller must Inspect a previously submitted ID instead of calling this again.
// Content and Target must contain non-secret product metadata only.
func (g *Gate) DoExplicit(ctx context.Context, id string, call Call, fn func(context.Context) error) (Result, error) {
	refused := Result{Outcome: "HELD", Attempt: id}
	if g.Ready() != nil || ctx == nil || ctx.Err() != nil || fn == nil || !validID(id) {
		return refused, ErrHeld
	}
	if !identityGateCallValid(ctx, g, call) || ctx.Value(gateKey{}) != nil || ctx.Value(participationKey{}) != nil {
		return refused, ErrHeld
	}
	mode := Write
	if call.Purpose == "CURRENT_READ" {
		mode = Read
	}
	req := Request{ID: id, Intent: Intent{
		Mode: mode, Purpose: call.Purpose, Operation: call.Operation,
		OperationContent: SyntheticReference(struct {
			Actor   string
			Content any
		}{call.Actor, call.Content}),
		Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(call.Target)}, Subject: "product",
	}}
	if req.Validate() != nil {
		return refused, ErrHeld
	}
	return g.service.Run(ctx, req, func(partCtx context.Context, transaction *Transaction) (Checkpoint, error) {
		p := transaction.proof
		if call.Actor != "" && p.authorization.Actor != call.Actor {
			return Checkpoint{}, ErrHeld
		}
		query := &participantExecutor{tx: transaction.tx, p: p, ctx: partCtx}
		before, err := g.source(partCtx, query)
		if err != nil || before != p.before {
			return Checkpoint{}, ErrHeld
		}
		p.gate = g
		admitted := context.WithValue(partCtx, gateKey{}, &gateAdmission{gate: g, call: call})
		if err := fn(admitted); err != nil {
			return Checkpoint{}, err
		}
		return g.source(admitted, query)
	})
}

// InspectExplicit is the existing pure B0 inspection path. It cannot complete,
// retry, release a held slot, or create an operational capability.
func (g *Gate) InspectExplicit(ctx context.Context, intent Intent, id string) (Inspection, error) {
	if g.Ready() != nil {
		return Inspection{}, ErrHeld
	}
	return g.service.Inspect(ctx, intent, id)
}

// InspectExplicitCall derives precisely the original public metadata binding.
// It never calls Run or creates another reservation.
func (g *Gate) InspectExplicitCall(ctx context.Context, id string, call Call) (Inspection, error) {
	mode := Write
	if call.Purpose == "CURRENT_READ" {
		mode = Read
	}
	intent := Intent{Mode: mode, Purpose: call.Purpose, Operation: call.Operation,
		OperationContent: SyntheticReference(struct {
			Actor   string
			Content any
		}{call.Actor, call.Content}),
		Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(call.Target)}, Subject: "product"}
	inspection, err := g.Inspect(ctx, call.Target, id)
	if err != nil {
		return Inspection{}, err
	}
	if inspection.Ledger.Reservation.Request.ID != id || inspection.Ledger.Reservation.Request.Intent != intent || inspection.Ledger.Reservation.Authorization.Actor != call.Actor {
		return Inspection{}, ErrHeld
	}
	return inspection, nil
}
