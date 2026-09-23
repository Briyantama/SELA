package event

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventv1 "github.com/Briyantama/SELA/gen/go/event/v1"
	"github.com/Briyantama/SELA/services/auth"
)

// GRPCServer exposes the event use cases as the sela.event.v1.EventService gRPC service. The owner
// comes from the host that the auth interceptor placed in the context, never from a request field.
type GRPCServer struct {
	eventv1.UnimplementedEventServiceServer
	events Events
}

// NewGRPCServer returns the gRPC adapter for the use cases.
func NewGRPCServer(events Events) *GRPCServer {
	return &GRPCServer{events: events}
}

// ListEventCategories returns the presets with their effective defaults. It is public.
func (s *GRPCServer) ListEventCategories(ctx context.Context, _ *eventv1.ListEventCategoriesRequest) (*eventv1.ListEventCategoriesResponse, error) {
	cats, err := s.events.ListCategories(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	out := make([]*eventv1.EventCategory, 0, len(cats))
	for _, c := range cats {
		out = append(out, toCategoryProto(c))
	}
	return &eventv1.ListEventCategoriesResponse{Categories: out}, nil
}

// CreateEvent creates an event for the authenticated host.
func (s *GRPCServer) CreateEvent(ctx context.Context, req *eventv1.CreateEventRequest) (*eventv1.CreateEventResponse, error) {
	hostID, ok := auth.HostID(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, msgUnauthorized)
	}

	in := CreateInput{
		CategoryCode:     req.GetCategoryCode(),
		Name:             req.GetName(),
		EventDate:        req.GetEventDate(),
		ShotLimit:        optionalInt(req.ShotLimit),
		RevealDelayHours: optionalInt(req.RevealDelayHours),
	}
	if req.RevealMode != nil {
		mode := revealModeFromProto(req.GetRevealMode())
		in.RevealMode = &mode
	}

	ev, err := s.events.CreateEvent(ctx, hostID, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return &eventv1.CreateEventResponse{Event: toEventProto(ev)}, nil
}

// GetEvent returns the event only when the authenticated host owns it.
func (s *GRPCServer) GetEvent(ctx context.Context, req *eventv1.GetEventRequest) (*eventv1.GetEventResponse, error) {
	hostID, ok := auth.HostID(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, msgUnauthorized)
	}
	ev, err := s.events.GetEvent(ctx, hostID, req.GetEventId())
	if err != nil {
		return nil, grpcError(err)
	}
	return &eventv1.GetEventResponse{Event: toEventProto(ev)}, nil
}

// grpcError maps a use-case error to a status code. Internal details never reach the caller.
func grpcError(err error) error {
	var validation *ValidationError
	switch {
	case errors.As(err, &validation):
		return status.Error(codes.InvalidArgument, validation.Message)
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, msgNotFound)
	default:
		slog.Error("event request failed", "error", err)
		return status.Error(codes.Internal, msgInternal)
	}
}

func optionalInt(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func optionalInt32(v *int) *int32 {
	if v == nil {
		return nil
	}
	return proto.Int32(int32(*v))
}

func revealModeFromProto(m eventv1.RevealMode) string {
	switch m {
	case eventv1.RevealMode_REVEAL_MODE_INSTANT:
		return revealInstant
	case eventv1.RevealMode_REVEAL_MODE_DELAYED:
		return revealDelayed
	default:
		return "" // rejected by the service as an invalid reveal_mode
	}
}

func revealModeToProto(mode string) eventv1.RevealMode {
	switch mode {
	case revealInstant:
		return eventv1.RevealMode_REVEAL_MODE_INSTANT
	case revealDelayed:
		return eventv1.RevealMode_REVEAL_MODE_DELAYED
	default:
		return eventv1.RevealMode_REVEAL_MODE_UNSPECIFIED
	}
}

func shotLimitDefaultToProto(v string) eventv1.ShotLimitDefault {
	switch v {
	case "unlimited":
		return eventv1.ShotLimitDefault_SHOT_LIMIT_DEFAULT_UNLIMITED
	case "limited":
		return eventv1.ShotLimitDefault_SHOT_LIMIT_DEFAULT_LIMITED
	case "tbd":
		return eventv1.ShotLimitDefault_SHOT_LIMIT_DEFAULT_TBD
	default:
		return eventv1.ShotLimitDefault_SHOT_LIMIT_DEFAULT_UNSPECIFIED
	}
}

func revealDefaultToProto(v string) eventv1.RevealDefault {
	switch v {
	case revealInstant:
		return eventv1.RevealDefault_REVEAL_DEFAULT_INSTANT
	case revealDelayed:
		return eventv1.RevealDefault_REVEAL_DEFAULT_DELAYED
	case "tbd":
		return eventv1.RevealDefault_REVEAL_DEFAULT_TBD
	default:
		return eventv1.RevealDefault_REVEAL_DEFAULT_UNSPECIFIED
	}
}

func toCategoryProto(c Category) *eventv1.EventCategory {
	return &eventv1.EventCategory{
		Code:                      c.Code,
		Name:                      c.Name,
		ThemeKey:                  c.ThemeKey,
		ShotLimitDefault:          shotLimitDefaultToProto(c.ShotLimitDefault),
		DefaultShotLimit:          optionalInt32(c.DefaultShotLimit),
		RevealDefault:             revealDefaultToProto(c.RevealDefault),
		DefaultRevealDelayHours:   optionalInt32(c.DefaultRevealDelayHours),
		EffectiveShotLimit:        optionalInt32(c.EffectiveShotLimit),
		EffectiveRevealMode:       revealModeToProto(c.EffectiveRevealMode),
		EffectiveRevealDelayHours: optionalInt32(c.EffectiveRevealDelayHours),
	}
}

func toEventProto(e Event) *eventv1.Event {
	out := &eventv1.Event{
		EventId:      e.ID,
		CategoryCode: e.CategoryCode,
		Name:         e.Name,
		EventDate:    e.EventDate,
		Timezone:     e.Timezone,
		Status:       e.Status,
		ShortCode:    e.ShortCode,
		ShortLink:    e.ShortLink,
		QrPngUrl:     e.QRPNGURL,
		QrSvgUrl:     e.QRSVGURL,
		ShotLimit:    optionalInt32(e.ShotLimit),
		RevealMode:   revealModeToProto(e.RevealMode),
		Package:      e.Package,
		CreatedAt:    timestamppb.New(e.CreatedAt),
	}
	if e.RevealAt != nil {
		out.RevealAt = timestamppb.New(*e.RevealAt)
	}
	return out
}
