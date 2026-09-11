package postgres

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}

	code, err := runWithPostgres(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runWithPostgres(m *testing.M) (int, error) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("booking"),
		tcpostgres.WithUsername("booking"),
		tcpostgres.WithPassword("booking"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return 0, fmt.Errorf("start postgres: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(container) }()

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, fmt.Errorf("connection string: %w", err)
	}

	if err := Migrate(url); err != nil {
		return 0, err
	}

	testPool, err = Pool(ctx, url, 20)
	if err != nil {
		return 0, err
	}
	defer testPool.Close()

	return m.Run(), nil
}

// reset puts the seeded event back to the state migration 0002 leaves it in.
func reset(t *testing.T) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration test needs docker; running with -short")
	}

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `TRUNCATE bookings RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate bookings: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE seats SET status = 'available', version = 0`); err != nil {
		t.Fatalf("reset seats: %v", err)
	}
}
