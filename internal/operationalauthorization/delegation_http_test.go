package operationalauthorization

import (
	"errors"
	"testing"
)

func TestOperationalDelegationExpectedRevision(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "roles.create", "streams.start")
	request := DelegationRequest{PrincipalID: "human-1", Action: "roles.create", Scope: operationalGlobal(), ExpectedRevision: 2,
		Grants: []Grant{{Operation: "streams.start", Scope: operationalGlobal(), Delegation: DelegationConstraints{Mode: DelegationSubset}}}}
	service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	if err := service.ValidateDelegation(t.Context(), request); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("generation mismatch: %v", err)
	}
	request.ExpectedRevision = 1
	if err := service.ValidateDelegation(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	service = NewService(operationalFixedStore{snapshot: snapshot, revision: 2})
	if err := service.ValidateDelegation(t.Context(), request); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("late revision change: %v", err)
	}
}

func TestOperationalDelegationSuperAdminExplicitConstraints(t *testing.T) {
	for _, mode := range []string{"legacy-subset", "unrepresented", "none", "revoked", "scope", "subject"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, _, _ := operationalFixture(t, "roles.assign", "streams.start")
			snapshot.Roles[0].Name = "super_admin"
			for i := range snapshot.Grants {
				if snapshot.Grants[i].Operation == "streams.start" {
					switch mode {
					case "none":
						snapshot.Grants[i].Delegation.Mode = DelegationNone
					case "revoked":
						snapshot.Grants[i].State = StateRevoked
					case "scope":
						snapshot.Grants[i].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
					case "subject":
						snapshot.Grants[i].Subjects = SubjectConstraints{IDs: []string{"human-2"}}
					}
				}
			}
			if mode == "unrepresented" {
				snapshot, _, _ = operationalFixture(t, "roles.assign")
				snapshot.Roles[0].Name = "super_admin"
			}
			service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
			request := DelegationRequest{PrincipalID: "human-1", Action: "roles.assign", SubjectID: "human-2", Scope: operationalGlobal(),
				Grants: []Grant{{PrincipalID: "human-2", Operation: "streams.start", Scope: operationalGlobal(), Delegation: DelegationConstraints{Mode: DelegationSubset}}}}
			err := service.ValidateDelegation(t.Context(), request)
			allow := mode == "legacy-subset" || mode == "unrepresented"
			if (err == nil) != allow {
				t.Fatalf("allow=%v err=%v", allow, err)
			}
		})
	}
}
