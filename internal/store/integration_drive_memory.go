package store

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"sort"
	"strings"
	"time"
)

func (s *MemoryIntegrationStore) ListDriveDestinations(ctx context.Context) ([]DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DriveDestination, 0, len(s.destinations))
	for _, destination := range s.destinations {
		out = append(out, publicDriveDestination(destination))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryIntegrationStore) CreateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	destination, err := normalizeDriveDestination(destination, true)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.ID = newUUID()
	now := time.Now().UTC().Format(time.RFC3339)
	destination.CreatedAt, destination.UpdatedAt = now, now
	destination.FolderIDConfigured = destination.FolderID != ""
	destination.FolderIDFingerprint = security.SecretFingerprint(destination.FolderID)
	destination.MaskedFolderID = maskIdentifier(destination.FolderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.destinations {
		if strings.EqualFold(existing.Name, destination.Name) {
			return DriveDestination{}, errors.New("drive destination name already exists")
		}
	}
	s.destinations[destination.ID] = destination
	return publicDriveDestination(destination), nil
}

func (s *MemoryIntegrationStore) GetDriveDestination(ctx context.Context, id string) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	destination, ok := s.destinations[id]
	if !ok {
		return DriveDestination{}, ErrNotFound
	}
	return publicDriveDestination(destination), nil
}

func (s *MemoryIntegrationStore) GetDriveDestinationForDispatch(ctx context.Context, id string) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	destination, ok := s.destinations[id]
	if !ok {
		return DriveDestination{}, ErrNotFound
	}
	return destination, nil
}

func (s *MemoryIntegrationStore) UpdateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	destination, err := normalizeDriveDestination(destination, false)
	if err != nil {
		return DriveDestination{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.destinations[destination.ID]
	if !ok {
		return DriveDestination{}, ErrNotFound
	}
	for id, item := range s.destinations {
		if id != destination.ID && strings.EqualFold(item.Name, destination.Name) {
			return DriveDestination{}, errors.New("drive destination name already exists")
		}
	}
	if destination.FolderID == "" {
		destination.FolderID = existing.FolderID
		destination.FolderIDConfigured = existing.FolderIDConfigured
		destination.FolderIDFingerprint = existing.FolderIDFingerprint
		destination.MaskedFolderID = existing.MaskedFolderID
	} else {
		destination.FolderIDConfigured = true
		destination.FolderIDFingerprint = security.SecretFingerprint(destination.FolderID)
		destination.MaskedFolderID = maskIdentifier(destination.FolderID)
	}
	destination.CreatedAt = existing.CreatedAt
	destination.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.destinations[destination.ID] = destination
	return publicDriveDestination(destination), nil
}

func (s *MemoryIntegrationStore) DeleteDriveDestination(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.destinations[id]; !ok {
		return ErrNotFound
	}
	delete(s.destinations, id)
	return nil
}
