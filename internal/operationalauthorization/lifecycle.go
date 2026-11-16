package operationalauthorization

import (
	"encoding/json"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

func (s *Service) WithLifecycleGate(g *lf.Gate) *Service {
	copy := *s
	if g == nil {
		g = lf.NewRequiredGate()
	}
	copy.lifecycle = g
	return &copy
}
func cloneDelegation(request DelegationRequest) DelegationRequest {
	body, _ := json.Marshal(request)
	var owned DelegationRequest
	_ = json.Unmarshal(body, &owned)
	return owned
}
