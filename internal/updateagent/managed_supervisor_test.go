package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type supervisorPolicySource struct{}

func (supervisorPolicySource) FetchManagedPolicy(_ context.Context, _ string, current int64) (*ManagedPolicy, bool, error) {
	switch current {
	case 0:
		return &ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}, true, nil
	case 1:
		return &ManagedPolicy{UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now()}, true, nil
	default:
		return nil, false, nil
	}
}

type singleSupervisorPolicySource struct{}

func (singleSupervisorPolicySource) FetchManagedPolicy(_ context.Context, _ string, current int64) (*ManagedPolicy, bool, error) {
	if current != 0 {
		return nil, false, nil
	}
	return &ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}, true, nil
}

type supervisorPolicySourceFunc func(context.Context, string, int64) (*ManagedPolicy, bool, error)

func (f supervisorPolicySourceFunc) FetchManagedPolicy(ctx context.Context, serviceID string, currentRevision int64) (*ManagedPolicy, bool, error) {
	return f(ctx, serviceID, currentRevision)
}

type supervisorCoordinator struct {
	revision         int64
	events           *[]string
	mu               *sync.Mutex
	started          chan struct{}
	blockReplacement bool
	replacementReady <-chan struct{}
	runErr           error
	runtimeExit      <-chan error
}

func (c *supervisorCoordinator) Run(ctx context.Context) error {
	done, err := c.StartManaged(ctx)
	if err != nil {
		return err
	}
	return <-done
}

func (c *supervisorCoordinator) StartManaged(ctx context.Context) (<-chan error, error) {
	c.mu.Lock()
	*c.events = append(*c.events, eventName("start", c.revision))
	c.mu.Unlock()
	if c.started != nil {
		close(c.started)
	}
	if c.runErr != nil {
		return nil, c.runErr
	}
	done := make(chan error, 1)
	go func() {
		var runErr error
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case runErr = <-c.runtimeExit:
		}
		c.mu.Lock()
		*c.events = append(*c.events, eventName("stop", c.revision))
		c.mu.Unlock()
		done <- runErr
		close(done)
	}()
	return done, nil
}

type scriptedSupervisorFetch struct {
	policy  *ManagedPolicy
	changed bool
	err     error
}

type scriptedSupervisorPolicySource struct {
	mu      sync.Mutex
	fetches []scriptedSupervisorFetch
	calls   int
}

func (s *scriptedSupervisorPolicySource) FetchManagedPolicy(context.Context, string, int64) (*ManagedPolicy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	s.calls++
	if index >= len(s.fetches) {
		index = len(s.fetches) - 1
	}
	fetch := s.fetches[index]
	return fetch.policy, fetch.changed, fetch.err
}

func (s *scriptedSupervisorPolicySource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (c *supervisorCoordinator) SetPolicyState(_, desiredRevision int64, status, _ string, _ map[string]string, _ map[string]string) {
	c.mu.Lock()
	*c.events = append(*c.events, eventName("status-"+status, desiredRevision))
	c.mu.Unlock()
}

func (c *supervisorCoordinator) ActivatePolicy() {
	c.mu.Lock()
	*c.events = append(*c.events, eventName("activate", c.revision))
	c.mu.Unlock()
}

func (c *supervisorCoordinator) BeginPolicyReplacement(context.Context) (func(bool), bool, error) {
	c.mu.Lock()
	*c.events = append(*c.events, eventName("drain", c.revision))
	c.mu.Unlock()
	if c.blockReplacement {
		return nil, false, nil
	}
	if c.replacementReady != nil {
		select {
		case <-c.replacementReady:
		default:
			return nil, false, nil
		}
	}
	return func(bool) {}, true, nil
}

func (c *supervisorCoordinator) AbortPolicyReplacement() {}

func supervisorRuntimeRevision(cfg Config) int64 {
	if cfg.PolicyStatus == PolicyStatusPending {
		return cfg.PolicyDesiredRevision
	}
	return cfg.PolicyRevision
}

func eventName(action string, revision int64) string {
	return action + "-" + string(rune('0'+revision))
}

func TestManagedSupervisorAppliesHigherRevisionAfterOldRunExits(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	var mu sync.Mutex
	var events []string
	snapshot := &memoryPolicySnapshot{events: &events, mu: &mu}
	secondStarted := make(chan struct{})
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       supervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			if cfg.PolicyStatus != PolicyStatusPending || cfg.PolicyDesiredRevision != cfg.PolicyRevision+1 {
				t.Errorf("candidate runtime was not pending before snapshot commit: %+v", cfg)
			}
			started := make(chan struct{})
			if supervisorRuntimeRevision(cfg) == 2 {
				started = secondStarted
			}
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu, started: started}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second policy revision was not started")
	}
	waitForSupervisorEvent(t, &mu, &events, "status-applied-2")
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	positions := map[string]int{}
	for index, event := range events {
		positions[event] = index
	}
	for _, required := range []string{"start-2", "snapshot-2", "status-applied-2", "activate-2"} {
		if _, ok := positions[required]; !ok {
			t.Fatalf("candidate lifecycle event %q is missing: %v", required, events)
		}
	}
	if positions["drain-1"] >= positions["stop-1"] || positions["stop-1"] >= positions["start-2"] {
		t.Fatalf("replacement order = %v", events)
	}
	if positions["start-2"] >= positions["snapshot-2"] ||
		positions["snapshot-2"] >= positions["status-applied-2"] ||
		positions["status-applied-2"] >= positions["activate-2"] {
		t.Fatalf("candidate was activated before its ready runtime and snapshot were committed: %v", events)
	}
}

func TestManagedSupervisorAppliesInitialRevisionWithoutSSHReachabilityGate(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	var mu sync.Mutex
	var events []string
	started := make(chan struct{})
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       singleSupervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu, started: started}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial policy did not become active before host reachability was available")
	}
	waitForSupervisorEvent(t, &mu, &events, "status-applied-1")
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if !containsEvent(events, "status-applied-1") || !containsEvent(events, "start-1") {
		t.Fatalf("initial policy status = %v", events)
	}
}

func TestManagedSupervisorKeepsOldCoordinatorOnCandidateMaterializationFailure(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	var mu sync.Mutex
	var events []string
	firstStarted := make(chan struct{})
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       supervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			if policy.Revision == 2 {
				return Config{}, context.DeadlineExceeded
			}
			return Config{PolicyRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu, started: firstStarted}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first coordinator did not start")
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		snapshot := append([]string(nil), events...)
		mu.Unlock()
		if containsEvent(snapshot, "status-failed-2") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("materialization failure status was not reported: %v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if containsEvent(events, "start-2") || containsEvent(events, "drain-1") {
		t.Fatalf("invalid candidate replaced or drained old coordinator: %v", events)
	}
}

func TestManagedSupervisorRestoresAppliedStatusAfterTransientFetchFailure(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	policy := &ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{
		{policy: policy, changed: true},
		{err: errors.New("temporary network error")},
		{},
	}}
	var mu sync.Mutex
	var events []string
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 5 * time.Millisecond,
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for source.callCount() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	failedAt := -1
	appliedAfterFailure := false
	for index, event := range events {
		if event == "status-failed-1" {
			failedAt = index
		}
		if event == "status-applied-1" && failedAt >= 0 && index > failedAt {
			appliedAfterFailure = true
		}
	}
	if failedAt < 0 || !appliedAfterFailure {
		t.Fatalf("policy status did not recover after successful unchanged fetch: %v", events)
	}
}

func TestManagedSupervisorDoesNotClearKnownHigherRevisionFailureOnUnchangedFetch(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	revision1 := &ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	revision2 := &ManagedPolicy{UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now()}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{
		{policy: revision1, changed: true},
		{policy: revision2, changed: true},
		{},
	}}
	var mu sync.Mutex
	var events []string
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 5 * time.Millisecond,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			if policy.Revision == 2 {
				return Config{}, errors.New("invalid desired policy")
			}
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for source.callCount() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	failedAt := -1
	for index, event := range events {
		if event == "status-failed-2" {
			failedAt = index
		}
		if failedAt >= 0 && index > failedAt && event == "status-applied-1" {
			t.Fatalf("unchanged fetch cleared a known higher desired revision failure: %v", events)
		}
	}
	if failedAt < 0 {
		t.Fatalf("higher desired revision failure was not reported: %v", events)
	}
}

func TestManagedSupervisorHeartbeatsBootstrapWhilePolicyIsUnavailable(t *testing.T) {
	heartbeats := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/services/heartbeat" {
			http.NotFound(w, request)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		heartbeats <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{err: errors.New("panel unavailable")}}}
	supervisor := NewManagedSupervisor(Config{
		PanelURL: server.URL, NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	})
	supervisor.Policy = source
	supervisor.PollInterval = 5 * time.Millisecond
	supervisor.Snapshot = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case payload := <-heartbeats:
		if payload["service_id"] != "central-updater" || payload["status"] != "online" {
			t.Fatalf("bootstrap heartbeat = %#v", payload)
		}
		capabilities, _ := payload["capabilities"].(map[string]any)
		if _, exists := capabilities["policy_status"]; exists {
			t.Fatalf("unconfigured bootstrap heartbeat reported a false policy state: %#v", capabilities)
		}
	case <-time.After(time.Second):
		t.Fatal("bootstrap heartbeat was not sent while no coordinator was available")
	}
	cancel()
	<-done
}

func TestManagedSupervisorReportsInitialCandidateFailureAndKeepsPolling(t *testing.T) {
	heartbeats := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if request.URL.Path == "/services/heartbeat" {
			_ = json.NewDecoder(request.Body).Decode(&payload)
			heartbeats <- payload
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, request)
	}))
	defer server.Close()
	policy := &ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{policy: policy, changed: true}}}
	supervisor := NewManagedSupervisor(Config{
		PanelURL: server.URL, NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	})
	supervisor.Policy = source
	supervisor.PollInterval = 5 * time.Millisecond
	supervisor.Snapshot = nil
	supervisor.Materialize = func(policy ManagedPolicy, _ Config) (Config, error) {
		return Config{
			PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied,
			SSHClientPublicKeys:      map[string]string{"host-a": "ssh-ed25519 AAAATEST updater"},
			SSHClientKeyFingerprints: map[string]string{"host-a": "SHA256:public"},
		}, nil
	}
	supervisor.NewCoordinator = func(Config) (ManagedCoordinatorRuntime, error) {
		return nil, errors.New("listen failed with secret detail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case payload := <-heartbeats:
		capabilities, _ := payload["capabilities"].(map[string]any)
		if capabilities["policy_revision"] != float64(0) ||
			capabilities["policy_desired_revision"] != float64(1) ||
			capabilities["policy_status"] != PolicyStatusFailed ||
			capabilities["policy_error_code"] != PolicyErrorCoordinator {
			t.Fatalf("initial failure heartbeat = %#v", payload)
		}
		if _, ok := capabilities["ssh_client_public_keys"].(map[string]any); !ok {
			t.Fatalf("candidate public keys missing from failure heartbeat: %#v", capabilities)
		}
	case <-time.After(time.Second):
		t.Fatal("initial candidate failure was not reported by heartbeat")
	}
	select {
	case err := <-done:
		t.Fatalf("supervisor stopped instead of polling after initial failure: %v", err)
	default:
	}
	cancel()
	<-done
}

func TestManagedSupervisorRollsBackWhenCandidateRunFailsBeforeReady(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	snapshot := &memoryPolicySnapshot{policy: &applied}
	desired := &ManagedPolicy{UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now()}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{policy: desired, changed: true}}}
	var mu sync.Mutex
	var events []string
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			runtimeRevision := supervisorRuntimeRevision(cfg)
			runtime := &supervisorCoordinator{revision: runtimeRevision, events: &events, mu: &mu}
			if runtimeRevision == 2 {
				runtime.runErr = errors.New("candidate failed before readiness")
			}
			return runtime, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		startedOldTwice := 0
		for _, event := range events {
			if event == "start-1" {
				startedOldTwice++
			}
		}
		mu.Unlock()
		if startedOldTwice >= 2 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("supervisor stopped after candidate readiness failure: %v; events=%v", err, events)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	oldStarts := 0
	for _, event := range events {
		if event == "start-1" {
			oldStarts++
		}
	}
	if oldStarts < 2 || snapshot.policy == nil || snapshot.policy.Revision != 1 {
		t.Fatalf("candidate readiness failure did not preserve revision 1: events=%v snapshot=%+v saves=%v", events, snapshot.policy, snapshot.saves)
	}
}

type committedWarningSnapshot struct {
	mu          sync.Mutex
	policy      *ManagedPolicy
	saves       int
	warnOnFirst bool
}

func (s *committedWarningSnapshot) Load() (*ManagedPolicy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policy == nil {
		return nil, false, nil
	}
	copy := *s.policy
	return &copy, true, nil
}

func (s *committedWarningSnapshot) Save(policy ManagedPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := policy
	s.policy = &copy
	s.saves++
	if s.warnOnFirst {
		s.warnOnFirst = false
		return &managedPolicySnapshotSaveError{err: errors.New("directory sync uncertain"), committed: true}
	}
	return nil
}

func (s *committedWarningSnapshot) state() (*ManagedPolicy, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policy == nil {
		return nil, s.saves
	}
	copy := *s.policy
	return &copy, s.saves
}

type controlledPolicySnapshot struct {
	mu          sync.Mutex
	policy      *ManagedPolicy
	loadErr     error
	saveResults []error
	saves       []int64
	saveStarted chan int
	saveRelease <-chan struct{}
}

func (s *controlledPolicySnapshot) Load() (*ManagedPolicy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, false, s.loadErr
	}
	if s.policy == nil {
		return nil, false, nil
	}
	copy := *s.policy
	return &copy, true, nil
}

func (s *controlledPolicySnapshot) Save(policy ManagedPolicy) error {
	s.mu.Lock()
	call := len(s.saves) + 1
	var result error
	if call <= len(s.saveResults) {
		result = s.saveResults[call-1]
	}
	s.mu.Unlock()
	if s.saveStarted != nil {
		s.saveStarted <- call
	}
	if s.saveRelease != nil {
		<-s.saveRelease
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := policy
	s.policy = &copy
	s.saves = append(s.saves, policy.Revision)
	return result
}

func (s *controlledPolicySnapshot) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saves)
}

type failingPolicySnapshot struct {
	mu           sync.Mutex
	policy       *ManagedPolicy
	failRevision int64
	saveStarted  chan struct{}
	saveOnce     sync.Once
}

func (s *failingPolicySnapshot) Load() (*ManagedPolicy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policy == nil {
		return nil, false, nil
	}
	copy := *s.policy
	return &copy, true, nil
}

func (s *failingPolicySnapshot) Save(policy ManagedPolicy) error {
	if policy.Revision != s.failRevision {
		return nil
	}
	if s.saveStarted != nil {
		s.saveOnce.Do(func() { close(s.saveStarted) })
	}
	return errors.New("snapshot failed before commit")
}

func TestManagedSupervisorFailsClosedWhenLastAppliedSnapshotCannotBeLoaded(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	snapshot := &controlledPolicySnapshot{loadErr: errors.New("snapshot checksum invalid")}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{
		policy:  &ManagedPolicy{UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now()},
		changed: true,
	}}}
	constructed := 0
	supervisor := ManagedSupervisor{
		Bootstrap: bootstrap, Policy: source, PollInterval: 5 * time.Millisecond, Snapshot: snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(Config) (ManagedCoordinatorRuntime, error) {
			constructed++
			return nil, errors.New("must not construct after snapshot load failure")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := supervisor.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "last-applied snapshot unavailable") {
		t.Fatalf("snapshot load failure did not fail closed: %v", err)
	}
	if source.callCount() != 0 || constructed != 0 || snapshot.saveCount() != 0 {
		t.Fatalf("snapshot load failure fetched or replaced policy: fetches=%d constructed=%d saves=%d", source.callCount(), constructed, snapshot.saveCount())
	}
}

func TestManagedSupervisorFailsClosedWhenLastAppliedSnapshotCannotBeMaterialized(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{
		UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now(),
		Targets: []Target{{TargetID: "worker-old", HostID: "host-old", ServiceType: "worker", DeploymentMode: ModeSystemd}},
	}
	snapshot := &controlledPolicySnapshot{policy: &applied}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{
		policy:  &ManagedPolicy{UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now()},
		changed: true,
	}}}
	constructed := 0
	supervisor := ManagedSupervisor{
		Bootstrap: bootstrap, Policy: source, PollInterval: 5 * time.Millisecond, Snapshot: snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			if policy.Revision == 1 {
				return Config{}, errors.New("last-applied host identity is invalid")
			}
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(Config) (ManagedCoordinatorRuntime, error) {
			constructed++
			return nil, errors.New("must not construct after snapshot materialization failure")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := supervisor.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "last-applied snapshot materialization failed") {
		t.Fatalf("snapshot materialization failure did not fail closed: %v", err)
	}
	if source.callCount() != 0 || constructed != 0 || snapshot.saveCount() != 0 {
		t.Fatalf("snapshot materialization failure fetched or replaced policy: fetches=%d constructed=%d saves=%d", source.callCount(), constructed, snapshot.saveCount())
	}
}

func TestManagedSupervisorKeepsCommittedCandidateAndRetriesSnapshotDurability(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	snapshot := &committedWarningSnapshot{warnOnFirst: true}
	var mu sync.Mutex
	var events []string
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       singleSupervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(Config) error {
			mu.Lock()
			events = append(events, "prune-1")
			mu.Unlock()
			return nil
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		failed := -1
		recovered := false
		for index, event := range snapshotEvents {
			if event == "status-failed-1" {
				failed = index
			}
			if event == "status-applied-1" && failed >= 0 && index > failed {
				recovered = true
			}
		}
		_, saves := snapshot.state()
		if recovered && saves >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	failed := -1
	recovered := false
	recoveredAt := -1
	activatedAt := -1
	starts := 0
	for index, event := range events {
		if event == "start-1" {
			starts++
		}
		if event == "status-failed-1" {
			failed = index
		}
		if event == "status-applied-1" && failed >= 0 && index > failed && recoveredAt < 0 {
			recovered = true
			recoveredAt = index
		}
		if event == "activate-1" {
			activatedAt = index
		}
	}
	policy, saves := snapshot.state()
	if policy == nil || policy.Revision != 1 || saves < 2 || starts != 1 || !recovered ||
		activatedAt <= recoveredAt {
		t.Fatalf("post-rename durability handling = events:%v policy:%+v saves:%d", events, policy, saves)
	}
}

func TestManagedSupervisorKeepsRestartedRuntimeClosedUntilCommittedSnapshotIsDurable(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	snapshotStarted := make(chan int, 4)
	snapshotRelease := make(chan struct{})
	t.Cleanup(func() { close(snapshotRelease) })
	snapshot := &controlledPolicySnapshot{
		saveResults: []error{
			&managedPolicySnapshotSaveError{err: errors.New("directory sync uncertain"), committed: true},
			errors.New("directory sync retry one failed"),
			errors.New("directory sync retry two failed"),
			nil,
		},
		saveStarted: snapshotStarted,
		saveRelease: snapshotRelease,
	}
	var mu sync.Mutex
	var events []string
	firstExit := make(chan error, 1)
	restarted := make(chan struct{})
	constructs := 0
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       singleSupervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(Config) error {
			mu.Lock()
			events = append(events, "prune-1")
			mu.Unlock()
			return nil
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			mu.Lock()
			defer mu.Unlock()
			constructs++
			runtime := &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}
			switch constructs {
			case 1:
				runtime.runtimeExit = firstExit
			case 2:
				runtime.started = restarted
			}
			return runtime, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	if call := <-snapshotStarted; call != 1 {
		t.Fatalf("first snapshot call = %d", call)
	}
	snapshotRelease <- struct{}{}
	waitForSupervisorEvent(t, &mu, &events, "status-failed-1")
	mu.Lock()
	if containsEvent(events, "activate-1") {
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		t.Fatalf("committed durability warning activated candidate: %v", snapshotEvents)
	}
	mu.Unlock()

	firstExit <- errors.New("candidate runtime crashed before directory sync was durable")
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("last-applied runtime was not restarted after durability-window crash")
	}
	for expectedCall := 2; expectedCall <= 4; expectedCall++ {
		select {
		case call := <-snapshotStarted:
			if call != expectedCall {
				t.Fatalf("snapshot retry call = %d, want %d", call, expectedCall)
			}
		case <-time.After(time.Second):
			t.Fatalf("snapshot retry %d did not start", expectedCall)
		}
		mu.Lock()
		activated := containsEvent(events, "activate-1")
		pruned := containsEvent(events, "prune-1")
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		if activated || pruned {
			t.Fatalf("identity prune or activation ran before snapshot retry %d completed: %v", expectedCall, snapshotEvents)
		}
		snapshotRelease <- struct{}{}
	}
	waitForSupervisorEvent(t, &mu, &events, "prune-1")
	waitForSupervisorEvent(t, &mu, &events, "activate-1")
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	pruneAt := -1
	activateAt := -1
	for index, event := range events {
		if event == "prune-1" {
			pruneAt = index
		}
		if event == "activate-1" {
			activateAt = index
		}
	}
	if pruneAt < 0 || activateAt <= pruneAt {
		t.Fatalf("durable retry did not prune before activation: %v", events)
	}
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func waitForSupervisorEvent(t *testing.T, mu *sync.Mutex, events *[]string, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		found := containsEvent(*events, want)
		mu.Unlock()
		if found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("supervisor event %q not observed: %v", want, *events)
}

type memoryPolicySnapshot struct {
	policy *ManagedPolicy
	saves  []int64
	events *[]string
	mu     *sync.Mutex
}

func (s *memoryPolicySnapshot) Load() (*ManagedPolicy, bool, error) {
	if s.policy == nil {
		return nil, false, nil
	}
	copy := *s.policy
	return &copy, true, nil
}

func (s *memoryPolicySnapshot) Save(policy ManagedPolicy) error {
	s.saves = append(s.saves, policy.Revision)
	if s.events != nil && s.mu != nil {
		s.mu.Lock()
		*s.events = append(*s.events, eventName("snapshot", policy.Revision))
		s.mu.Unlock()
	}
	copy := policy
	s.policy = &copy
	return nil
}

func TestManagedSupervisorRestartsLastAppliedBeforeDesiredPolicyChange(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	snapshot := &memoryPolicySnapshot{policy: &applied}
	var mu sync.Mutex
	var events []string
	started := make(chan struct{})
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       supervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			mu.Lock()
			events = append(events, eventName("construct", supervisorRuntimeRevision(cfg)))
			mu.Unlock()
			return &supervisorCoordinator{
				revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu, started: started,
				blockReplacement: supervisorRuntimeRevision(cfg) == 1,
			}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("last-applied coordinator was not started")
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		if containsEvent(snapshotEvents, "drain-1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desired policy was not evaluated after restart: %v", snapshotEvents)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if !containsEvent(events, "start-1") || containsEvent(events, "construct-2") || containsEvent(events, "start-2") || len(snapshot.saves) != 0 {
		t.Fatalf("restart bypassed last-applied recovery boundary: events=%v saves=%v", events, snapshot.saves)
	}
}

func TestManagedSupervisorRestartsCrashedAppliedRuntimeBeforeRemovingItsTargets(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{
		UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now(),
		Targets: []Target{{TargetID: "worker-old", HostID: "host-old", ServiceType: "worker", DeploymentMode: ModeSystemd}},
	}
	var mu sync.Mutex
	var events []string
	snapshot := &memoryPolicySnapshot{policy: &applied, events: &events, mu: &mu}
	firstStarted := make(chan struct{})
	restarted := make(chan struct{})
	firstExit := make(chan error, 1)
	recoveryComplete := make(chan struct{})
	rev1Constructs := 0
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       supervisorPolicySource{},
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{
				PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied,
				Targets: append([]Target(nil), policy.Targets...),
			}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			revision := supervisorRuntimeRevision(cfg)
			mu.Lock()
			events = append(events, eventName("construct", revision))
			if revision == 2 && len(cfg.Targets) != 0 {
				mu.Unlock()
				t.Errorf("revision 2 unexpectedly retained removed targets: %+v", cfg.Targets)
				return nil, errors.New("revision 2 retained removed targets")
			}
			runtime := &supervisorCoordinator{revision: revision, events: &events, mu: &mu}
			if revision == 1 {
				rev1Constructs++
				runtime.replacementReady = recoveryComplete
				switch rev1Constructs {
				case 1:
					runtime.started = firstStarted
					runtime.runtimeExit = firstExit
				case 2:
					runtime.started = restarted
				}
			}
			mu.Unlock()
			return runtime, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("initial revision 1 runtime did not start")
	}
	waitForSupervisorEvent(t, &mu, &events, "drain-1")
	firstExit <- errors.New("revision 1 coordinator crashed")

	select {
	case <-restarted:
	case <-time.After(time.Second):
		cancel()
		mu.Lock()
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		t.Fatalf("last-applied revision was not restarted after crash: %v", snapshotEvents)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		snapshotEvents := append([]string(nil), events...)
		secondStart := -1
		drainAfterRestart := false
		for index, event := range snapshotEvents {
			if event == "start-1" {
				if secondStart >= 0 {
					continue
				}
				if containsEvent(snapshotEvents[:index], "start-1") {
					secondStart = index
				}
			}
			if secondStart >= 0 && index > secondStart && event == "drain-1" {
				drainAfterRestart = true
			}
		}
		unsafeReplacement := containsEvent(snapshotEvents, "construct-2") ||
			containsEvent(snapshotEvents, "start-2") ||
			containsEvent(snapshotEvents, "snapshot-2")
		mu.Unlock()
		if unsafeReplacement {
			cancel()
			t.Fatalf("revision 2 bypassed crashed revision 1 recovery: %v", snapshotEvents)
		}
		if drainAfterRestart {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("restarted revision 1 did not re-enter the recovery drain: %v", snapshotEvents)
		}
		time.Sleep(time.Millisecond)
	}

	close(recoveryComplete)
	waitForSupervisorEvent(t, &mu, &events, "snapshot-2")
	cancel()
	<-done
}

func TestManagedSupervisorDoesNotFetchWhileCrashedAppliedRuntimeCannotRestart(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	snapshot := &memoryPolicySnapshot{policy: &applied}
	var eventMu sync.Mutex
	var events []string
	firstStarted := make(chan struct{})
	firstExit := make(chan error, 1)
	firstFetchStarted := make(chan struct{})
	releaseFirstFetch := make(chan struct{})
	restartAttempts := make(chan struct{}, 8)
	var fetchMu sync.Mutex
	fetchCalls := 0
	source := supervisorPolicySourceFunc(func(ctx context.Context, _ string, _ int64) (*ManagedPolicy, bool, error) {
		fetchMu.Lock()
		fetchCalls++
		call := fetchCalls
		fetchMu.Unlock()
		if call == 1 {
			close(firstFetchStarted)
			select {
			case <-releaseFirstFetch:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
		}
		return nil, false, nil
	})
	constructs := 0
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 100 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{
				PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied,
			}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			eventMu.Lock()
			constructs++
			attempt := constructs
			events = append(events, eventName("construct", supervisorRuntimeRevision(cfg)))
			eventMu.Unlock()
			if attempt == 1 {
				return &supervisorCoordinator{
					revision: supervisorRuntimeRevision(cfg), events: &events, mu: &eventMu,
					started: firstStarted, runtimeExit: firstExit,
				}, nil
			}
			select {
			case restartAttempts <- struct{}{}:
			default:
			}
			return nil, errors.New("last-applied coordinator restart failed")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("initial revision 1 runtime did not start")
	}
	select {
	case <-firstFetchStarted:
	case <-time.After(time.Second):
		t.Fatal("initial policy fetch did not start")
	}
	firstExit <- errors.New("revision 1 coordinator crashed")
	close(releaseFirstFetch)

	for attempt := 0; attempt < 3; attempt++ {
		select {
		case <-restartAttempts:
		case <-time.After(time.Second):
			eventMu.Lock()
			snapshotEvents := append([]string(nil), events...)
			eventMu.Unlock()
			t.Fatalf("last-applied restart attempt %d was not observed: %v", attempt+1, snapshotEvents)
		}
	}
	fetchMu.Lock()
	gotFetchCalls := fetchCalls
	fetchMu.Unlock()
	if gotFetchCalls != 1 {
		t.Fatalf("policy fetch resumed while last-applied runtime could not restart: calls=%d", gotFetchCalls)
	}

	cancel()
	<-done
}

func TestManagedSupervisorPrunesRemovedIdentityOnlyAfterSnapshotCommit(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{
		UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now(),
		Hosts: []ManagedPolicyHost{{HostID: "host-old"}},
	}
	desired := &ManagedPolicy{
		UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now(),
		Hosts: []ManagedPolicyHost{{HostID: "host-new"}},
	}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{policy: desired, changed: true}}}
	var mu sync.Mutex
	var events []string
	oldIdentityExists := true
	snapshot := &memoryPolicySnapshot{policy: &applied, events: &events, mu: &mu}
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(cfg Config) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, eventName("prune", cfg.PolicyRevision))
			if cfg.PolicyRevision == 2 {
				if snapshot.policy == nil || snapshot.policy.Revision != 2 {
					t.Error("removed identity prune ran before revision 2 snapshot commit")
				}
				oldIdentityExists = false
			}
			return nil
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	waitForSupervisorEvent(t, &mu, &events, "activate-2")
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if oldIdentityExists {
		t.Fatal("removed host identity was not pruned after snapshot commit")
	}
	positions := make(map[string]int)
	for index, event := range events {
		positions[event] = index
	}
	for _, required := range []string{"snapshot-2", "prune-2", "status-applied-2", "activate-2"} {
		if _, exists := positions[required]; !exists {
			t.Fatalf("identity prune lifecycle event %q is missing: %v", required, events)
		}
	}
	if positions["snapshot-2"] >= positions["prune-2"] ||
		positions["prune-2"] >= positions["status-applied-2"] ||
		positions["status-applied-2"] >= positions["activate-2"] {
		t.Fatalf("removed identity prune order is unsafe: %v", events)
	}
}

func TestManagedSupervisorPreservesRemovedIdentityWhenSnapshotFailsBeforeCommit(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{
		UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now(),
		Hosts: []ManagedPolicyHost{{HostID: "host-old"}},
	}
	desired := &ManagedPolicy{
		UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now(),
		Hosts: []ManagedPolicyHost{{HostID: "host-new"}},
	}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{policy: desired, changed: true}}}
	snapshotSaved := make(chan struct{})
	snapshot := &failingPolicySnapshot{policy: &applied, failRevision: 2, saveStarted: snapshotSaved}
	var mu sync.Mutex
	var events []string
	oldIdentityExists := true
	prunedRevision2 := false
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 50 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(cfg Config) error {
			if cfg.PolicyRevision == 2 {
				prunedRevision2 = true
				oldIdentityExists = false
			}
			return nil
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-snapshotSaved:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("candidate snapshot save was not attempted")
	}
	cancel()
	<-done
	if prunedRevision2 || !oldIdentityExists {
		t.Fatal("removed host identity was pruned before snapshot commit")
	}
}

func TestManagedSupervisorRetriesCommittedIdentityPruneBeforeActivationAfterCrash(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	var mu sync.Mutex
	var events []string
	snapshot := &memoryPolicySnapshot{events: &events, mu: &mu}
	firstStarted := make(chan struct{})
	restarted := make(chan struct{})
	firstExit := make(chan error, 1)
	firstPruneStarted := make(chan struct{})
	retryPruneStarted := make(chan struct{})
	releaseFirstPrune := make(chan struct{})
	releaseRetryPrune := make(chan struct{})
	pruneCalls := 0
	constructs := 0
	policy := &ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{
		{policy: policy, changed: true},
		{},
	}}
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 20 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(Config) error {
			pruneCalls++
			switch pruneCalls {
			case 1:
				close(firstPruneStarted)
				<-releaseFirstPrune
				return errors.New("identity prune failed")
			case 2:
				close(retryPruneStarted)
				<-releaseRetryPrune
				return nil
			default:
				return nil
			}
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			constructs++
			runtime := &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}
			switch constructs {
			case 1:
				runtime.started = firstStarted
				runtime.runtimeExit = firstExit
			case 2:
				runtime.started = restarted
			}
			return runtime, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("committed candidate did not start")
	}
	select {
	case <-firstPruneStarted:
	case <-time.After(time.Second):
		t.Fatal("post-commit identity prune did not start")
	}
	mu.Lock()
	if !containsEvent(events, "snapshot-1") || containsEvent(events, "activate-1") {
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		t.Fatalf("candidate activated before identity prune result: %v", snapshotEvents)
	}
	mu.Unlock()
	firstExit <- errors.New("candidate crashed while identity prune was pending")
	close(releaseFirstPrune)
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("committed runtime was not restarted after prune failure and crash")
	}
	select {
	case <-retryPruneStarted:
	case <-time.After(time.Second):
		t.Fatal("committed identity prune was not retried after restart")
	}
	mu.Lock()
	if containsEvent(events, "activate-1") {
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		t.Fatalf("restarted runtime activated before identity prune retry succeeded: %v", snapshotEvents)
	}
	mu.Unlock()
	if source.callCount() != 1 {
		t.Fatalf("policy fetch resumed while committed identity prune was failing: calls=%d", source.callCount())
	}
	close(releaseRetryPrune)
	waitForSupervisorEvent(t, &mu, &events, "activate-1")
	cancel()
	<-done
}

func TestManagedSupervisorRetriesCommittedSnapshotPruneOnStartupBeforeActivation(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{UpdaterID: "central-updater", Revision: 1, UpdatedAt: time.Now()}
	snapshot := &memoryPolicySnapshot{policy: &applied}
	var mu sync.Mutex
	var events []string
	firstPruneStarted := make(chan struct{})
	retryPruneStarted := make(chan struct{})
	releaseFirstPrune := make(chan struct{})
	releaseRetryPrune := make(chan struct{})
	pruneCalls := 0
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       singleSupervisorPolicySource{},
		PollInterval: 20 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(cfg Config) error {
			if cfg.PolicyRevision != 1 {
				t.Errorf("startup prune revision = %d, want 1", cfg.PolicyRevision)
			}
			pruneCalls++
			switch pruneCalls {
			case 1:
				close(firstPruneStarted)
				<-releaseFirstPrune
				return errors.New("startup identity prune failed")
			case 2:
				close(retryPruneStarted)
				<-releaseRetryPrune
				return nil
			default:
				return nil
			}
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			if cfg.PolicyStatus != PolicyStatusPending {
				t.Errorf("startup runtime status = %q, want pending", cfg.PolicyStatus)
			}
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-firstPruneStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup orphan identity prune did not start")
	}
	mu.Lock()
	if containsEvent(events, "activate-1") {
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		t.Fatalf("startup runtime activated before orphan prune: %v", snapshotEvents)
	}
	mu.Unlock()
	close(releaseFirstPrune)
	select {
	case <-retryPruneStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup orphan identity prune was not retried")
	}
	mu.Lock()
	if containsEvent(events, "activate-1") {
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		t.Fatalf("startup runtime activated before orphan prune retry succeeded: %v", snapshotEvents)
	}
	mu.Unlock()
	close(releaseRetryPrune)
	waitForSupervisorEvent(t, &mu, &events, "activate-1")
	cancel()
	<-done
}

func TestManagedSupervisorConfirmsLoadedSnapshotDurabilityBeforePruneOrActivation(t *testing.T) {
	bootstrap := Config{
		PanelURL: "https://panel.example.com", NodeID: "central-updater",
		RuntimeToken: "runtime-secret", ServiceName: "Central Updater",
	}
	applied := ManagedPolicy{UpdaterID: "central-updater", Revision: 2, UpdatedAt: time.Now()}
	snapshotStarted := make(chan int, 3)
	snapshotRelease := make(chan struct{})
	t.Cleanup(func() { close(snapshotRelease) })
	snapshot := &controlledPolicySnapshot{
		policy: &applied,
		saveResults: []error{
			errors.New("loaded snapshot directory sync retry one failed"),
			errors.New("loaded snapshot directory sync retry two failed"),
			nil,
		},
		saveStarted: snapshotStarted,
		saveRelease: snapshotRelease,
	}
	source := &scriptedSupervisorPolicySource{fetches: []scriptedSupervisorFetch{{}}}
	var mu sync.Mutex
	var events []string
	supervisor := ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       source,
		PollInterval: 5 * time.Millisecond,
		Snapshot:     snapshot,
		ReportStatus: func(context.Context, Config) error { return nil },
		Logf:         func(string, ...any) {},
		PruneSSHIdentities: func(Config) error {
			mu.Lock()
			events = append(events, "prune-2")
			mu.Unlock()
			return nil
		},
		Materialize: func(policy ManagedPolicy, _ Config) (Config, error) {
			return Config{PolicyRevision: policy.Revision, PolicyDesiredRevision: policy.Revision, PolicyStatus: PolicyStatusApplied}, nil
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			if cfg.PolicyStatus != PolicyStatusPending {
				t.Errorf("loaded snapshot runtime status = %q, want pending", cfg.PolicyStatus)
			}
			return &supervisorCoordinator{revision: supervisorRuntimeRevision(cfg), events: &events, mu: &mu}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	for expectedCall := 1; expectedCall <= 3; expectedCall++ {
		select {
		case call := <-snapshotStarted:
			if call != expectedCall {
				t.Fatalf("loaded snapshot durability call = %d, want %d", call, expectedCall)
			}
		case <-time.After(time.Second):
			t.Fatalf("loaded snapshot durability retry %d did not start", expectedCall)
		}
		mu.Lock()
		pruned := containsEvent(events, "prune-2")
		activated := containsEvent(events, "activate-2")
		snapshotEvents := append([]string(nil), events...)
		mu.Unlock()
		if pruned || activated || source.callCount() != 0 {
			t.Fatalf("loaded uncertain snapshot escaped fail-closed gate before retry %d: events=%v fetches=%d", expectedCall, snapshotEvents, source.callCount())
		}
		snapshotRelease <- struct{}{}
	}
	waitForSupervisorEvent(t, &mu, &events, "prune-2")
	waitForSupervisorEvent(t, &mu, &events, "activate-2")
	deadline := time.Now().Add(time.Second)
	for source.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if source.callCount() == 0 {
		t.Fatal("policy fetch did not resume after loaded snapshot became durable and identities were pruned")
	}
	cancel()
	<-done
}

func TestMaterializePolicyErrorCodeClassifiesBootstrapEnvelopeIdentityFailure(t *testing.T) {
	if got := materializePolicyErrorCode(errors.New("initialize bootstrap envelope identity: unsafe state directory")); got != PolicyErrorSSHIdentity {
		t.Fatalf("bootstrap envelope identity error code=%q", got)
	}
}
