package postgres

import (
	"testing"

	"go.sia.tech/core/types"
	"go.sia.tech/indexd/api/app"
	"go.uber.org/zap/zaptest"
)

func TestAppConnectKeys(t *testing.T) {
	ctx := t.Context()
	store := initPostgres(t, zaptest.NewLogger(t).Named("postgres"))

	if ok, err := store.ValidAppConnectKey(ctx, "foobar"); err != nil {
		t.Fatal("failed to validate app connect key:", err)
	} else if ok {
		t.Fatal("expected app connect key to be invalid")
	}

	if err := store.AddAppConnectKey(ctx, app.AddConnectKey{
		Key:           "foobar",
		Description:   "Test key",
		RemainingUses: 1,
	}); err != nil {
		t.Fatal("failed to add app connect key:", err)
	}

	if ok, err := store.ValidAppConnectKey(ctx, "foobar"); err != nil {
		t.Fatal("failed to validate app connect key:", err)
	} else if !ok {
		t.Fatal("expected app connect key to be valid")
	}

	if err := store.UseAppConnectKey(ctx, "foobar", types.GeneratePrivateKey().PublicKey()); err != nil {
		t.Fatal("failed to remove app connect key:", err)
	}

	if ok, err := store.ValidAppConnectKey(ctx, "foobar"); err != nil {
		t.Fatal("failed to validate app connect key:", err)
	} else if ok {
		t.Fatal("expected app connect key to be invalid")
	}

	if err := store.AddAppConnectKey(ctx, app.AddConnectKey{
		Key:           "foobar",
		Description:   "Test key",
		RemainingUses: 1,
	}); err != nil {
		t.Fatal("failed to add app connect key:", err)
	}

	if ok, err := store.ValidAppConnectKey(ctx, "foobar"); err != nil {
		t.Fatal("failed to validate app connect key:", err)
	} else if !ok {
		t.Fatal("expected app connect key to be valid")
	}

	if err := store.DeleteAppConnectKey(ctx, "foobar"); err != nil {
		t.Fatal("failed to delete app connect key:", err)
	}

	if ok, err := store.ValidAppConnectKey(ctx, "foobar"); err != nil {
		t.Fatal("failed to validate app connect key:", err)
	} else if ok {
		t.Fatal("expected app connect key to be invalid")
	}
}
