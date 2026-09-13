package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestMariaDBServiceTokenMutationsLockServiceBeforeToken(t *testing.T) {
	sourceBytes, err := readServiceRegistryLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, test := range []struct {
		name           string
		next           string
		lockHelper     string
		mutationPhases []string
	}{
		{name: "RevokeServiceToken", next: "RotateServiceToken", lockHelper: "lockMariaDBServiceTokenMutationRetryable(", mutationPhases: []string{"revokeServiceTokenInTx("}},
		{name: "RotateServiceToken", next: "RotateServiceNodeToken", lockHelper: "lockMariaDBServiceTokenMutationRetryable(", mutationPhases: []string{"INSERT INTO service_tokens"}},
		{name: "RotateServiceNodeToken", next: "requiresStagedNodeTokenRotation", mutationPhases: []string{"INSERT INTO service_tokens"}},
		{name: "ConfigureServiceNode", next: "StageServiceNodeConfiguration", mutationPhases: []string{"INSERT INTO service_tokens"}},
		{name: "stageServiceNodeConfigurationWithReferences", next: "ActivateServiceNodeConfiguration", lockHelper: "lockMariaDBServiceTokenMutationRetryable("},
		{name: "activateServiceNodeConfigurationWithReferences", next: "SetServiceNodeTokenSecret", lockHelper: "lockMariaDBServiceTokenMutationRetryable(", mutationPhases: []string{"INSERT INTO service_tokens"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mariaDBServiceTokenFunctionSource(t, source, test.name, test.next)
			lockHelper := test.lockHelper
			if lockHelper == "" {
				lockHelper = "lockMariaDBServiceTokenMutation("
			}
			lockPhase := strings.Index(body, lockHelper)
			if lockPhase < 0 {
				t.Fatalf("%s does not use the canonical service-token lock helper", test.name)
			}
			if directTokenLock := firstSourceIndex(body,
				"FROM service_tokens WHERE id = ? FOR UPDATE",
				"selectActiveServiceTokenForUpdate(",
				"selectServiceTokenForNodeConfiguration(",
			); directTokenLock >= 0 {
				t.Fatalf("%s bypasses the canonical service-token lock helper", test.name)
			}
			if mutationPhase := firstSourceIndex(body, test.mutationPhases...); mutationPhase >= 0 && lockPhase >= mutationPhase {
				t.Fatalf("%s mutates a token before the canonical lock helper", test.name)
			}
		})
	}

	helper := mariaDBServiceTokenFunctionSource(
		t,
		source,
		"lockMariaDBServiceTokenMutation",
		"mariaDBServiceTokenReferenceContains",
	)
	servicePhase := strings.Index(helper, "lockMariaDBServicesSorted(")
	tokenPhase := strings.Index(helper, "lockMariaDBServiceTokensSorted(")
	revalidationPhase := strings.Index(helper, "discoverMariaDBServiceTokenReferences(ctx, tx, tokenIDs)")
	if servicePhase < 0 || tokenPhase < 0 || revalidationPhase < 0 ||
		servicePhase >= tokenPhase || tokenPhase >= revalidationPhase {
		t.Fatalf("canonical helper order service=%d token=%d revalidation=%d", servicePhase, tokenPhase, revalidationPhase)
	}
}

func TestFIX009GenericTokenMutationsUseOnlyDiscoveredServiceRows(t *testing.T) {
	sourceBytes, err := readServiceRegistryLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, test := range []struct {
		name string
		next string
	}{
		{name: "RevokeServiceToken", next: "RotateServiceToken"},
		{name: "RotateServiceToken", next: "RotateServiceNodeToken"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mariaDBServiceTokenFunctionSource(t, source, test.name, test.next)
			if strings.Contains(body, "WHERE token_id = ?") {
				t.Fatalf("%s still scans or mutates future service bindings after locking the token", test.name)
			}
			if !strings.Contains(body, "WHERE service_id = ? AND token_id = ?") {
				t.Fatalf("%s does not constrain service mutation to an exact discovered service ID", test.name)
			}
			if !strings.Contains(body, "errMariaDBServiceTokenReferenceSetChanged") {
				t.Fatalf("%s does not retry a committed binding-set change", test.name)
			}
		})
	}
}

func TestMariaDBRuntimeTokenMutationsLockServiceBeforeToken(t *testing.T) {
	sourceBytes, err := os.ReadFile("system_update_runtime_token_rotations_mariadb.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, test := range []struct {
		name string
		next string
	}{
		{name: "stageSystemUpdateRuntimeTokenRotationOnce", next: "isMariaDBRuntimeTokenRotationDeadlock"},
		{name: "ClaimSystemUpdateRuntimeTokenRotationStagedCredential", next: "mariaDBRuntimeTokenRotationPolicyForUpdate"},
		{name: "MarkSystemUpdateRuntimeTokenRotationLocalStaged", next: "ProveSystemUpdateRuntimeTokenRotationHeartbeat"},
		{name: "ProveSystemUpdateRuntimeTokenRotationHeartbeat", next: "ActivateSystemUpdateRuntimeTokenRotation"},
		{name: "ActivateSystemUpdateRuntimeTokenRotation", next: "CancelSystemUpdateRuntimeTokenRotation"},
		{name: "CancelSystemUpdateRuntimeTokenRotation", next: "AcknowledgeSystemUpdateRuntimeTokenRotationCancel"},
		{name: "AcknowledgeSystemUpdateRuntimeTokenRotationCancel", next: "EmergencyRevokeSystemUpdateRuntimeToken"},
		{name: "EmergencyRevokeSystemUpdateRuntimeToken", next: "mariaDBRuntimeTokenRotationForTransition"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mariaDBRuntimeTokenFunctionSource(t, source, test.name, test.next)
			lockPhase := strings.Index(body, "lockMariaDBRuntimeTokenRotationPlan(")
			if lockPhase < 0 {
				t.Fatalf("%s does not use the canonical runtime service-token lock plan", test.name)
			}
			if tokenPhase := firstSourceIndex(
				body,
				"selectActiveServiceTokenForUpdate(",
				"mariaDBRuntimeServiceTokenForUpdate(",
				"mariaDBRuntimeTokenServiceReferencesForUpdate(",
				"INSERT INTO service_tokens",
				"UPDATE service_tokens",
			); tokenPhase >= 0 && lockPhase >= tokenPhase {
				t.Fatalf("%s reaches a token phase before the canonical runtime lock plan", test.name)
			}
		})
	}
}

func TestMariaDBPrecreateServiceEstablishesServiceBeforeTokenAndRevalidatesBinding(t *testing.T) {
	sourceBytes, err := readServiceRegistryLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	body := mariaDBServiceTokenFunctionSource(
		t,
		string(sourceBytes),
		"PrecreateService",
		"RegisterService",
	)
	transactionPhase := strings.Index(body, "BeginTx(")
	insertPhase := strings.Index(body, "INSERT INTO services")
	tokenPhase := strings.Index(body, "lockMariaDBServiceTokensSorted(")
	revalidationPhase := strings.Index(body, "discoverMariaDBServiceTokenReferences(ctx, tx")
	commitPhase := strings.Index(body, "tx.Commit()")
	if transactionPhase < 0 || insertPhase < 0 || tokenPhase < 0 ||
		revalidationPhase < 0 || commitPhase < 0 ||
		transactionPhase >= insertPhase || insertPhase >= tokenPhase ||
		tokenPhase >= revalidationPhase || revalidationPhase >= commitPhase {
		t.Fatalf(
			"precreate lock order transaction=%d insert=%d token=%d revalidation=%d commit=%d",
			transactionPhase, insertPhase, tokenPhase, revalidationPhase, commitPhase,
		)
	}
	if strings.Contains(body, "s.db.ExecContext(ctx, `INSERT INTO services") {
		t.Fatal("PrecreateService still inserts the token binding outside its transaction")
	}
}

func TestMariaDBDeactivatePullUpdaterOwnershipLocksAllServicesBeforeTokens(t *testing.T) {
	sourceBytes, err := readUpdaterPolicyLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership("
	nextMarker := "func attachUpdaterTargetDatabases("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("DeactivatePullUpdaterOwnership not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following DeactivatePullUpdaterOwnership not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	if !strings.Contains(body, "lockMariaDBServiceTokenMutation(") {
		t.Fatal("DeactivatePullUpdaterOwnership does not lock the complete service set before token rows")
	}
	if strings.Contains(body, "selectActiveServiceTokenForUpdate(") {
		t.Fatal("DeactivatePullUpdaterOwnership retains a token lock before its later legacy service lock")
	}
}

func TestMariaDBActivatePullUpdaterOwnershipLocksAllServicesBeforeTokens(t *testing.T) {
	sourceBytes, err := readUpdaterPolicyLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func (s MariaDBUpdaterPolicyStore) ActivatePullUpdaterOwnership("
	nextMarker := "func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("ActivatePullUpdaterOwnership not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following ActivatePullUpdaterOwnership not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	if strings.Count(body, "lockMariaDBServiceTokenMutation(") != 1 {
		t.Fatalf(
			"ActivatePullUpdaterOwnership canonical lock helper count = %d, want 1",
			strings.Count(body, "lockMariaDBServiceTokenMutation("),
		)
	}
	if strings.Contains(body, "serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`") {
		t.Fatal("ActivatePullUpdaterOwnership locks a service outside the global sorted service set")
	}
	discoveryPhase := strings.Index(body, "discoverMariaDBPullUpdaterOwnershipLockPlan(")
	transactionPhase := strings.Index(body, "BeginTx(")
	policyLockPhase := strings.Index(body, "lockMariaDBPullUpdaterOwnershipPolicies(")
	lockPhase := strings.Index(body, "lockMariaDBServiceTokenMutation(")
	if discoveryPhase < 0 || transactionPhase < 0 || policyLockPhase < 0 || lockPhase < 0 ||
		discoveryPhase >= transactionPhase || transactionPhase >= policyLockPhase ||
		policyLockPhase >= lockPhase {
		t.Fatalf(
			"ActivatePullUpdaterOwnership discovery=%d transaction=%d policy_lock=%d service_token_lock=%d",
			discoveryPhase, transactionPhase, policyLockPhase, lockPhase,
		)
	}
}

func TestMariaDBDeactivatePullUpdaterOwnershipLocksGlobalPoliciesBeforeServices(t *testing.T) {
	sourceBytes, err := readUpdaterPolicyLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership("
	nextMarker := "func attachUpdaterTargetDatabases("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("DeactivatePullUpdaterOwnership not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following DeactivatePullUpdaterOwnership not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	policyLockPhase := strings.Index(body, "lockMariaDBPullUpdaterOwnershipPolicies(")
	serviceLockPhase := strings.Index(body, "lockMariaDBServiceTokenMutation(")
	if policyLockPhase < 0 || serviceLockPhase < 0 || policyLockPhase >= serviceLockPhase {
		t.Fatalf(
			"DeactivatePullUpdaterOwnership policy_lock=%d service_token_lock=%d",
			policyLockPhase, serviceLockPhase,
		)
	}
	if strings.Contains(body, "mariaDBUpdaterPolicyForUpdate(") {
		t.Fatal("DeactivatePullUpdaterOwnership takes an individual policy lock outside the global policy set")
	}
}

func TestMariaDBPullUpdaterOwnershipPolicyServiceClosureIsTransitiveAndSorted(t *testing.T) {
	policies := []UpdaterPolicy{
		{UpdaterID: "policy-b", Targets: []UpdaterPolicyTarget{{ServiceID: "service-c"}}},
		{UpdaterID: "policy-a", Targets: []UpdaterPolicyTarget{{ServiceID: "policy-b"}}},
	}
	got := expandMariaDBUpdaterPolicyServiceClosure(policies, []string{"policy-a"})
	want := []string{"policy-a", "policy-b", "service-c"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("policy service closure = %#v, want %#v", got, want)
	}
}

func TestMariaDBUpdaterPolicyLockObserverNilIsNoOp(t *testing.T) {
	observeMariaDBUpdaterPolicyLockPhase(
		context.Background(),
		"activate_pull_updater_ownership",
		mariaDBUpdaterPolicyBeforePolicyLocks,
	)
}

func TestMariaDBFIX006CanonicalPairUsesLeadingFixtureNamespace(t *testing.T) {
	sourceBytes, err := os.ReadFile("service_token_lock_order_pair_fixture_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func " + "newMariaDBServiceTokenPairFixture("
	nextMarker := "type " + "mariaDBServiceTokenMutationResult struct"
	start := strings.Index(source, startMarker)
	end := strings.Index(source, nextMarker)
	if start < 0 || end <= start {
		t.Fatal("canonical pair fixture source was not found")
	}
	if strings.Contains(source[start:end], `serviceID := serviceType + "-" + suffix`) {
		t.Fatal("canonical pair service ID places the service type before the cleanup namespace")
	}
	if !strings.Contains(source[start:end], `serviceID := suffix + "-" + serviceType`) {
		t.Fatal("canonical pair service ID does not begin with the cleanup namespace")
	}
}

func TestMariaDBUnreferencedTokenCleanupDoesNotLockServiceAfterToken(t *testing.T) {
	sourceBytes, err := readServiceRegistryLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	body := mariaDBServiceTokenFunctionSource(
		t,
		string(sourceBytes),
		"revokeServiceTokenIfUnreferencedInTx",
		"AuthenticateServiceToken",
	)
	if strings.Contains(body, "FROM services") && strings.Contains(body, "FOR UPDATE") {
		t.Fatal("unreferenced-token cleanup locks a service row after callers already locked the token row")
	}
}

func TestMariaDBDeleteServiceRevalidatesAllTokenReferencesBeforeRevocation(t *testing.T) {
	sourceBytes, err := readServiceRegistryLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	body := mariaDBServiceTokenFunctionSource(
		t,
		string(sourceBytes),
		"DeleteService",
		"AssignServiceToStream",
	)
	const referenceDiscoveryCall = "discoverMariaDBServiceTokenReferences("
	discoveryPhase := strings.Index(body, referenceDiscoveryCall)
	servicePhase := strings.Index(body, "lockMariaDBServicesSorted(")
	assignmentPhase := strings.Index(body, "lockMariaDBAssignmentRowsSorted(")
	tokenPhase := strings.Index(body, "lockMariaDBServiceTokensSorted(")
	revalidationPhase := -1
	if discoveryPhase >= 0 {
		if offset := strings.Index(body[discoveryPhase+len(referenceDiscoveryCall):], referenceDiscoveryCall); offset >= 0 {
			revalidationPhase = discoveryPhase + len(referenceDiscoveryCall) + offset
		}
	}
	if discoveryPhase < 0 || servicePhase < 0 || assignmentPhase < 0 || tokenPhase < 0 || revalidationPhase < 0 ||
		servicePhase >= assignmentPhase || assignmentPhase >= tokenPhase || tokenPhase >= revalidationPhase {
		t.Fatalf(
			"delete lock order discovery=%d service=%d assignment=%d token=%d revalidation=%d",
			discoveryPhase, servicePhase, assignmentPhase, tokenPhase, revalidationPhase,
		)
	}
}

func TestMariaDBServiceTokenReferencePlanIsSortedAndFailClosed(t *testing.T) {
	references := []mariaDBServiceTokenReference{
		{ServiceID: "service-b", TokenID: "token-current"},
		{ServiceID: "service-a", StagedPreviousTokenID: "token-current", StagedTokenID: "token-staged"},
	}
	serviceIDs := mariaDBServiceTokenReferenceServiceIDs(references, " service-c ", "service-a")
	wantServiceIDs := []string{"service-a", "service-b", "service-c"}
	if len(serviceIDs) != len(wantServiceIDs) {
		t.Fatalf("service IDs = %#v, want %#v", serviceIDs, wantServiceIDs)
	}
	for index := range wantServiceIDs {
		if serviceIDs[index] != wantServiceIDs[index] {
			t.Fatalf("service IDs = %#v, want %#v", serviceIDs, wantServiceIDs)
		}
	}
	services := map[string]RegisteredService{
		"service-a": {
			ServiceID: "service-a", ServiceType: "worker",
			StagedNodePreviousTokenID: "token-current", StagedNodeTokenID: "token-staged",
		},
		"service-b": {ServiceID: "service-b", ServiceType: "worker", TokenID: "token-current"},
	}
	tokens := map[string]ServiceToken{
		"token-current": {ID: "token-current", ServiceType: "worker"},
		"token-staged":  {ID: "token-staged", ServiceType: "worker"},
	}
	if !mariaDBServiceTokenReferenceTypesMatch(references, services, tokens) {
		t.Fatal("valid current/staged reference plan was rejected")
	}
	changed := append([]mariaDBServiceTokenReference(nil), references...)
	changed[1].StagedTokenID = "token-raced"
	if mariaDBServiceTokenReferencesEqual(references, changed) {
		t.Fatal("staged token binding change was accepted")
	}
	tokens["token-current"] = ServiceToken{ID: "token-current", ServiceType: "encoder_recorder"}
	if mariaDBServiceTokenReferenceTypesMatch(references, services, tokens) {
		t.Fatal("service/token type mismatch was accepted")
	}
	if mariaDBServiceTokenReferencesUseCurrentToken(references, "token-current") {
		t.Fatal("staged-only token reference was accepted as a current-token rotation target")
	}
	if !mariaDBServiceTokenReferencesUseCurrentToken(
		[]mariaDBServiceTokenReference{{ServiceID: "service-b", TokenID: "token-current"}},
		"token-current",
	) {
		t.Fatal("current token reference was rejected")
	}
	if !mariaDBServiceTokenReferencesUseCurrentToken(nil, "token-unbound") {
		t.Fatal("unbound token was rejected")
	}
	if ids := mariaDBServiceTokenReferenceServiceIDs(nil); len(ids) != 0 {
		t.Fatalf("unbound token service IDs = %#v, want none", ids)
	}
}

func mariaDBServiceTokenFunctionSource(t *testing.T, source, name, next string) string {
	t.Helper()
	startMarker := "func (s MariaDBAuthStore) " + name + "("
	if strings.HasPrefix(name, "revokeServiceToken") || strings.HasPrefix(name, "lockMariaDBServiceToken") {
		startMarker = "func " + name + "("
	}
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	nextMarker := "func (s MariaDBAuthStore) " + next + "("
	if next == "requiresStagedNodeTokenRotation" || next == "mariaDBServiceTokenReferenceContains" {
		nextMarker = "func " + next + "("
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatalf("function following %s (%s) not found", name, next)
	}
	return source[start : start+len(startMarker)+endOffset]
}

func mariaDBRuntimeTokenFunctionSource(t *testing.T, source, name, next string) string {
	t.Helper()
	startMarkers := []string{
		"func (s *MariaDBSystemUpdateStore) " + name + "(",
		"func " + name + "(",
	}
	nextMarkers := []string{
		"func (s *MariaDBSystemUpdateStore) " + next + "(",
		"func " + next + "(",
	}
	start := firstSourceIndex(source, startMarkers...)
	if start < 0 {
		t.Fatalf("runtime token function %s not found", name)
	}
	nextOffset := firstSourceIndex(source[start+1:], nextMarkers...)
	if nextOffset < 0 {
		t.Fatalf("runtime token function following %s (%s) not found", name, next)
	}
	return source[start : start+1+nextOffset]
}

func firstSourceIndex(source string, needles ...string) int {
	first := -1
	for _, needle := range needles {
		if index := strings.Index(source, needle); index >= 0 && (first < 0 || index < first) {
			first = index
		}
	}
	return first
}
