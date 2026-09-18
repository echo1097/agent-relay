package agents

import (
	"context"
	"errors"
	"time"
)

type RetentionPolicy struct {
	ArchiveAfterDays int `json:"archive_after_days"`
	DeleteAfterDays  int `json:"delete_after_days"`
}

type RetentionResult struct {
	Archived int64
	Restored int64
	Deleted  int64
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{ArchiveAfterDays: 7, DeleteAfterDays: 30}
}

func (policy RetentionPolicy) Validate() error {
	maxDays := int((1<<63 - 1) / (24 * time.Hour))
	if policy.ArchiveAfterDays < 1 || policy.DeleteAfterDays <= policy.ArchiveAfterDays || policy.DeleteAfterDays > maxDays {
		return errors.New("retention requires archive days >= 1 and delete days > archive days, with a maximum of 106751 days")
	}
	return nil
}

func (registry *Registry) Retain(ctx context.Context) (RetentionResult, error) {
	result, err := registry.store.RetainAgents(ctx, registry.nodeID, registry.now().UTC())
	if err == nil && (result.Archived > 0 || result.Restored > 0 || result.Deleted > 0) {
		registry.logger.Info("session retention applied", "archived", result.Archived, "restored", result.Restored, "deleted", result.Deleted)
	}
	return result, err
}
