package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// maxReplayAllConcurrency is the maximum number of concurrent deliveries
// performed by the replay-all endpoint.
const maxReplayAllConcurrency = 8

// manualReplayCoolingOff is the fixed cooling-off period applied to a DLQ record
// after a failed manual replay via the HTTP endpoint. Manual replay ignores the
// configurable DLQCoolingOff because it is an explicit operator action; this
// short delay merely prevents the auto-retry scheduler from immediately
// re-picking up the record after a manual attempt.
const manualReplayCoolingOff = 5 * time.Minute

// ListDLQHandler handles GET /webhooks/dlq.
// Supports optional ?cursor= and ?limit= pagination parameters.
// Returns a JSON object with the current page of DLQ records and a next_cursor value.
func ListDLQHandler(dlq *webhooks.DLQStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cursor, limit, ok := parsePaginationParams(w, r)
		if !ok {
			return
		}

		records, nextCursor, err := dlq.ListPage(cursor, limit)
		if err != nil {
			slog.Error("webhooks: dlq: failed to list records", "error", err)
			http.Error(w, "failed to list DLQ records", http.StatusInternalServerError)
			return
		}
		if records == nil {
			records = []webhooks.DLQRecord{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"records":     records,
			"next_cursor": nextCursor,
		})
	}
}

// GetDLQHandler handles GET /webhooks/dlq/{id}.
// Returns the DLQ record with the given ID, or 404 if not found.
func GetDLQHandler(dlq *webhooks.DLQStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rec, err := dlq.Get(id)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "DLQ record not found", http.StatusNotFound)
				return
			}
			slog.Error("webhooks: dlq: failed to get record", "id", id, "error", err)
			http.Error(w, "failed to get DLQ record", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec) //nolint:errcheck
	}
}

// DeleteDLQHandler handles DELETE /webhooks/dlq/{id}.
// Removes the DLQ record with the given ID. Returns 204 on success, 404 if not found.
func DeleteDLQHandler(dlq *webhooks.DLQStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := dlq.Delete(id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "DLQ record not found", http.StatusNotFound)
				return
			}
			slog.Error("webhooks: dlq: failed to delete record", "id", id, "error", err)
			http.Error(w, "failed to delete DLQ record", http.StatusInternalServerError)
			return
		}

		slog.Info("webhooks: dlq: record deleted", "id", id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ReplayDLQHandler handles POST /webhooks/dlq/{id}/replay.
// Attempts a single delivery of the DLQ record's event to its target subscription.
// On success the record is removed from the DLQ. On failure the record is updated
// with the new error and an incremented attempt count.
func ReplayDLQHandler(dlq *webhooks.DLQStore, replayer webhooks.DLQReplayer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rec, err := dlq.Get(id)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "DLQ record not found", http.StatusNotFound)
				return
			}
			slog.Error("webhooks: dlq: replay: failed to get record", "id", id, "error", err)
			http.Error(w, "failed to get DLQ record", http.StatusInternalServerError)
			return
		}

		replayErr := replayer.ReplayRecord(rec)
		rec.Attempts++
		rec.FailedAt = time.Now().UTC()

		if replayErr == nil {
			if delErr := dlq.Delete(rec.ID); delErr != nil {
				slog.Error("webhooks: dlq: replay: failed to delete after success",
					"id", rec.ID, "error", delErr)
			}
			slog.Info("webhooks: dlq: replay succeeded", "id", id)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
				"status": "delivered",
				"id":     id,
			})
			return
		}

		rec.LastError = replayErr.Error()
		rec.NextRetryAfter = rec.FailedAt.Add(manualReplayCoolingOff)
		if saveErr := dlq.Save(&rec); saveErr != nil {
			slog.Error("webhooks: dlq: replay: failed to update record after failure",
				"id", id, "error", saveErr)
		}

		slog.Warn("webhooks: dlq: replay failed",
			"id", id,
			"attempts", rec.Attempts,
			"error", replayErr,
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status": "failed",
			"id":     id,
			"error":  replayErr.Error(),
		})
	}
}

// replayAllResult holds per-record outcome for the replay-all endpoint.
type replayAllResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// ReplayAllDLQHandler handles POST /webhooks/dlq/replay-all.
// Replays all DLQ records (regardless of NextRetryAfter). Eligible records are
// delivered concurrently up to maxReplayAllConcurrency goroutines.
// Returns a JSON summary with counts and per-record outcomes.
func ReplayAllDLQHandler(dlq *webhooks.DLQStore, replayer webhooks.DLQReplayer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := dlq.List()
		if err != nil {
			slog.Error("webhooks: dlq: replay-all: failed to list records", "error", err)
			http.Error(w, "failed to list DLQ records", http.StatusInternalServerError)
			return
		}

		sem := make(chan struct{}, maxReplayAllConcurrency)
		var (
			mu      sync.Mutex
			results []replayAllResult
			successes, failures int
		)
		var wg sync.WaitGroup

		for _, rec := range records {
			wg.Add(1)
			sem <- struct{}{}
			go func(rec webhooks.DLQRecord) {
				defer wg.Done()
				defer func() { <-sem }()

				replayErr := replayer.ReplayRecord(rec)
				rec.Attempts++
				rec.FailedAt = time.Now().UTC()

				result := replayAllResult{ID: rec.ID}
				if replayErr == nil {
					result.Status = "delivered"
					_ = dlq.Delete(rec.ID)
					mu.Lock()
					successes++
					mu.Unlock()
				} else {
					result.Status = "failed"
					result.Error = replayErr.Error()
					rec.LastError = replayErr.Error()
					rec.NextRetryAfter = rec.FailedAt.Add(manualReplayCoolingOff)
					_ = dlq.Save(&rec)
					mu.Lock()
					failures++
					mu.Unlock()
				}

				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}(rec)
		}
		wg.Wait()

		slog.Info("webhooks: dlq: replay-all complete",
			"total", len(records),
			"succeeded", successes,
			"failed", failures,
		)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"total":     len(records),
			"succeeded": successes,
			"failed":    failures,
			"results":   results,
		})
	}
}
