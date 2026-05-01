package store

import (
	"context"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type FirestoreStore struct {
	client *firestore.Client
}

func NewFirestoreStore(client *firestore.Client) *FirestoreStore {
	return &FirestoreStore{client: client}
}

func (s *FirestoreStore) IncrementUsage(ctx context.Context, uid, model string) error {
	if uid == "" {
		return nil
	}

	today := time.Now().UTC().Format("2006-01-02")
	ref := s.client.Collection("users").Doc(uid).Collection("usage").Doc("summary")

	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}

		totalRequests := int64(0)
		dailyCount := int64(0)
		dailyDate := today

		if snap != nil && snap.Exists() {
			data := snap.Data()
			totalRequests = numberToInt64(data["totalRequests"])
			if existingDate, ok := data["dailyDate"].(string); ok && existingDate == today {
				dailyDate = existingDate
				dailyCount = numberToInt64(data["dailyCount"])
			}
		}

		update := map[string]any{
			"totalRequests": totalRequests + 1,
			"dailyCount":    dailyCount + 1,
			"dailyDate":     dailyDate,
			"lastRequestAt": firestore.ServerTimestamp,
		}

		if model != "" {
			update["modelUsage"] = map[string]any{
				sanitizeFieldPath(model): firestore.Increment(1),
			}
		}

		return tx.Set(ref, update, firestore.MergeAll)
	})
}

func sanitizeFieldPath(value string) string {
	replacer := strings.NewReplacer(
		".", "_",
		"/", "__",
		"*", "_",
		"`", "_",
		"[", "_",
		"]", "_",
	)
	return replacer.Replace(value)
}

func numberToInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}
