package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryAuthStore) Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeID)
	}
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeIDSnake)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[heartbeat.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if svc.TokenID != token.ID {
		return RegisteredService{}, ErrForbidden
	}
	activeToken, ok := s.serviceTokens[token.ID]
	if !ok || activeToken.RevokedAt != nil {
		return RegisteredService{}, ErrForbidden
	}
	if heartbeat.CurrentStreamID != "" && !s.isAssignedLocked(heartbeat.ServiceID, heartbeat.CurrentStreamID) {
		return RegisteredService{}, ErrForbidden
	}
	now := s.heartbeatNow().UTC()
	svc.Status = heartbeat.Status
	svc.LastHeartbeatAt = &now
	heartbeatCommit := truncateServiceReportedValue(strings.TrimSpace(heartbeat.Commit), 80)
	heartbeatBuildDate := truncateServiceReportedValue(strings.TrimSpace(heartbeat.BuildDate), 80)
	if heartbeat.CurrentStreamID != "" {
		svc.CurrentStreamID = heartbeat.CurrentStreamID
	}
	svc.Metrics = sanitizeServiceMetrics(heartbeat.Metrics)
	if strings.TrimSpace(heartbeat.Version) != "" {
		svc.Version = strings.TrimSpace(heartbeat.Version)
		svc.ReportedVersion = svc.Version
	}
	if heartbeatCommit != "" {
		svc.ReportedCommit = heartbeatCommit
	}
	if heartbeatBuildDate != "" {
		svc.ReportedBuildDate = heartbeatBuildDate
	}
	if len(heartbeat.Capabilities) > 0 {
		reportedCapabilities := sanitizeServiceCapabilities(heartbeat.Capabilities)
		if svc.ServiceType != "update_agent" {
			svc.Capabilities = reportedCapabilities
		}
		svc.ReportedCapabilities = reportedCapabilities
	}
	if strings.TrimSpace(heartbeat.Hostname) != "" {
		svc.ReportedHostname = strings.TrimSpace(heartbeat.Hostname)
	}
	if strings.TrimSpace(heartbeat.OS) != "" {
		svc.ReportedOS = strings.TrimSpace(heartbeat.OS)
	}
	if strings.TrimSpace(heartbeat.Arch) != "" {
		svc.ReportedArch = strings.TrimSpace(heartbeat.Arch)
	}
	if heartbeat.API != nil {
		apiHost := strings.TrimSpace(heartbeat.API.Host)
		if apiHost != "" && heartbeat.API.Port >= 1 && heartbeat.API.Port <= 65535 {
			svc.ReportedEndpoint = serviceEndpoint(apiHost, heartbeat.API.Port, heartbeat.API.SSLEnabled, "")
		}
	}
	if heartbeat.Version != "" || heartbeatCommit != "" || heartbeatBuildDate != "" || len(heartbeat.Capabilities) > 0 || heartbeat.Hostname != "" || heartbeat.OS != "" || heartbeat.Arch != "" || heartbeat.API != nil {
		svc.LastReportedAt = &now
	}
	svc.UpdatedAt = now
	s.services[svc.ServiceID] = svc
	s.recordMetricHistoryLocked(svc, now)
	return svc, nil
}

func (s *MemoryAuthStore) recordMetricHistoryLocked(service RegisteredService, observedAt time.Time) {
	cutoff := observedAt.Add(-3 * time.Hour)
	next := s.metricHistory[:0]
	for _, snapshot := range s.metricHistory {
		if snapshot.ObservedAt.After(cutoff) || snapshot.ObservedAt.Equal(cutoff) {
			next = append(next, snapshot)
		}
	}
	for name, raw := range service.Metrics {
		value, ok := serviceMetricSnapshotNumber(raw)
		if !ok {
			continue
		}
		next = append(next, ServiceMetricSnapshot{
			Name:        name,
			ServiceID:   service.ServiceID,
			ServiceType: service.ServiceType,
			Status:      service.Status,
			Value:       value,
			ObservedAt:  observedAt,
		})
	}
	s.metricHistory = next
}

func (s *MemoryAuthStore) ListServiceMetricSnapshots(ctx context.Context, since time.Time, maxPointsPerSeries int) ([]ServiceMetricSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-3 * time.Hour)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	series := map[string][]ServiceMetricSnapshot{}
	for _, snapshot := range s.metricHistory {
		if snapshot.ObservedAt.Before(since) {
			continue
		}
		key := strings.Join([]string{snapshot.ServiceID, snapshot.ServiceType, snapshot.Name}, "\x00")
		series[key] = append(series[key], snapshot)
	}
	if maxPointsPerSeries <= 0 {
		maxPointsPerSeries = 360
	}
	out := make([]ServiceMetricSnapshot, 0)
	for _, points := range series {
		sort.SliceStable(points, func(i, j int) bool { return points[i].ObservedAt.Before(points[j].ObservedAt) })
		if len(points) <= maxPointsPerSeries {
			out = append(out, points...)
			continue
		}
		for bucket := 0; bucket < maxPointsPerSeries; bucket++ {
			end := (bucket + 1) * len(points) / maxPointsPerSeries
			start := bucket * len(points) / maxPointsPerSeries
			if end > start {
				out = append(out, points[end-1])
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ServiceID+out[i].Name < out[j].ServiceID+out[j].Name
		}
		return out[i].ObservedAt.Before(out[j].ObservedAt)
	})
	return out, nil
}

func (s *MemoryAuthStore) UpdateServiceRuntimeReport(ctx context.Context, report ServiceRuntimeReport) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	report = normalizeServiceRuntimeReport(report)
	if report.ServiceID == "" {
		return RegisteredService{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[report.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	now := time.Now().UTC()
	if svc.Status == "pending" {
		svc.Status = "registered"
	}
	if report.Version != "" {
		svc.Version = report.Version
		svc.ReportedVersion = report.Version
	}
	if report.Commit != "" {
		svc.ReportedCommit = report.Commit
	}
	if report.BuildDate != "" {
		svc.ReportedBuildDate = report.BuildDate
	}
	if report.Hostname != "" {
		svc.ReportedHostname = report.Hostname
	}
	if report.OS != "" {
		svc.ReportedOS = report.OS
	}
	if report.Arch != "" {
		svc.ReportedArch = report.Arch
	}
	if report.Version != "" || report.Commit != "" || report.BuildDate != "" || report.Hostname != "" || report.OS != "" || report.Arch != "" {
		svc.LastReportedAt = &now
	}
	svc.UpdatedAt = now
	s.services[svc.ServiceID] = svc
	return svc, nil
}
