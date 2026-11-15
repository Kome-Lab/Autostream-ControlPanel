package lifecyclefoundation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
)

type Service struct {
	store      *SQLStore
	ledger     Port
	authorizer Authorizer
}

func missing(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Chan, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func NewService(store *SQLStore, ledger Port, authorizer Authorizer) (*Service, error) {
	if store == nil || store.db == nil || store.binding.Validate() != nil || missing(ledger) || missing(authorizer) {
		return nil, ErrHeld
	}
	return &Service{store: store, ledger: ledger, authorizer: authorizer}, nil
}

func equivalent(a, b Frame) bool {
	return a.Binding == b.Binding && a.Sequence == b.Sequence &&
		a.Checkpoint == b.Checkpoint && a.Attempt == b.Attempt
}

// Run evaluates a trusted local authorizer and never retries any mutation.
// There is no product HTTP route, automatic enrollment, recovery or fallback.
func (s *Service) Run(ctx context.Context, req Request, effect Effect) (Result, error) {
	rejected := Result{Outcome: "HELD", Attempt: req.ID}
	if s == nil || ctx == nil || ctx.Err() != nil || req.Validate() != nil || effect == nil {
		return rejected, ErrHeld
	}
	frame, err := s.store.current(ctx)
	if err != nil {
		return rejected, err
	}
	if err = s.store.alreadyUsed(ctx, req.ID); err != nil {
		return rejected, err
	}
	authorization, err := s.authorizer.Resolve(ctx, req.Intent)
	if err != nil || validateAuthorization(authorization, req.Intent) != nil {
		return rejected, ErrHeld
	}
	// Detach slices/pointers owned by the trusted adapter before crossing stages.
	var frozen Authorization
	if decode(encode(authorization), &frozen) != nil {
		return rejected, ErrHeld
	}
	challengeBytes := make([]byte, 24)
	if _, err = rand.Read(challengeBytes); err != nil {
		return rejected, ErrHeld
	}
	challenge := hex.EncodeToString(challengeBytes)
	observation, err := s.ledger.ObserveLatest(ctx, s.store.binding, challenge)
	if err != nil || observation.Binding != s.store.binding || observation.Challenge != challenge ||
		observation.Head.Slot != nil || observation.Head.Phase != Completed || observation.Head.Frame.Phase != Completed ||
		!equivalent(observation.Head.Frame, frame) || frame.Sequence == ^uint64(0) {
		return rejected, ErrHeld
	}
	want := Reservation{Binding: s.store.binding, Before: frame, Request: req, Authorization: frozen, Sequence: frame.Sequence + 1}
	uncertain := func(phase Phase, cause error) (Result, error) {
		journalErr := s.store.quarantine(ctx, req.ID, phase)
		return Result{Outcome: "UNKNOWN", Attempt: req.ID, Phase: phase}, errors.Join(ErrUnknown, cause, journalErr)
	}
	reserved, err := s.ledger.Reserve(ctx, want)
	if err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrHeld) {
			return rejected, err
		}
		return uncertain(Reserved, err)
	}
	if !same(reserved, want) {
		return uncertain(Reserved, ErrHeld)
	}
	committed, err := s.store.apply(ctx, reserved, s.authorizer, effect)
	if err != nil {
		return uncertain(CPCommitted, err)
	}
	receipt, err := s.ledger.Finalize(ctx, committed)
	if err != nil || !same(receipt.Commit, committed) || receipt.LedgerSequence != reserved.Sequence {
		return uncertain(Finalized, err)
	}
	saved, err := s.store.saveReceipt(ctx, receipt)
	if err != nil {
		return uncertain(ReceiptSaved, err)
	}
	if err = s.ledger.Complete(ctx, saved); err != nil {
		return uncertain(Completed, err)
	}
	return Result{Outcome: "COMPLETE", Attempt: req.ID, Phase: Completed}, nil
}

// Inspect is a separately authorized, read-only observation. It does not clear
// quarantine, complete a slot, replay a callback, consume Approval or reissue rights.
func (s *Service) Inspect(ctx context.Context, intent Intent, id string) (Inspection, error) {
	if s == nil || ctx == nil || ctx.Err() != nil || !validID(id) || intent.Validate() != nil ||
		intent.Mode != Read || intent.Operation != "lifecycle.inspect" {
		return Inspection{}, ErrHeld
	}
	auth, err := s.authorizer.Resolve(ctx, intent)
	if err != nil || validateAuthorization(auth, intent) != nil {
		return Inspection{}, ErrHeld
	}
	cp, receipt, quarantined, err := s.store.inspect(ctx, id, auth, s.authorizer)
	if err != nil {
		return Inspection{}, err
	}
	ledger, err := s.ledger.InspectAttempt(ctx, s.store.binding, id)
	if err != nil || ledger.Reservation.Binding != s.store.binding || ledger.Reservation.Request.ID != id {
		return Inspection{}, ErrHeld
	}
	r := ledger.Reservation
	if r.Request.Validate() != nil || validateAuthorization(r.Authorization, r.Request.Intent) != nil ||
		r.Before.Binding != r.Binding || r.Before.Checkpoint.Validate() != nil ||
		r.Before.Sequence == ^uint64(0) || r.Sequence != r.Before.Sequence+1 {
		return Inspection{}, ErrHeld
	}
	if cp != nil && !same(cp.Reservation, ledger.Reservation) {
		return Inspection{}, ErrHeld
	}
	switch ledger.Phase {
	case Reserved:
		if ledger.Commit != nil || ledger.Receipt != nil || receipt != nil {
			return Inspection{}, ErrHeld
		}
	case Finalized, Completed:
		if cp == nil || ledger.Commit == nil || !same(*ledger.Commit, *cp) || ledger.Receipt == nil {
			return Inspection{}, ErrHeld
		}
		if receipt != nil && !same(*receipt, *ledger.Receipt) {
			return Inspection{}, ErrHeld
		}
		if ledger.Phase == Completed && receipt == nil {
			return Inspection{}, ErrHeld
		}
	default:
		return Inspection{}, ErrHeld
	}
	return Inspection{Ledger: ledger, CP: cp, Receipt: receipt, Quarantined: quarantined}, nil
}
