// Package lifecyclefoundation contains the opt-in S12-B0 participation contract.
// No product entrypoint constructs it. It neither grants authority nor issues Evidence.
package lifecyclefoundation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrHeld     = errors.New("lifecycle held")
	ErrConflict = errors.New("lifecycle conflict")
	ErrUnknown  = errors.New("lifecycle outcome unknown")
)

const SyntheticProfile = "b0-synthetic-v1"

type Phase string

const (
	Reserved     Phase = "RESERVED"
	CPCommitted  Phase = "CP_COMMITTED"
	Finalized    Phase = "FINALIZED"
	ReceiptSaved Phase = "RECEIPT_SAVED"
	Completed    Phase = "COMPLETED"
)

type Mode string

const (
	Read  Mode = "READ"
	Write Mode = "WRITE"
)

type Binding struct {
	Deployment    string
	Ledger        string
	Store         string
	Incarnation   string
	Protocol      string
	Coverage      string
	RootReference string
}

type Checkpoint struct {
	OperationalRevision uint64
	AuthorityEpoch      uint64
	IdentityRevision    uint64
	Count               uint64
	Content             string
	HistoryHead         string
}

type Scope struct {
	Type string
	ID   string
}

type Intent struct {
	Mode             Mode
	Purpose          string
	Operation        string
	OperationContent string
	Scope            Scope
	Subject          string
}

type Request struct {
	ID     string
	Intent Intent
}

type Approval struct {
	Origin   string
	ID       string
	Manifest string
	Purpose  string
}

type Authorization struct {
	// Supplied only by the explicitly configured trusted local authorizer.
	Actor         string
	PrincipalKind string
	Intent        Intent
	Approval      *Approval
}

// Authorizer is a trusted composition dependency, not an HTTP DTO decoder.
// ValidateTx must use the existing local transaction; it must perform no external I/O.
type Authorizer interface {
	Resolve(context.Context, Intent) (Authorization, error)
	ValidateTx(context.Context, *sql.Tx, Authorization, Checkpoint) error
}

type Frame struct {
	Binding    Binding
	Sequence   uint64
	Checkpoint Checkpoint
	Attempt    string
	Phase      Phase
}

type Reservation struct {
	Binding       Binding
	Before        Frame
	Request       Request
	Authorization Authorization
	Sequence      uint64
}

type CommitRecord struct {
	Reservation Reservation
	After       Checkpoint
}

type Receipt struct {
	Commit         CommitRecord
	LedgerSequence uint64
}

type receiptConfirmation struct {
	db   *sql.DB
	body string
}

// SavedReceipt can only be minted after the CP Receipt Commit was acknowledged.
// Encoding a Finalize receipt cannot manufacture this private confirmation.
type SavedReceipt struct {
	Receipt Receipt
	proof   *receiptConfirmation
}

func (r SavedReceipt) Validate() error {
	if r.proof == nil || r.proof.db == nil || r.proof.body != encode(r.Receipt) {
		return ErrHeld
	}
	return nil
}

type Head struct {
	Frame Frame
	Slot  *Reservation
	Phase Phase
}

type Observation struct {
	Binding   Binding
	Challenge string
	Head      Head
}

type Attempt struct {
	Reservation Reservation
	Phase       Phase
	Commit      *CommitRecord
	Receipt     *Receipt
}

type Port interface {
	ObserveLatest(context.Context, Binding, string) (Observation, error)
	Reserve(context.Context, Reservation) (Reservation, error)
	Finalize(context.Context, CommitRecord) (Receipt, error)
	Complete(context.Context, SavedReceipt) error
	InspectAttempt(context.Context, Binding, string) (Attempt, error)
}

// RecoveryContract defines purposes only. B0 provides no Abort/Reconcile implementation.
type RecoveryContract struct {
	Purpose                      string
	Attempt                      string
	WriterFence                  string
	IndependentApprovalReference string
}

type Result struct {
	Outcome string
	Attempt string
	Phase   Phase
}

type Inspection struct {
	Ledger      Attempt
	CP          *CommitRecord
	Receipt     *Receipt
	Quarantined bool
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 96 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:", c)) {
			return false
		}
	}
	return true
}

func (b Binding) Validate() error {
	if b.Protocol != SyntheticProfile || !validID(b.Deployment) || !validID(b.Ledger) ||
		!validID(b.Store) || !validID(b.Incarnation) || !validID(b.Coverage) || !validID(b.RootReference) {
		return ErrHeld
	}
	return nil
}

func (c Checkpoint) Validate() error {
	// Commitments are opaque B0 fixture references, not an adopted cryptographic profile.
	if c.OperationalRevision == 0 || c.AuthorityEpoch == 0 || c.IdentityRevision == 0 ||
		!validID(c.Content) || !validID(c.HistoryHead) {
		return ErrHeld
	}
	return nil
}

func (i Intent) Validate() error {
	if i.Mode != Read && i.Mode != Write {
		return ErrHeld
	}
	if !validID(i.Operation) || !validID(i.OperationContent) || !validID(i.Subject) {
		return ErrHeld
	}
	switch i.Purpose {
	case "CURRENT_READ":
		if i.Mode != Read {
			return ErrHeld
		}
	case "AUTHORITY_CHANGE", "OPERATIONAL_CHANGE", "IDENTITY_CHANGE":
		if i.Mode != Write {
			return ErrHeld
		}
	default:
		return ErrHeld
	}
	switch i.Scope.Type {
	case "GLOBAL":
		if i.Scope.ID != "" {
			return ErrHeld
		}
	case "REALM", "HOST", "RESOURCE", "STREAM", "PROFILE", "INTEGRATION", "SECRET", "INCIDENT":
		if !validID(i.Scope.ID) {
			return ErrHeld
		}
	default:
		return ErrHeld
	}
	return nil
}

func (r Request) Validate() error {
	if !validID(r.ID) {
		return ErrHeld
	}
	return r.Intent.Validate()
}

func validateAuthorization(a Authorization, i Intent) error {
	if !validID(a.Actor) || a.PrincipalKind != "HUMAN" || a.Intent != i {
		return ErrHeld
	}
	if a.Approval != nil && (!validID(a.Approval.Origin) || !validID(a.Approval.ID) ||
		!validID(a.Approval.Manifest) || a.Approval.Purpose != i.Purpose) {
		return ErrHeld
	}
	return nil
}

func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("non-serializable lifecycle internal value")
	}
	return string(b)
}

func decode(s string, v any) error {
	d := json.NewDecoder(strings.NewReader(s))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrHeld
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrHeld
	}
	// The internal SQL representation is closed: duplicates, unknown fields,
	// alternate encodings and trailing values are not accepted as current state.
	if !bytes.Equal([]byte(s), []byte(encode(v))) {
		return ErrHeld
	}
	return nil
}

func same(a, b any) bool { return encode(a) == encode(b) }

func held(err error) error { return fmt.Errorf("%w: %v", ErrHeld, err) }
