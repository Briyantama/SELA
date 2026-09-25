package media_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/media"
)

func ids(items []media.Item) map[string]bool {
	set := map[string]bool{}
	for _, item := range items {
		set[item.ID] = true
	}
	return set
}

func (f *fixture) highlight(t *testing.T, mediaID string, rank int) {
	t.Helper()
	f.exec(t, `UPDATE media SET hall_of_fame = true, hall_of_fame_rank = $2 WHERE media_id = $1`, mediaID, rank)
}

func TestMyMedia_showsAGuestOnlyTheirOwnUploadsEvenAfterReveal(t *testing.T) {
	// Arrange: an instant-reveal event, so reveal is already over.
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	a := f.guest(t, eventID).Guest
	b := f.guest(t, eventID).Guest
	aReady := f.upload(t, a)
	aPending := f.begin(t, a, 10)
	bReady := f.upload(t, b)
	f.highlight(t, bReady.ID, 1)

	// Act
	items, err := f.svc.MyMedia(context.Background(), a)

	// Assert
	if err != nil {
		t.Fatalf("MyMedia: %v", err)
	}
	got := ids(items)
	if len(items) != 2 || !got[aReady.ID] || !got[aPending.MediaID] {
		t.Fatalf("MyMedia = %v, want exactly A's two items", got)
	}
	if got[bReady.ID] {
		t.Fatal("guest A can see guest B's media")
	}
	for _, item := range items {
		if (item.Processing == media.ProcessingReady) != (item.URL != "") {
			t.Fatalf("item %s (%s) url = %q: only ready items get a URL", item.ID, item.Processing, item.URL)
		}
	}
}

func TestMyMedia_leavesOutDeletedItems(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{})).Guest
	deleted := f.upload(t, g)
	f.exec(t, `UPDATE media SET status = 'deleted', deleted_at = now() WHERE media_id = $1`, deleted.ID)

	// Act
	items, err := f.svc.MyMedia(context.Background(), g)

	// Assert
	if err != nil || len(items) != 0 {
		t.Fatalf("MyMedia = %v, %v; want nothing", ids(items), err)
	}
}

func TestHallOfFame_opensOnlyAfterTheRevealTime(t *testing.T) {
	// Arrange
	f := newFixture(t)
	revealAt := f.clock.Now().Add(2 * time.Hour)
	eventID := f.event(t, eventOpts{revealMode: "delayed", revealAt: &revealAt})
	a := f.guest(t, eventID).Guest
	b := f.guest(t, eventID).Guest
	star := f.upload(t, b)
	f.highlight(t, star.ID, 1)
	ctx := context.Background()

	// Act
	before, beforeErr := f.svc.HallOfFame(ctx, a)
	f.clock.Advance(2*time.Hour + time.Second)
	after, afterErr := f.svc.HallOfFame(ctx, a)

	// Assert
	if beforeErr != nil || len(before) != 0 {
		t.Fatalf("before reveal = %v, %v; want nothing", ids(before), beforeErr)
	}
	if afterErr != nil || len(after) != 1 || after[0].ID != star.ID || after[0].URL == "" {
		t.Fatalf("after reveal = %+v, %v; want the highlighted item with a URL", after, afterErr)
	}
}

func TestHallOfFame_isOnlyTheCuratedVisibleSubsetInRankOrder(t *testing.T) {
	// Arrange: instant reveal; three highlights, one then hidden, plus an ordinary item.
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	viewer := f.guest(t, eventID).Guest
	other := f.guest(t, eventID).Guest
	second := f.upload(t, other)
	first := f.upload(t, other)
	hidden := f.upload(t, other)
	ordinary := f.upload(t, other)
	f.highlight(t, second.ID, 2)
	f.highlight(t, first.ID, 1)
	f.highlight(t, hidden.ID, 3)
	f.exec(t, `UPDATE media SET hall_of_fame = false, hall_of_fame_rank = NULL, status = 'hidden' WHERE media_id = $1`, hidden.ID)

	// Act
	items, err := f.svc.HallOfFame(context.Background(), viewer)

	// Assert
	if err != nil {
		t.Fatalf("HallOfFame: %v", err)
	}
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != second.ID {
		t.Fatalf("HallOfFame = %v, want [first second] in rank order", items)
	}
	if ids(items)[ordinary.ID] || ids(items)[hidden.ID] {
		t.Fatal("HallOfFame leaked a non-curated or hidden item")
	}
}

func TestHallOfFame_neverCrossesEvents(t *testing.T) {
	// Arrange
	f := newFixture(t)
	elsewhere := f.event(t, eventOpts{})
	star := f.upload(t, f.guest(t, elsewhere).Guest)
	f.highlight(t, star.ID, 1)
	viewer := f.guest(t, f.event(t, eventOpts{})).Guest

	// Act
	items, err := f.svc.HallOfFame(context.Background(), viewer)

	// Assert
	if err != nil || len(items) != 0 {
		t.Fatalf("HallOfFame = %v, %v; want nothing from another event", ids(items), err)
	}
}

func TestHostMedia_showsTheOwnerEveryNonDeletedItemRegardlessOfReveal(t *testing.T) {
	// Arrange: reveal is far in the future; the host is never gated by it (FR-05.6).
	f := newFixture(t)
	hostID := f.host(t)
	revealAt := f.clock.Now().Add(30 * 24 * time.Hour)
	eventID := f.event(t, eventOpts{hostID: hostID, revealMode: "delayed", revealAt: &revealAt})
	a := f.guest(t, eventID).Guest
	b := f.guest(t, eventID).Guest
	aReady := f.upload(t, a)
	bPending := f.begin(t, b, 10)
	hidden := f.upload(t, b)
	deleted := f.upload(t, b)
	f.exec(t, `UPDATE media SET status = 'hidden' WHERE media_id = $1`, hidden.ID)
	f.exec(t, `UPDATE media SET status = 'deleted', deleted_at = now() WHERE media_id = $1`, deleted.ID)

	// Act
	items, err := f.svc.HostMedia(context.Background(), hostID, eventID)

	// Assert
	if err != nil {
		t.Fatalf("HostMedia: %v", err)
	}
	got := ids(items)
	if len(items) != 3 || !got[aReady.ID] || !got[bPending.MediaID] || !got[hidden.ID] {
		t.Fatalf("HostMedia = %v, want A's ready, B's pending and B's hidden item", got)
	}
	if got[deleted.ID] {
		t.Fatal("HostMedia returned a deleted item")
	}
}

func TestHostMedia_anotherHostCannotSeeTheEvent(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	f.upload(t, f.guest(t, eventID).Guest)
	stranger := f.host(t)

	// Act
	_, err := f.svc.HostMedia(context.Background(), stranger, eventID)
	_, bogus := f.svc.HostMedia(context.Background(), stranger, "not-a-uuid")

	// Assert
	if !errors.Is(err, media.ErrNotFound) || !errors.Is(bogus, media.ErrNotFound) {
		t.Fatalf("stranger = %v, bogus id = %v; want ErrNotFound", err, bogus)
	}
}
