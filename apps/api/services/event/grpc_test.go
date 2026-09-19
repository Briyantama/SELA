package event_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/Briyantama/SELA/gen/go/event/v1"
	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/event"
)

const listMethod = "/sela.event.v1.EventService/ListEventCategories"

// startEventGRPC serves EventService behind the real host interceptor, with only the category list public.
func startEventGRPC(t *testing.T, svc event.Events, tokens map[string]string) eventv1.EventServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.NewUnaryHostInterceptor(fakeAuth{tokens: tokens}, listMethod)))
	eventv1.RegisterEventServiceServer(srv, event.NewGRPCServer(svc))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return eventv1.NewEventServiceClient(conn)
}

func withToken(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func grpcStatus(t *testing.T, err error, want codes.Code) *status.Status {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || st.Code() != want {
		t.Fatalf("err = %v, want gRPC code %s", err, want)
	}
	return st
}

type grpcFixture struct {
	*fixture
	client eventv1.EventServiceClient
	owner  string
	other  string
}

func newGRPCFixture(t *testing.T) *grpcFixture {
	t.Helper()
	f := newFixture(t)
	owner, other := f.host(t, "owner@example.test"), f.host(t, "other@example.test")
	return &grpcFixture{fixture: f, owner: owner, other: other,
		client: startEventGRPC(t, f.svc, map[string]string{"owner-token": owner, "other-token": other})}
}

func TestGRPCListEventCategories_isPublicAndResolvesDefaults(t *testing.T) {
	// Arrange
	g := newGRPCFixture(t)

	// Act
	resp, err := g.client.ListEventCategories(context.Background(), &eventv1.ListEventCategoriesRequest{})

	// Assert
	if err != nil {
		t.Fatalf("ListEventCategories: %v", err)
	}
	cats := resp.GetCategories()
	if len(cats) != 6 || cats[0].GetCode() != "pernikahan" || cats[2].GetCode() != "ulang_tahun" {
		t.Fatalf("categories = %v", cats)
	}
	wedding, birthday := cats[0], cats[2]
	if wedding.GetRevealDefault() != eventv1.RevealDefault_REVEAL_DEFAULT_DELAYED || wedding.GetDefaultRevealDelayHours() != 24 ||
		wedding.GetEffectiveRevealMode() != eventv1.RevealMode_REVEAL_MODE_DELAYED || wedding.GetEffectiveRevealDelayHours() != 24 {
		t.Errorf("wedding = %v", wedding)
	}
	if birthday.GetShotLimitDefault() != eventv1.ShotLimitDefault_SHOT_LIMIT_DEFAULT_TBD || birthday.DefaultShotLimit != nil ||
		birthday.GetRevealDefault() != eventv1.RevealDefault_REVEAL_DEFAULT_TBD ||
		birthday.GetEffectiveRevealMode() != eventv1.RevealMode_REVEAL_MODE_INSTANT || birthday.EffectiveShotLimit != nil || birthday.EffectiveRevealDelayHours != nil {
		t.Errorf("birthday = %v, want tbd resolved to instant/unlimited", birthday)
	}
}

func TestGRPCCreateEvent_createsForTheAuthenticatedHost(t *testing.T) {
	// Arrange
	g := newGRPCFixture(t)

	// Act
	resp, err := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{
		CategoryCode: "pernikahan", Name: "Akad", EventDate: "2026-12-05", ShotLimit: proto.Int32(30),
	})

	// Assert
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	ev := resp.GetEvent()
	if ev.GetName() != "Akad" || ev.GetCategoryCode() != "pernikahan" || ev.GetEventDate() != "2026-12-05" || ev.GetStatus() != "active" || ev.GetPackage() != "gratis" {
		t.Errorf("event = %v", ev)
	}
	if ev.ShotLimit == nil || ev.GetShotLimit() != 30 || ev.GetRevealMode() != eventv1.RevealMode_REVEAL_MODE_DELAYED || ev.GetRevealAt() == nil {
		t.Errorf("settings = %v", ev)
	}
	if ev.GetShortLink() != baseURL+"/"+ev.GetShortCode() || ev.GetQrPngUrl() != "/api/v1/events/"+ev.GetEventId()+"/qr.png" || ev.GetQrSvgUrl() != "/api/v1/events/"+ev.GetEventId()+"/qr.svg" {
		t.Errorf("links = %q %q %q", ev.GetShortLink(), ev.GetQrPngUrl(), ev.GetQrSvgUrl())
	}
	if ev.GetCreatedAt() == nil || ev.GetCreatedAt().AsTime().IsZero() {
		t.Error("created_at not set")
	}
	var owner string
	if err := g.conn.QueryRow(`SELECT host_id FROM events WHERE event_id = $1`, ev.GetEventId()).Scan(&owner); err != nil || owner != g.owner {
		t.Errorf("stored owner = %q (%v), want %q", owner, err, g.owner)
	}
}

func TestGRPCCreateEvent_unsetAndZeroShotLimitBothMeanUnlimitedOnAFreshCategory(t *testing.T) {
	// Arrange
	g := newGRPCFixture(t)

	// Act
	unset, unsetErr := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{CategoryCode: "ulang_tahun", Name: "A", EventDate: "2026-12-05"})
	zero, zeroErr := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{CategoryCode: "ulang_tahun", Name: "B", EventDate: "2026-12-05", ShotLimit: proto.Int32(0)})

	// Assert
	if unsetErr != nil || zeroErr != nil {
		t.Fatalf("errors: %v / %v", unsetErr, zeroErr)
	}
	if unset.GetEvent().ShotLimit != nil || zero.GetEvent().ShotLimit != nil {
		t.Errorf("shot limits = %v / %v, want both unset (unlimited)", unset.GetEvent().ShotLimit, zero.GetEvent().ShotLimit)
	}
	if unset.GetEvent().GetRevealMode() != eventv1.RevealMode_REVEAL_MODE_INSTANT || unset.GetEvent().GetRevealAt() != nil {
		t.Errorf("reveal = %v at %v, want instant with no reveal time", unset.GetEvent().GetRevealMode(), unset.GetEvent().GetRevealAt())
	}
}

func TestGRPCCreateEvent_appliesOverridesAndRejectsBadInput(t *testing.T) {
	// Arrange
	g := newGRPCFixture(t)

	// Act
	delayed, delayedErr := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{
		CategoryCode: "ulang_tahun", Name: "A", EventDate: "2026-12-05",
		RevealMode: eventv1.RevealMode_REVEAL_MODE_DELAYED.Enum(), RevealDelayHours: proto.Int32(48),
	})
	_, missingName := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{CategoryCode: "ulang_tahun", EventDate: "2026-12-05"})
	_, unknownCategory := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{CategoryCode: "konser", Name: "A", EventDate: "2026-12-05"})
	_, unspecifiedMode := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{
		CategoryCode: "ulang_tahun", Name: "A", EventDate: "2026-12-05", RevealMode: eventv1.RevealMode_REVEAL_MODE_UNSPECIFIED.Enum(),
	})

	// Assert
	if delayedErr != nil || delayed.GetEvent().GetRevealMode() != eventv1.RevealMode_REVEAL_MODE_DELAYED || delayed.GetEvent().GetRevealAt() == nil {
		t.Errorf("delayed override = %v, %v", delayed, delayedErr)
	}
	if st := grpcStatus(t, missingName, codes.InvalidArgument); !strings.Contains(st.Message(), "name is required") {
		t.Errorf("message = %q", st.Message())
	}
	if st := grpcStatus(t, unknownCategory, codes.InvalidArgument); !strings.Contains(st.Message(), "unknown category") {
		t.Errorf("message = %q", st.Message())
	}
	grpcStatus(t, unspecifiedMode, codes.InvalidArgument)
}

func TestGRPCProtectedCalls_requireASession(t *testing.T) {
	// Arrange
	g := newGRPCFixture(t)

	// Act
	_, createErr := g.client.CreateEvent(context.Background(), &eventv1.CreateEventRequest{CategoryCode: "ulang_tahun", Name: "A", EventDate: "2026-12-05"})
	_, getErr := g.client.GetEvent(context.Background(), &eventv1.GetEventRequest{EventId: "00000000-0000-4000-8000-000000000000"})
	_, badTokenErr := g.client.GetEvent(withToken("nope"), &eventv1.GetEventRequest{EventId: "00000000-0000-4000-8000-000000000000"})

	// Assert
	grpcStatus(t, createErr, codes.Unauthenticated)
	grpcStatus(t, getErr, codes.Unauthenticated)
	grpcStatus(t, badTokenErr, codes.Unauthenticated)
	var rows int
	if err := g.conn.QueryRow(`SELECT count(*) FROM events`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("events stored = %d (%v), want 0", rows, err)
	}
}

func TestGRPCGetEvent_returnsOnlyTheOwnersEvent(t *testing.T) {
	// Arrange
	g := newGRPCFixture(t)
	created, err := g.client.CreateEvent(withToken("owner-token"), &eventv1.CreateEventRequest{CategoryCode: "ulang_tahun", Name: "Pribadi", EventDate: "2026-12-05"})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	id := created.GetEvent().GetEventId()

	// Act
	owner, ownerErr := g.client.GetEvent(withToken("owner-token"), &eventv1.GetEventRequest{EventId: id})
	_, otherErr := g.client.GetEvent(withToken("other-token"), &eventv1.GetEventRequest{EventId: id})
	_, malformedErr := g.client.GetEvent(withToken("owner-token"), &eventv1.GetEventRequest{EventId: "not-a-uuid"})

	// Assert
	if ownerErr != nil || owner.GetEvent().GetEventId() != id || owner.GetEvent().GetShortLink() != created.GetEvent().GetShortLink() {
		t.Fatalf("owner read = %v, %v", owner, ownerErr)
	}
	for name, e := range map[string]error{"other host": otherErr, "malformed id": malformedErr} {
		st := grpcStatus(t, e, codes.NotFound)
		if strings.Contains(st.Message(), "Pribadi") {
			t.Errorf("%s response leaks event data: %q", name, st.Message())
		}
	}
}

func TestGRPCErrors_areGenericForInternalFailures(t *testing.T) {
	// Arrange
	internal := errors.New("pq: password authentication failed for user sela")
	client := startEventGRPC(t, stubEvents{err: internal}, map[string]string{"t": "host-1"})

	// Act
	_, listErr := client.ListEventCategories(context.Background(), &eventv1.ListEventCategoriesRequest{})
	_, createErr := client.CreateEvent(withToken("t"), &eventv1.CreateEventRequest{CategoryCode: "ulang_tahun", Name: "A", EventDate: "2026-12-05"})
	_, getErr := client.GetEvent(withToken("t"), &eventv1.GetEventRequest{EventId: "00000000-0000-4000-8000-000000000000"})

	// Assert
	for name, err := range map[string]error{"list": listErr, "create": createErr, "get": getErr} {
		st := grpcStatus(t, err, codes.Internal)
		if st.Message() != "internal error" {
			t.Errorf("%s message = %q, want the generic message", name, st.Message())
		}
	}
}

func TestGRPCServer_refusesCallsWithoutAHostEvenWithoutTheInterceptor(t *testing.T) {
	// Defense in depth: the handlers must not trust that an interceptor is installed.
	srv := event.NewGRPCServer(stubEvents{})

	_, createErr := srv.CreateEvent(context.Background(), &eventv1.CreateEventRequest{})
	_, getErr := srv.GetEvent(context.Background(), &eventv1.GetEventRequest{})

	grpcStatus(t, createErr, codes.Unauthenticated)
	grpcStatus(t, getErr, codes.Unauthenticated)
}
