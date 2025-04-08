package postgres

import (
	"context"
	"errors"
	"testing"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/indexd/accounts"
	"go.uber.org/zap/zaptest"
	"lukechampine.com/frand"
)

func TestPinSlabs(t *testing.T) {
	store := initPostgres(t, zaptest.NewLogger(t).Named("postgres"))
	account := proto.Account{1}

	// pin without an account
	slabIDs, err := store.PinSlabs(context.Background(), account, []SlabPinParams{{}})
	if !errors.Is(err, accounts.ErrNotFound) {
		t.Fatal("expected ErrNotFound, got", err)
	}

	// add account
	if err := store.AddAccount(context.Background(), types.PublicKey(account)); err != nil {
		t.Fatal("failed to add account:", err)
	}

	// helper to create slabs
	newSlab := func(i byte) (SlabID, SlabPinParams) {
		slab := SlabPinParams{
			EncryptionKey: [32]byte{i},
			MinShards:     10,
			Sectors: []SectorPinParams{
				{
					Root:    frand.Entropy256(),
					HostKey: frand.Entropy256(),
				},
				{
					Root:    frand.Entropy256(),
					HostKey: frand.Entropy256(),
				},
			},
		}
		hasher := types.NewHasher()
		for _, sector := range slab.Sectors {
			hasher.E.Write(sector.Root[:])
		}
		return SlabID(hasher.Sum()), slab
	}

	// pins slabs
	_, slab1 := newSlab(1)
	_, slab2 := newSlab(2)
	slabs := []SlabPinParams{slab1, slab2}
	slabIDs, err = store.PinSlabs(context.Background(), proto.Account{1}, slabs)
	if err != nil {
		t.Fatal(err)
	} else if len(slabIDs) != len(slabs) {
		t.Fatalf("expected %d slab IDs, got %d", len(slabs), len(slabIDs))
	}

	// TODO: fetch slab from DB and assert it matches
	//
	// TODO: pin same slab again and assert
}
