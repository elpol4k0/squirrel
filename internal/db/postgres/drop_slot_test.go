package postgres

import (
	"testing"
)

// TestAdapter_DropSlot_MethodExists verifies the method compiles and is accessible.
// Real integration is covered by the testcontainers tests.
func TestAdapter_DropSlot_MethodExists(t *testing.T) {
	a := New("postgres://user:pw@localhost/db?replication=database")
	// the method must exist and be callable; we only verify the signature here
	var _ func(interface{}, string) error
	_ = a.DropSlot // assigning to blank identifier ensures the symbol resolves
}
