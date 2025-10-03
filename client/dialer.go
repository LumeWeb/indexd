package client

import (
	"context"
	"fmt"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/coreutils/rhp/v4/siamux"
	"go.uber.org/zap"
)

const dialTimeout = 10 * time.Second

// Dialer can be used to dial a host using the SiaMux protocol.
type Dialer struct {
	cm                       ChainManager
	revisionSubmissionBuffer uint64
	signer                   rhp.FormContractSigner
	store                    RevisionStore
	log                      *zap.Logger
}

// DialerOption is a functional option type for configuring the
// Dialer.
type DialerOption func(*Dialer)

// WithRevisionSubmissionBuffer sets the revision submission buffer for the
// Dialer.
func WithRevisionSubmissionBuffer(buffer uint64) DialerOption {
	if buffer == 0 {
		panic("revisionSubmissionBuffer mustn't be 0") // developer error
	}
	return func(c *Dialer) {
		c.revisionSubmissionBuffer = buffer
	}
}

// NewDialer creates a new Dialer.
func NewDialer(cm ChainManager, signer rhp.FormContractSigner, store RevisionStore, log *zap.Logger, opts ...DialerOption) *Dialer {
	d := &Dialer{
		cm:                       cm,
		revisionSubmissionBuffer: defaultRevisionSubmissionBuffer,
		signer:                   signer,
		store:                    store,
		log:                      log,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DialHost dials the host and returns a Client that can be used to interact
// with the host. It uses the SiaMux protocol to establish a connection and
// returns a host client that exposes the RPC methods defined in the RHP.
func (d *Dialer) DialHost(ctx context.Context, hk types.PublicKey, addr string) (*HostClient, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	tc, err := siamux.Dial(ctx, addr, hk)
	if err != nil {
		return nil, fmt.Errorf("failed to dial host: %w", err)
	}

	return newHostClient(hk, d.cm, tc, d.signer, d.store, d.revisionSubmissionBuffer, d.log.With(zap.Stringer("hostKey", hk))), nil
}
