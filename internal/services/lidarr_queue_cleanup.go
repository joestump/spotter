package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"spotter/ent"
	"spotter/ent/lidarrqueue"
)

// Governing: SPEC-0017 REQ "Queue Cleanup", ADR-0029

// CleanupLidarrQueue deletes stale entries from the Lidarr submission queue:
//   - Submitted entries older than 7 days (successfully processed)
//   - Failed entries with 10+ attempts older than 30 days (permanently failed)
func CleanupLidarrQueue(ctx context.Context, client *ent.Client) error {
	now := time.Now()

	// Delete submitted entries older than 7 days
	submittedCutoff := now.AddDate(0, 0, -7)
	submittedCount, err := client.LidarrQueue.Delete().
		Where(
			lidarrqueue.StatusEQ(lidarrqueue.StatusSubmitted),
			lidarrqueue.UpdatedAtLT(submittedCutoff),
		).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("cleanup submitted lidarr queue entries: %w", err)
	}

	// Delete failed entries with 10+ attempts older than 30 days
	failedCutoff := now.AddDate(0, 0, -30)
	failedCount, err := client.LidarrQueue.Delete().
		Where(
			lidarrqueue.StatusEQ(lidarrqueue.StatusFailed),
			lidarrqueue.AttemptsGTE(10),
			lidarrqueue.UpdatedAtLT(failedCutoff),
		).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("cleanup failed lidarr queue entries: %w", err)
	}

	if submittedCount > 0 || failedCount > 0 {
		slog.Info("lidarr queue cleanup completed",
			"submitted_deleted", submittedCount,
			"failed_deleted", failedCount,
		)
	}

	return nil
}
