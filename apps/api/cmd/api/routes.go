package main

import (
	"net/http"

	"google.golang.org/grpc"

	authv1 "github.com/Briyantama/SELA/gen/go/auth/v1"
	eventv1 "github.com/Briyantama/SELA/gen/go/event/v1"
	"github.com/Briyantama/SELA/internal/health"
	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/event"
	"github.com/Briyantama/SELA/services/rbac"
)

// publicGRPCMethods are the only gRPC methods callable without a session: signing in and the
// category presets. Every other method is protected by the host interceptor (deny by default).
var publicGRPCMethods = []string{
	"/sela.auth.v1.AuthService/RequestOtp",
	"/sela.auth.v1.AuthService/VerifyOtp",
	"/sela.event.v1.EventService/ListEventCategories",
}

// newMux builds the HTTP routes: liveness, the auth endpoints, the event endpoints and the RBAC
// endpoints (GET /api/v1/auth/me, PATCH /api/v1/me/preferences). The event routes that touch a
// specific event are guarded by the auth handler's RequireHost middleware, and creating one
// additionally needs the events:create permission via the RBAC handler.
func newMux(authHTTP *auth.HTTPHandler, eventHTTP *event.HTTPHandler, rbacHTTP *rbac.HTTPHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/healthz", health.Handler())
	authHTTP.Register(mux)
	eventHTTP.Register(mux)
	rbacHTTP.Register(mux)
	return mux
}

// newGRPCServer builds the gRPC server with the auth and event services registered behind the
// host interceptor.
func newGRPCServer(flow auth.Flow, events event.Events) *grpc.Server {
	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.NewUnaryHostInterceptor(flow, publicGRPCMethods...)))
	authv1.RegisterAuthServiceServer(srv, auth.NewGRPCServer(flow))
	eventv1.RegisterEventServiceServer(srv, event.NewGRPCServer(events))
	return srv
}
