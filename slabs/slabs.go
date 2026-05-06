package slabs

import (
	"context"
	"errors"
	"fmt"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
)

const (
	// maxTotalShards is the maximum number of total shards (data + parity) allowed in a slab.
	maxTotalShards = 256
)

var (
	// ErrSlabNotFound is returned when a slab is not found in the database.
	ErrSlabNotFound = errors.New("slab not found")

	// ErrUnrecoverable is returned when a slab is unrecoverable, meaning it cannot be repaired or migrated.
	ErrUnrecoverable = errors.New("slab is unrecoverable")

	// ErrBadHosts is returned when attempting to pin a slab with too many
	// sectors on bad hosts.
	ErrBadHosts = errors.New("slab has too many sectors on bad hosts")

	// ErrMinShards is returned when attempting to pin a slab with an invalid
	// number of minimum shards, for example if `MinShards` exceeds the number
	// of sectors.
	ErrMinShards = errors.New("slab has invalid number of minimum shards")
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
		ID            SlabID        `json:"id"`
		EncryptionKey EncryptionKey `json:"encryptionKey"`
		MinShards     uint          `json:"minShards"`
		Sectors       []Sector      `json:"sectors"`
		PinnedAt      time.Time     `json:"pinnedAt"`
	}

	// A PinnedSector is a sector that has been pinned to a host.
	PinnedSector struct {
		Root    types.Hash256   `json:"root"`
		HostKey types.PublicKey `json:"hostKey"`
	}

	// SlabPinParams is the input to PinSlabs
	SlabPinParams struct {
		EncryptionKey EncryptionKey  `json:"encryptionKey"`
		MinShards     uint           `json:"minShards"`
		Sectors       []PinnedSector `json:"sectors"`
	}

	// A PinnedSlab is a slab that has been pinned to hosts.
	PinnedSlab struct {
		ID            SlabID         `json:"id"`
		EncryptionKey EncryptionKey  `json:"encryptionKey"`
		MinShards     uint           `json:"minShards"`
		Sectors       []PinnedSector `json:"sectors"`
	}

	// SlabObject identifies an object that references a slab.
	SlabObject struct {
		Account  proto.Account
		ObjectID types.Hash256
	}
)

// String implements the Stringer interface for SlabID.
func (s SlabID) String() string {
	return types.Hash256(s).String()
}

// MarshalText implements encoding.TextMarshaler.
func (s SlabID) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *SlabID) UnmarshalText(b []byte) error {
	return (*types.Hash256)(s).UnmarshalText(b)
}

// Digest computes the digest for the slab pin params.
func (s SlabPinParams) Digest() SlabID {
	return slabDigest(s.MinShards, s.EncryptionKey, s.Sectors)
}

// Digest computes the digest for the slab slice.
func (s SlabSlice) Digest() SlabID {
	return slabDigest(s.MinShards, s.EncryptionKey, s.Sectors)
}

// slabDigest creates a unique digest for a slab. It is important, that the same
// params always result in the same hash since we deduplicate slabs using it. So
// if one user makes the mistake of pinning a slab with a different encryption
// key, this shouldn't prevent other users from pinning the same slab with the
// correct key.
func slabDigest(minShards uint, ec [32]byte, sectors []PinnedSector) SlabID {
	hasher := types.NewHasher()
	hasher.E.WriteUint64(uint64(minShards))
	hasher.E.Write(ec[:])
	for _, sector := range sectors {
		hasher.E.Write(sector.Root[:])
	}
	return SlabID(hasher.Sum())
}

// Size returns the size of the slab in bytes including redundancy.
func (s SlabPinParams) Size() uint64 {
	return uint64(len(s.Sectors)) * proto.SectorSize
}

// DataSize returns the size of the slab's data in bytes before redundancy
// is applied.
func (s SlabPinParams) DataSize() uint64 {
	return uint64(s.MinShards) * proto.SectorSize
}

// Validate checks if the SlabPinParams are valid. It ensures that the
// encryption key is set, the minimum number of shards is met, and that there
// are no duplicate host keys or empty roots in the sectors.
func (s SlabPinParams) Validate() error {
	if s.EncryptionKey == ([32]byte{}) {
		return errors.New("encryption key is empty")
	} else if err := ValidateECParams(int(s.MinShards), len(s.Sectors)); err != nil {
		return err
	}

	hks := make(map[types.PublicKey]struct{}, len(s.Sectors))
	for i, sector := range s.Sectors {
		if sector.Root == (types.Hash256{}) {
			return fmt.Errorf("sector %d invalid: root is empty", i)
		} else if sector.HostKey == (types.PublicKey{}) {
			return fmt.Errorf("sector %d invalid: host key is empty", i)
		} else if _, exists := hks[sector.HostKey]; exists {
			return fmt.Errorf("sector %d is invalid: duplicate host key %q", i, sector.HostKey)
		}
		hks[sector.HostKey] = struct{}{}
	}

	return nil
}

// PinSlabs adds slabs to the database for pinning. The slabs are associated
// with the provided account.
func (m *SlabManager) PinSlabs(ctx context.Context, account proto.Account, nextIntegrityCheck time.Time, toPin ...SlabPinParams) ([]SlabID, error) {
	return m.store.PinSlabs(account, nextIntegrityCheck, toPin...)
}

// UnpinSlab removes the association between the account and the given slab. If
// this slab was only referenced by the given account, it will also be deleted.
// The sectors are potentially orphaned and will be removed by a background
// process.
func (m *SlabManager) UnpinSlab(ctx context.Context, account proto.Account, slabID SlabID) error {
	return m.store.UnpinSlab(account, slabID)
}

// Slabs returns the slabs with the given IDs from the database.
func (m *SlabManager) Slabs(ctx context.Context, account proto.Account, slabIDs []SlabID) ([]Slab, error) {
	return m.store.Slabs(account, slabIDs)
}

// PinnedSlab retrieves a pinned slab from the database by its ID.  If account
// is not nil, the last used field of that account will be updated.
func (m *SlabManager) PinnedSlab(ctx context.Context, account proto.Account, slabID SlabID) (PinnedSlab, error) {
	return m.store.PinnedSlab(account, slabID)
}

// SlabIDs returns the IDs of slabs associated with the given account. The IDs
// are returned in descending order of the `pinned_at` timestamp, which is the
// time when the slab was pinned to the indexer.
func (m *SlabManager) SlabIDs(ctx context.Context, account proto.Account, offset, limit int) ([]SlabID, error) {
	return m.store.SlabIDs(account, offset, limit)
}

// PruneSlabs prunes all pinned slabs of a user not currently connected to an
// object.
func (m *SlabManager) PruneSlabs(ctx context.Context, account proto.Account) error {
	return m.store.PruneSlabs(account)
}

// ValidateECParams checks the erasure coding parameters are
// acceptable. If they are not, an error is returned.
func ValidateECParams(dataShards, totalShards int) error {
	switch {
	case totalShards > maxTotalShards:
		return fmt.Errorf("total number of shards %d exceeds maximum of %d", totalShards, maxTotalShards)
	case dataShards == 0:
		return errors.New("data shards cannot be zero")
	case totalShards == 0:
		return errors.New("total shards cannot be zero")
	case dataShards > totalShards:
		return fmt.Errorf("data shards %d cannot be greater than total shards %d", dataShards, totalShards)
	case dataShards > 255:
		return fmt.Errorf("data shards %d exceeds maximum of 255", dataShards)
	case totalShards-dataShards > 255:
		return fmt.Errorf("parity shards %d exceeds maximum of 255", totalShards-dataShards)
	}

	redundancy := float64(totalShards) / float64(dataShards)
	const maxRedundancy = 4
	if redundancy > maxRedundancy {
		return fmt.Errorf("redundancy of %0.2f is too high", redundancy)
	}
	return nil
}
