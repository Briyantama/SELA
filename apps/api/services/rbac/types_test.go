package rbac_test

import (
	"testing"

	"github.com/Briyantama/SELA/services/rbac"
)

func TestParsePermission(t *testing.T) {
	// Arrange
	ok := []string{"events:create", "reports:read", "user_admin:reset_password"}
	bad := []string{"", "events", "events:", ":create", "Events:create", "events:create:x", "events:cre ate", "1a:b"}

	// Act + Assert
	for _, code := range ok {
		if _, err := rbac.ParsePermission(code); err != nil {
			t.Errorf("ParsePermission(%q) = %v, want ok", code, err)
		}
	}
	for _, code := range bad {
		if _, err := rbac.ParsePermission(code); err == nil {
			t.Errorf("ParsePermission(%q) accepted, want an error", code)
		}
	}
}

func TestParsePermission_splitsResourceAndAction(t *testing.T) {
	// Act
	p, err := rbac.ParsePermission("events:create")
	if err != nil {
		t.Fatalf("ParsePermission: %v", err)
	}

	// Assert
	if p.Resource != "events" || p.Action != "create" {
		t.Errorf("Permission = %+v, want events/create", p)
	}
	if p.String() != "events:create" {
		t.Errorf("String() = %q, want events:create", p.String())
	}
}
