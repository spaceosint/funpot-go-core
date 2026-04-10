package media

import "context"

// UploadedVideoStore persists uploaded video metadata for admin history endpoints.
type UploadedVideoStore interface {
	Save(ctx context.Context, streamerID string, item UploadedVideo) error
	ListByStreamer(ctx context.Context, streamerID string) ([]UploadedVideo, error)
	DeleteByStreamer(ctx context.Context, streamerID string) error
}
