package gen_test

import (
	"sort"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"

	authv1 "github.com/Briyantama/SELA/gen/go/auth/v1"
	eventv1 "github.com/Briyantama/SELA/gen/go/event/v1"
)

func methodNames(desc grpc.ServiceDesc) []string {
	names := make([]string, 0, len(desc.Methods))
	for _, m := range desc.Methods {
		names = append(names, m.MethodName)
	}
	sort.Strings(names)
	return names
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func field(t *testing.T, msg protoreflect.Message, name string) protoreflect.FieldDescriptor {
	t.Helper()
	fd := msg.Descriptor().Fields().ByName(protoreflect.Name(name))
	if fd == nil {
		t.Fatalf("%s has no field %q", msg.Descriptor().FullName(), name)
	}
	return fd
}

func TestAuthService_exposesTheOTPFlow(t *testing.T) {
	// Arrange
	want := []string{"RequestOtp", "VerifyOtp"}

	// Act
	got := methodNames(authv1.AuthService_ServiceDesc)

	// Assert
	if !equal(got, want) {
		t.Fatalf("AuthService methods = %v, want %v", got, want)
	}
}

func TestAuthMessages_carryTheFieldsTheHandlersNeed(t *testing.T) {
	tests := []struct {
		name   string
		msg    protoreflect.Message
		fields []string
	}{
		{"RequestOtpRequest", (&authv1.RequestOtpRequest{}).ProtoReflect(), []string{"email"}},
		{"RequestOtpResponse", (&authv1.RequestOtpResponse{}).ProtoReflect(), []string{"expires_in_seconds"}},
		{"VerifyOtpRequest", (&authv1.VerifyOtpRequest{}).ProtoReflect(), []string{"email", "code"}},
		{"VerifyOtpResponse", (&authv1.VerifyOtpResponse{}).ProtoReflect(), []string{"session_token", "host_id", "is_new_host", "session_expires_in_seconds"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range tc.fields {
				field(t, tc.msg, name)
			}
		})
	}
}

func TestEventService_exposesCategoriesCreateAndGet(t *testing.T) {
	// Arrange
	want := []string{"CreateEvent", "GetEvent", "ListEventCategories"}

	// Act
	got := methodNames(eventv1.EventService_ServiceDesc)

	// Assert
	if !equal(got, want) {
		t.Fatalf("EventService methods = %v, want %v", got, want)
	}
}

func TestCreateEventRequest_optionalSettingsHavePresence(t *testing.T) {
	// A host who does not set a shot limit or reveal mode gets the category default,
	// so "unset" must be distinguishable from 0 / unspecified.
	msg := (&eventv1.CreateEventRequest{}).ProtoReflect()

	for _, name := range []string{"shot_limit", "reveal_mode", "reveal_delay_hours"} {
		if !field(t, msg, name).HasPresence() {
			t.Errorf("CreateEventRequest.%s must track presence (proto3 optional)", name)
		}
	}
	for _, name := range []string{"category_code", "name", "event_date"} {
		field(t, msg, name)
	}
}

func TestEvent_carriesTheShortLinkAndQRURLs(t *testing.T) {
	msg := (&eventv1.Event{}).ProtoReflect()

	for _, name := range []string{"short_link", "qr_png_url", "qr_svg_url"} {
		field(t, msg, name)
	}
}

func TestEventCategory_exposesTheEffectiveDefaultsAfterFallbacks(t *testing.T) {
	// UIs pre-fill the create form from these, so undefined (tbd) defaults are already resolved.
	msg := (&eventv1.EventCategory{}).ProtoReflect()

	field(t, msg, "effective_reveal_mode")
	for _, name := range []string{"effective_shot_limit", "effective_reveal_delay_hours"} {
		if !field(t, msg, name).HasPresence() {
			t.Errorf("EventCategory.%s must track presence (unset = unlimited / not delayed)", name)
		}
	}
}

func TestCreateEventRequest_hasNoHostIdentity(t *testing.T) {
	// The owner comes from the authenticated session, never from the request body (FSD 3.4).
	msg := (&eventv1.CreateEventRequest{}).ProtoReflect()

	if msg.Descriptor().Fields().ByName("host_id") != nil {
		t.Fatal("CreateEventRequest must not accept a host_id; ownership comes from the session")
	}
}
