package store

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStreamStore) RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error) {
	return s.AppendStreamLog(ctx, StreamLog{StreamID: id, Level: "info", Message: "archive upload retry requested", Fields: map[string]any{"actor_user_id": actorUserID}})
}

func (s *MemoryStreamStore) AppendStreamLog(ctx context.Context, log StreamLog) (StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return StreamLog{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	log = normalizeStreamLog(log)
	if _, ok := s.streams[log.StreamID]; !ok {
		return StreamLog{}, ErrNotFound
	}
	s.logs[log.StreamID] = append(s.logs[log.StreamID], log)
	return log, nil
}

func (s *MemoryStreamStore) ListStreamLogs(ctx context.Context, id string) ([]StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return nil, ErrNotFound
	}
	logs := append([]StreamLog(nil), s.logs[id]...)
	for index := range logs {
		logs[index] = enrichMemoryStreamLog(logs[index], s.streams[id])
	}
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].CreatedAt.After(logs[j].CreatedAt) })
	return logs, nil
}

func (s *MemoryStreamStore) ListStreamLogHistory(ctx context.Context, limit int, before time.Time, beforeID string) ([]StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := make([]StreamLog, 0)
	for streamID, streamLogs := range s.logs {
		stream := s.streams[streamID]
		for _, log := range streamLogs {
			if !before.IsZero() && (log.CreatedAt.After(before) || (log.CreatedAt.Equal(before) && log.ID >= beforeID)) {
				continue
			}
			logs = append(logs, enrichMemoryStreamLog(log, stream))
		}
	}
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].CreatedAt.Equal(logs[j].CreatedAt) {
			return logs[i].ID > logs[j].ID
		}
		return logs[i].CreatedAt.After(logs[j].CreatedAt)
	})
	limit = boundedStreamLogLimit(limit)
	if len(logs) > limit {
		logs = logs[:limit]
	}
	return logs, nil
}

func enrichMemoryStreamLog(log StreamLog, stream Stream) StreamLog {
	log.StreamName = stream.Name
	if stream.DeletedAt != nil {
		deletedAt := *stream.DeletedAt
		log.StreamDeletedAt = &deletedAt
	}
	return log
}
