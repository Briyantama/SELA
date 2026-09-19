package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NewUnaryHostInterceptor is the gRPC counterpart of HTTPHandler.RequireHost. Every method requires
// a session presented as "authorization: Bearer <token>" metadata, except the fully qualified
// method names listed in publicMethods. Unlisted methods are protected, so a new RPC is
// deny-by-default. The authenticated host is available to handlers through HostID.
func NewUnaryHostInterceptor(flow Flow, publicMethods ...string) grpc.UnaryServerInterceptor {
	public := make(map[string]struct{}, len(publicMethods))
	for _, method := range publicMethods {
		public[method] = struct{}{}
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := public[info.FullMethod]; ok {
			return handler(ctx, req)
		}

		token, ok := bearerToken(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, msgUnauthorized)
		}
		hostID, err := flow.Authenticate(ctx, token)
		if err != nil {
			return nil, grpcError(err)
		}
		return handler(context.WithValue(ctx, hostContextKey{}, hostID), req)
	}
}

// bearerToken extracts the token from a single "authorization: Bearer <token>" metadata value.
func bearerToken(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	values := md.Get("authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, found := strings.Cut(strings.TrimSpace(values[0]), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
