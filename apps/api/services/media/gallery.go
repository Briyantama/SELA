package media

import "context"

// MyMedia is the guest's own gallery, including uploads still processing (FR-05.5).
func (s *Service) MyMedia(ctx context.Context, g Guest) ([]Item, error) {
	media, err := s.repo.ListSessionMedia(ctx, g.EventID, g.SessionID)
	if err != nil {
		return nil, err
	}
	return s.items(ctx, media)
}

// HallOfFame is the host-curated collection, open to guests once the event's reveal has passed (FSD 3.2).
func (s *Service) HallOfFame(ctx context.Context, g Guest) ([]Item, error) {
	media, err := s.repo.ListHallOfFame(ctx, g.EventID, s.opts.Now())
	if err != nil {
		return nil, err
	}
	return s.items(ctx, media)
}

// HostMedia is everything in the host's own event, whatever its reveal state (FR-05.6).
func (s *Service) HostMedia(ctx context.Context, hostID, eventID string) ([]Item, error) {
	if !isUUID(eventID) || !isUUID(hostID) {
		return nil, ErrNotFound
	}
	media, err := s.repo.ListHostMedia(ctx, hostID, eventID)
	if err != nil {
		return nil, err
	}
	return s.items(ctx, media)
}

func (s *Service) items(ctx context.Context, media []Media) ([]Item, error) {
	items := make([]Item, 0, len(media))
	for _, m := range media {
		item, err := s.item(ctx, m)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// item attaches a short-lived download URL to a processed item; nothing else is ever linkable.
func (s *Service) item(ctx context.Context, m Media) (Item, error) {
	item := Item{Media: m}
	if m.Processing != ProcessingReady {
		return item, nil
	}
	url, err := s.store.PresignGet(ctx, m.ObjectKey, s.opts.DownloadTTL)
	if err != nil {
		return Item{}, err
	}
	item.URL = url
	return item, nil
}
