package lifecyclefoundation

import "testing"

func TestB2IdentityProjectionContract(t *testing.T) {
	valid := IdentityProjection{
		Revision: 1, OperationalRevision: 7, AuthorityEpoch: 3,
		ContentReference: "fixture-content", HistoryReference: "fixture-history", Coverage: IdentityFixtureCoverage,
	}
	if valid.Validate() != nil {
		t.Fatal("explicit synthetic projection rejected")
	}
	for name, mutate := range map[string]func(*IdentityProjection){
		"zero-identity-revision":    func(p *IdentityProjection) { p.Revision = 0 },
		"zero-operational-revision": func(p *IdentityProjection) { p.OperationalRevision = 0 },
		"zero-authority-epoch":      func(p *IdentityProjection) { p.AuthorityEpoch = 0 },
		"missing-content":           func(p *IdentityProjection) { p.ContentReference = "" },
		"missing-history":           func(p *IdentityProjection) { p.HistoryReference = "" },
		"secret-shaped-reference":   func(p *IdentityProjection) { p.ContentReference = "raw password value" },
		"production-coverage":       func(p *IdentityProjection) { p.Coverage = "production-secret-v1" },
		"qualified-fixture":         func(p *IdentityProjection) { p.Qualified = true },
	} {
		t.Run(name, func(t *testing.T) {
			p := valid
			mutate(&p)
			if p.Validate() == nil {
				t.Fatal("invalid or falsely qualified projection accepted")
			}
		})
	}
}
