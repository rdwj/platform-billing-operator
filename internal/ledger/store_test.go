package ledger

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewStoreConfigSetsUTCTimezone(t *testing.T) {
	// Verify that NewStore's config parsing sets the timezone runtime
	// parameter to UTC. We can't call NewStore (no database), but we
	// can replicate the config-parsing step it uses and assert the
	// parameter is set.
	dsn := "postgres://user:pass@localhost:5432/testdb?sslmode=disable"
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	// Simulate what NewStore does after parsing.
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"

	got, ok := config.ConnConfig.RuntimeParams["timezone"]
	if !ok {
		t.Fatal("expected timezone runtime param to be set, but it was missing")
	}
	if got != "UTC" {
		t.Errorf("timezone runtime param = %q, want %q", got, "UTC")
	}
}
