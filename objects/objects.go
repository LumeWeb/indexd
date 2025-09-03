package objects

import (
	"errors"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/indexd/slabs"
)

type (
	// Object represents a collection of slabs that form an uploaded object.
	Object struct {
		Key       types.Hash256
		Slabs     []SlabSlice
		Meta      []byte
		CreatedAt time.Time
		UpdatedAt time.Time
	}

	// SlabSlice represents a slice of a slab that is part of an object.
	SlabSlice struct {
		SlabID slabs.SlabID
		Offset uint32
		Length uint32
	}
)

var (
	// ErrObjectNotFound is returned when an object is not found in the database.
	ErrObjectNotFound = errors.New("object not found")
)
