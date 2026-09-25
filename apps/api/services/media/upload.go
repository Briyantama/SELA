package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Briyantama/SELA/internal/objstore"
	"github.com/Briyantama/SELA/services/media/strip"
)

// BeginUpload reserves a shot and issues a pre-signed PUT for exactly size bytes of contentType
// (FR-03.3, FR-04.1, FR-SEC.1). The media row exists from this moment, as pending.
func (s *Service) BeginUpload(ctx context.Context, g Guest, contentType string, size int64) (Upload, error) {
	kind, normalized, err := KindOf(contentType)
	if err != nil || !uploadable[normalized] {
		return Upload{}, ErrUnsupportedType
	}
	if size <= 0 {
		return Upload{}, &ValidationError{Field: "size_bytes", Message: "must be positive"}
	}
	if size > MaxPhotoBytes {
		return Upload{}, ErrTooLarge
	}
	ev, err := s.repo.GuestEvent(ctx, g.EventID, s.opts.Now())
	if err != nil {
		return Upload{}, err
	}

	var remaining *int
	if ev.ShotLimit != nil {
		limit := *ev.ShotLimit
		left, ok, err := s.guests.Reserve(ctx, g, func() (int, error) { return s.remaining(ctx, g, limit) }, s.opts.SessionTTL)
		if err != nil {
			return Upload{}, err
		}
		if !ok {
			return Upload{}, ErrQuotaExhausted
		}
		remaining = &left
	}

	up, err := s.issue(ctx, g, kind, normalized, size)
	if err != nil {
		s.release(ctx, g, ev.ShotLimit)
		return Upload{}, err
	}
	up.ShotsRemaining = remaining
	return up, nil
}

func (s *Service) issue(ctx context.Context, g Guest, kind Kind, contentType string, size int64) (Upload, error) {
	mediaID, err := newUUID()
	if err != nil {
		return Upload{}, err
	}
	key := "incoming/" + g.EventID + "/" + mediaID
	err = s.repo.CreatePending(ctx, PendingMedia{
		ID: mediaID, EventID: g.EventID, SessionID: g.SessionID, Kind: kind, ContentType: contentType,
		ObjectKey: key, DeclaredSize: size, UploadedAt: s.opts.Now(),
	})
	if err != nil {
		return Upload{}, err
	}
	req, err := s.store.PresignPut(ctx, key, contentType, size, s.opts.UploadTTL)
	if err != nil {
		if _, markErr := s.repo.MarkFailed(ctx, mediaID); markErr != nil {
			slog.Error("mark unsigned upload failed", "media_id", mediaID, "err", markErr)
		}
		return Upload{}, err
	}
	return Upload{MediaID: mediaID, Request: req}, nil
}

// CompleteUpload validates the uploaded bytes by magic number, strips their metadata and publishes a
// clean copy (FR-SEC.2, FR-SEC.3). Bad bytes fail the item and return its shot.
func (s *Service) CompleteUpload(ctx context.Context, g Guest, mediaID string) (Item, error) {
	if !isUUID(mediaID) {
		return Item{}, ErrNotFound
	}
	m, limit, err := s.repo.PendingUpload(ctx, g.EventID, g.SessionID, mediaID)
	if err != nil {
		return Item{}, err
	}
	info, err := s.store.Head(ctx, m.ObjectKey)
	if errors.Is(err, objstore.ErrNotFound) {
		return Item{}, ErrUploadMissing
	}
	if err != nil {
		return Item{}, err
	}
	if m.SizeBytes == nil || info.Size != *m.SizeBytes {
		return Item{}, s.reject(ctx, m, g, limit, errors.New("size differs from the declared size"))
	}
	body, err := s.store.Get(ctx, m.ObjectKey, *m.SizeBytes)
	if errors.Is(err, objstore.ErrTooLarge) || errors.Is(err, objstore.ErrNotFound) {
		return Item{}, s.reject(ctx, m, g, limit, err)
	}
	if err != nil {
		return Item{}, err
	}
	clean, err := strip.Clean(m.ContentType, body)
	if err != nil {
		return Item{}, s.reject(ctx, m, g, limit, err)
	}
	return s.publish(ctx, m, clean)
}

// publish stores the clean copy under a fresh random key and flips the row to ready.
func (s *Service) publish(ctx context.Context, m Media, clean []byte) (Item, error) {
	id, err := newUUID()
	if err != nil {
		return Item{}, err
	}
	finalKey := "media/" + m.EventID + "/" + id
	if err := s.store.Put(ctx, finalKey, m.ContentType, clean); err != nil {
		return Item{}, err
	}
	now := s.opts.Now()
	size := int64(len(clean))
	ok, err := s.repo.MarkReady(ctx, m.ID, finalKey, size, now)
	if err != nil || !ok {
		s.deleteObject(ctx, finalKey)
		if err != nil {
			return Item{}, err
		}
		return Item{}, ErrNotFound // the sweeper failed it meanwhile
	}
	s.deleteObject(ctx, m.ObjectKey)

	m.ObjectKey, m.SizeBytes, m.ReadyAt, m.Processing = finalKey, &size, &now, ProcessingReady
	return s.item(ctx, m)
}

func (s *Service) reject(ctx context.Context, m Media, g Guest, limit *int, cause error) error {
	failed, err := s.repo.MarkFailed(ctx, m.ID)
	if err != nil {
		return err
	}
	if failed {
		s.release(ctx, g, limit)
	}
	s.deleteObject(ctx, m.ObjectKey)
	return fmt.Errorf("%w: %v", ErrRejected, cause)
}

// SweepExpired fails uploads whose URL expired without a completion, deletes whatever arrived and
// returns their shots. It is safe to run concurrently: each row is claimed by exactly one sweep.
func (s *Service) SweepExpired(ctx context.Context) (int, error) {
	expired, err := s.repo.ExpirePending(ctx, s.opts.Now().Add(-s.opts.UploadTTL-sweepGrace))
	if err != nil {
		return 0, err
	}
	for _, x := range expired {
		s.deleteObject(ctx, x.ObjectKey)
		if x.SessionID != nil {
			s.release(ctx, Guest{EventID: x.EventID, SessionID: *x.SessionID}, x.ShotLimit)
		}
	}
	return len(expired), nil
}

// RunSweeper calls SweepExpired every interval until ctx ends.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.SweepExpired(ctx); err != nil {
				slog.Error("sweep expired uploads", "err", err)
			} else if n > 0 {
				slog.Info("swept expired uploads", "count", n)
			}
		}
	}
}

func (s *Service) release(ctx context.Context, g Guest, limit *int) {
	if limit == nil {
		return
	}
	if err := s.guests.Release(ctx, g, *limit); err != nil {
		// The counter reseeds from the media rows once it expires, so a lost release self-heals.
		slog.Warn("release shot", "event_id", g.EventID, "err", err)
	}
}

func (s *Service) deleteObject(ctx context.Context, key string) {
	if err := s.store.Delete(ctx, key); err != nil {
		slog.Warn("delete object", "key", key, "err", err)
	}
}
