package postgres

import (
	"context"
	"fmt"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/indexd/accounts"
)

type (
	// SlabID is the ID of a slab derived from the slab's sectors' roots.
	SlabID types.Hash256

	// Sector is a 4MiB sector stored on a host.
	Sector struct {
		// Root is the sector root of a 4MiB sector
		Root types.Hash256 `json:"root"`

		// ContractID is the ID of the contract the sector is pinned to.
		// 'nil' if the sector isn't pinned.
		ContractID *types.FileContractID `json:"contractID,omitempty"`

		// HostKey is the public key of the host that stores the sector data.
		// 'nil' if a host lost the sector and the sector requires migration
		HostKey *types.PublicKey `json:"host,omitempty"`
	}

	// Slab is a group of sectors that is encrypted, erasure-coded and uploaded
	// to hosts.
	Slab struct {
		ID            SlabID   `json:"id"`
		EncryptionKey [32]byte `json:"encryptionKey"`
	}

	// SectorPinParams describes an uploaded sector to be pinned.
	SectorPinParams struct {
		Root    types.Hash256   `json:"root"`
		HostKey types.PublicKey `json:"hostKey"`
	}

	// SlabPinParams is the input to PinSlabs
	SlabPinParams struct {
		EncryptionKey [32]byte          `json:"encryptionKey"`
		MinShards     uint              `json:"minShards"`
		Sectors       []SectorPinParams `json:"sectors"`
	}
)

// PinSlabs adds slabs to the database for pinning. The slabs are associated
// with the provided account.
func (s *Store) PinSlabs(ctx context.Context, account proto.Account, slabs []SlabPinParams) ([]SlabID, error) {
	if len(slabs) == 0 {
		return nil, nil
	}
	var ids []SlabID
	err := s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		var accountID int64
		err := tx.QueryRow(ctx, "SELECT id FROM accounts WHERE public_key = $1", sqlPublicKey(account)).Scan(&accountID)
		if err != nil {
			return fmt.Errorf("%w: %v", accounts.ErrNotFound, account)
		}
		for i, slab := range slabs {
			slabID, err := s.pinSlab(ctx, tx, accountID, slab)
			if err != nil {
				return fmt.Errorf("failed to pin slab %d: %w", i+1, err)
			}
			ids = append(ids, slabID)
		}
		return nil
	})
	return ids, err
}

func (s *Store) pinSlab(ctx context.Context, tx *txn, accountID int64, slab SlabPinParams) (SlabID, error) {
	hasher := types.NewHasher()
	for _, sector := range slab.Sectors {
		// create slab id from sector roots
		if _, err := hasher.E.Write(sector.Root[:]); err != nil {
			return SlabID{}, fmt.Errorf("failed to write sector root to hasher: %w", err)
		}
	}
	digest := SlabID(hasher.Sum())

	// insert slab, overwriting existing ones
	var slabID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO slabs (account_id, digest, encryption_key, min_shards)
		VALUES ($1, $2, $3, $4)
		RETURNING id
		`, accountID, sqlHash256(digest), sqlHash256(slab.EncryptionKey), slab.MinShards).Scan(&slabID)
	if err != nil {
		return SlabID{}, err
	}

	// insert sectors, overwriting existing ones
	for i, sector := range slab.Sectors {
		_, err := tx.Exec(ctx, `
			INSERT INTO sectors (sector_root, host_id, slab_id, slab_index)
			VALUES ($1, (SELECT id FROM hosts WHERE public_key = $2), $3, $4)
			RETURNING id
		`, sqlHash256(sector.Root), sqlPublicKey(sector.HostKey), slabID, i)
		if err != nil {
			return SlabID{}, fmt.Errorf("failed to insert sector %d: %w", i+1, err)
		}
	}

	return digest, nil
}
