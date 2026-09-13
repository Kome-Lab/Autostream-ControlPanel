package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s MariaDBAuthStore) Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error) {
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeID)
	}
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeIDSnake)
	}
	heartbeat.Version = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Version), 80)
	heartbeat.Commit = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Commit), 80)
	heartbeat.BuildDate = truncateServiceReportedValue(strings.TrimSpace(heartbeat.BuildDate), 80)
	heartbeat.Hostname = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Hostname), 255)
	heartbeat.OS = truncateServiceReportedValue(strings.TrimSpace(heartbeat.OS), 80)
	heartbeat.Arch = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Arch), 80)
	now := time.Now().UTC()
	sanitizedMetrics := sanitizeServiceMetrics(heartbeat.Metrics)
	metrics, err := json.Marshal(sanitizedMetrics)
	if err != nil {
		return RegisteredService{}, err
	}
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(heartbeat.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	apiHost := ""
	apiPort := 0
	apiSSL := false
	if heartbeat.API != nil {
		apiHost = strings.TrimSpace(heartbeat.API.Host)
		apiPort = heartbeat.API.Port
		apiSSL = heartbeat.API.SSLEnabled
	}
	if apiPort < 1 || apiPort > 65535 {
		apiHost = ""
		apiPort = 0
		apiSSL = false
	}
	apiPublicURL := buildServiceURL(apiHost, apiPort, apiSSL)
	discoveredService, err := s.getService(ctx, heartbeat.ServiceID)
	if errors.Is(err, ErrNotFound) {
		return RegisteredService{}, ErrForbidden
	}
	if err != nil {
		return RegisteredService{}, err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, heartbeat.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	streamIDs := []string{heartbeat.CurrentStreamID, discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	if _, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs); err != nil {
		return RegisteredService{}, ErrForbidden
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{heartbeat.ServiceID})
	if err != nil {
		return RegisteredService{}, ErrForbidden
	}
	service, ok := lockedServices[heartbeat.ServiceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) || service.TokenID != token.ID {
		return RegisteredService{}, ErrForbidden
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		if errors.Is(err, ErrServiceAssignmentConflict) {
			return RegisteredService{}, ErrForbidden
		}
		return RegisteredService{}, err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForService(ctx, tx, heartbeat.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) {
		return RegisteredService{}, ErrForbidden
	}
	owner, _, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil {
		return RegisteredService{}, ErrForbidden
	}
	if heartbeat.CurrentStreamID != "" {
		if owner != heartbeat.CurrentStreamID {
			return RegisteredService{}, ErrForbidden
		}
	}
	var activeTokenID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM service_tokens WHERE id = ? AND revoked_at IS NULL FOR UPDATE`, token.ID).Scan(&activeTokenID); err != nil {
		if err == sql.ErrNoRows {
			return RegisteredService{}, ErrForbidden
		}
		return RegisteredService{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE services SET
status = ?,
last_heartbeat_at = ?,
current_stream_id = CASE WHEN ? = '' THEN current_stream_id ELSE ? END,
metrics = ?,
version = CASE WHEN ? = '' THEN version ELSE ? END,
reported_version = CASE WHEN ? = '' THEN reported_version ELSE ? END,
reported_commit = CASE WHEN ? = '' THEN reported_commit ELSE ? END,
reported_build_date = CASE WHEN ? = '' THEN reported_build_date ELSE ? END,
capabilities = CASE WHEN service_type = 'update_agent' OR ? = '{}' THEN capabilities ELSE ? END,
reported_capabilities = CASE WHEN ? = '{}' THEN reported_capabilities ELSE ? END,
reported_hostname = CASE WHEN ? = '' THEN reported_hostname ELSE ? END,
reported_os = CASE WHEN ? = '' THEN reported_os ELSE ? END,
reported_arch = CASE WHEN ? = '' THEN reported_arch ELSE ? END,
reported_api_host = CASE WHEN ? = '' THEN reported_api_host ELSE ? END,
reported_api_port = CASE WHEN ? = '' THEN reported_api_port ELSE ? END,
reported_api_ssl_enabled = CASE WHEN ? = '' THEN reported_api_ssl_enabled ELSE ? END,
reported_api_public_url = CASE WHEN ? = '' THEN reported_api_public_url ELSE ? END,
last_reported_at = CASE WHEN ? = '' AND ? = '' AND ? = '' AND ? = '{}' AND ? = '' AND ? = '' AND ? = '' AND ? = '' THEN last_reported_at ELSE ? END,
updated_at = ?
WHERE service_id = ?
  AND token_id = ?
  AND EXISTS (SELECT 1 FROM service_tokens st WHERE st.id = ? AND st.revoked_at IS NULL)`,
		heartbeat.Status, now,
		heartbeat.CurrentStreamID, heartbeat.CurrentStreamID,
		string(metrics),
		heartbeat.Version, heartbeat.Version,
		heartbeat.Version, heartbeat.Version,
		heartbeat.Commit, heartbeat.Commit,
		heartbeat.BuildDate, heartbeat.BuildDate,
		string(capabilities), string(capabilities),
		string(capabilities), string(capabilities),
		heartbeat.Hostname, heartbeat.Hostname,
		heartbeat.OS, heartbeat.OS,
		heartbeat.Arch, heartbeat.Arch,
		apiHost, apiHost,
		apiHost, apiPort,
		apiHost, apiSSL,
		apiHost, apiPublicURL,
		heartbeat.Version, heartbeat.Commit, heartbeat.BuildDate, string(capabilities),
		heartbeat.Hostname, heartbeat.OS, heartbeat.Arch, apiHost, now,
		now, heartbeat.ServiceID, token.ID, token.ID,
	)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	if err := s.recordServiceMetricSnapshots(ctx, heartbeat.ServiceID, token.ServiceType, heartbeat.Status, sanitizedMetrics, now); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, heartbeat.ServiceID)
}

func (s MariaDBAuthStore) recordServiceMetricSnapshots(ctx context.Context, serviceID, serviceType, status string, metrics map[string]any, observedAt time.Time) error {
	for name, raw := range metrics {
		value, ok := serviceMetricSnapshotNumber(raw)
		if !ok {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO service_metric_snapshots (service_id, service_type, metric_name, status, value, observed_at) VALUES (?, ?, ?, ?, ?, ?)`, serviceID, serviceType, name, status, value, observedAt); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM service_metric_snapshots WHERE observed_at < ?`, observedAt.Add(-3*time.Hour))
	return err
}

func (s MariaDBAuthStore) ListServiceMetricSnapshots(ctx context.Context, since time.Time, maxPointsPerSeries int) ([]ServiceMetricSnapshot, error) {
	if since.IsZero() {
		since = time.Now().UTC().Add(-3 * time.Hour)
	}
	if maxPointsPerSeries <= 0 {
		maxPointsPerSeries = 360
	}
	rows, err := s.db.QueryContext(ctx, `SELECT metric_name, service_id, service_type, status, value, observed_at
FROM (
  SELECT id, metric_name, service_id, service_type, status, value, observed_at,
    ROW_NUMBER() OVER (
      PARTITION BY service_id, service_type, metric_name
      ORDER BY observed_at DESC, id DESC
    ) AS series_rank
  FROM service_metric_snapshots
  WHERE observed_at >= ?
) ranked
WHERE series_rank <= ?
ORDER BY observed_at ASC, service_id ASC, metric_name ASC`, since.UTC(), maxPointsPerSeries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceMetricSnapshot
	for rows.Next() {
		var snapshot ServiceMetricSnapshot
		if err := rows.Scan(&snapshot.Name, &snapshot.ServiceID, &snapshot.ServiceType, &snapshot.Status, &snapshot.Value, &snapshot.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}

func (s MariaDBAuthStore) UpdateServiceRuntimeReport(ctx context.Context, report ServiceRuntimeReport) (RegisteredService, error) {
	report = normalizeServiceRuntimeReport(report)
	if report.ServiceID == "" {
		return RegisteredService{}, ErrNotFound
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END, version = CASE WHEN ? = '' THEN version ELSE ? END, reported_version = CASE WHEN ? = '' THEN reported_version ELSE ? END, reported_commit = CASE WHEN ? = '' THEN reported_commit ELSE ? END, reported_build_date = CASE WHEN ? = '' THEN reported_build_date ELSE ? END, reported_hostname = CASE WHEN ? = '' THEN reported_hostname ELSE ? END, reported_os = CASE WHEN ? = '' THEN reported_os ELSE ? END, reported_arch = CASE WHEN ? = '' THEN reported_arch ELSE ? END, last_reported_at = CASE WHEN ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' THEN last_reported_at ELSE ? END, updated_at = ? WHERE service_id = ?`,
		report.Version, report.Version, report.Version, report.Version, report.Commit, report.Commit, report.BuildDate, report.BuildDate, report.Hostname, report.Hostname, report.OS, report.OS, report.Arch, report.Arch, report.Version, report.Commit, report.BuildDate, report.Hostname, report.OS, report.Arch, now, now, report.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, report.ServiceID)
}

func sanitizeServiceMetrics(metrics map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metrics {
		if strings.TrimSpace(key) == "" {
			continue
		}
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			out[key] = typed
		}
	}
	return out
}

func serviceMetricSnapshotNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float32:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func sanitizeServiceCapabilities(capabilities map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range capabilities {
		key = strings.TrimSpace(key)
		if key == "" || serviceCapabilitySecretKey(key) {
			continue
		}
		out[key] = sanitizeServiceCapabilityValue(value)
	}
	return out
}

func sanitizeServiceCapabilityValue(value any) any {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		if secretLikeValue(typed) {
			return "<redacted>"
		}
		return typed
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeServiceCapabilityValue(item))
		}
		return out
	case map[string]any:
		return sanitizeServiceCapabilities(typed)
	default:
		return nil
	}
}

func serviceCapabilitySecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"password", "passwd", "token", "api_key", "apikey", "private_key", "credential", "webhook_url", "stream_key", "client_secret", "refresh_token", "access_token", "authorization", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func normalizeServiceRuntimeReport(report ServiceRuntimeReport) ServiceRuntimeReport {
	report.ServiceID = strings.TrimSpace(report.ServiceID)
	report.Version = truncateServiceReportedValue(strings.TrimSpace(report.Version), 80)
	report.Commit = truncateServiceReportedValue(strings.TrimSpace(report.Commit), 80)
	report.BuildDate = truncateServiceReportedValue(strings.TrimSpace(report.BuildDate), 80)
	report.Hostname = truncateServiceReportedValue(strings.TrimSpace(report.Hostname), 255)
	report.OS = truncateServiceReportedValue(strings.TrimSpace(report.OS), 80)
	report.Arch = truncateServiceReportedValue(strings.TrimSpace(report.Arch), 80)
	return report
}

func applyServiceRuntimeReport(service RegisteredService, report ServiceRuntimeReport, now time.Time) RegisteredService {
	if service.Status == "pending" {
		service.Status = "registered"
	}
	if report.Version != "" {
		service.Version = report.Version
		service.ReportedVersion = report.Version
	}
	if report.Commit != "" {
		service.ReportedCommit = report.Commit
	}
	if report.BuildDate != "" {
		service.ReportedBuildDate = report.BuildDate
	}
	if report.Hostname != "" {
		service.ReportedHostname = report.Hostname
	}
	if report.OS != "" {
		service.ReportedOS = report.OS
	}
	if report.Arch != "" {
		service.ReportedArch = report.Arch
	}
	if report.Version != "" || report.Commit != "" || report.BuildDate != "" || report.Hostname != "" || report.OS != "" || report.Arch != "" {
		service.LastReportedAt = &now
	}
	service.UpdatedAt = now
	return service
}

func truncateServiceReportedValue(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
