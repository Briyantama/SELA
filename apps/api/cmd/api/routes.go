package main

import (
	"net/http"

	"google.golang.org/grpc"

	authv1 "github.com/Briyantama/SELA/gen/go/auth/v1"
	"github.com/Briyantama/SELA/internal/health"
	"github.com/Briyantama/SELA/services/auth"
)

// newMux builds the HTTP routes: liveness plus the auth endpoints.
func newMux(authHTTP *auth.HTTPHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/healthz", health.Handler())
	authHTTP.Register(mux)
	return mux
}

// newGRPCServer builds the gRPC server with the auth service registered.
func newGRPCServer(flow auth.Flow) *grpc.Server {
	srv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(srv, auth.NewGRPCServer(flow))
	return srv
}
