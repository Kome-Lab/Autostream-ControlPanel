package store

import (
	"errors"
	"testing"
	"time"
)

func TestUI188CB15LeasePrecedesValueAndRollsBackMemory(t *testing.T) {
	f := cb15StoreFixture(t, nil)
	snapshot, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, lease, err := f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secrets := f.stores.Secrets.(*MemorySecretStore)
	secrets.mu.Lock()
	delete(secrets.values, f.secret.Name)
	secrets.mu.Unlock()
	if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
		t.Fatal("active lease did not precede missing value", err)
	}
	if err = f.stores.Leases.ReleaseRuntimeSecretLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing value accepted", err)
	}
	secrets.mu.Lock()
	secrets.values[f.secret.Name] = "synthetic-cb15-current-value"
	secrets.mu.Unlock()
	if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute); err != nil {
		t.Fatal("failed value left a lease", err)
	}
}

func TestUI188CB15LeasePrecedesDecryptAndRollsBackMariaDB(t *testing.T) {
	db := cb15TestDB(t)
	f := cb15StoreFixture(t, db)
	snapshot, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, lease, err := f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wrong := f.stores
	wrong.Secrets = NewMariaDBSecretStore(db, "cb15-deliberately-wrong-key")
	if _, _, err = wrong.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
		t.Fatal("decrypt error hid active lease", err)
	}
	if err = f.stores.Leases.ReleaseRuntimeSecretLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if _, _, err = wrong.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute); err == nil || errors.Is(err, ErrRuntimeSecretLeaseActive) {
		t.Fatal("wrong key accepted or premature lease remained", err)
	}
	if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute); err != nil {
		t.Fatal("decrypt failure did not roll back lease", err)
	}
}
