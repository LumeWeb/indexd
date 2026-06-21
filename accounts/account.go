package accounts

import (
	"errors"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
)

var (
	// ErrExists is returned by database operations that fail due to an account
	// already existing.
	ErrExists = errors.New("account already exists")

	// ErrNotFound is returned by database operations that fail due to an
	// account not being found.
	ErrNotFound = errors.New("account not found")

	// ErrNoQuota is returned when trying to create a connect key without a quota.
	ErrNoQuota = errors.New("quota is required")

	// ErrAccountStorageLimitExceeded is returned when an operation fails due
	// to the account exceeding its storage limit.  We use the term "app
	// storage limit" here because from the user's perspective, they will have
	// one connect key with multiple apps attached, each of which is
	// actually represented by an account in the database.
	ErrAccountStorageLimitExceeded = errors.New("app storage limit exceeded")
)

// Funding type constants for funding_events.fund_type column.
const (
	// FundingTypeAccount is the fund type for individual account funding.
	FundingTypeAccount = "account"
	// FundingTypePool is the fund type for pool funding.
	FundingTypePool = "pool"
)

type (
	// AddAccountOptions holds optional parameters for account creation.
	AddAccountOptions struct {
		MaxPinnedData uint64
	}

	// AddAccountOption is a functional option for configuring optional
	// parameters during account creation.
	AddAccountOption func(*AddAccountOptions)
)

// WithMaxPinnedData sets the maximum amount of data that can be pinned
func WithMaxPinnedData(maxPinnedData uint64) AddAccountOption {
	return func(opts *AddAccountOptions) {
		opts.MaxPinnedData = maxPinnedData
	}
}

type (
	// QueryAccountsOptions holds options for querying accounts.
	QueryAccountsOptions struct {
		ConnectKey *string
	}

	// QueryAccountsOpt is a functional option for querying accounts.
	QueryAccountsOpt func(o *QueryAccountsOptions)
)

// WithConnectKey filters the accounts by the connect key they are associated
// with.
func WithConnectKey(connectKey string) QueryAccountsOpt {
	return func(opt *QueryAccountsOptions) {
		opt.ConnectKey = &connectKey
	}
}

type (
	// Account represents an account in the indexer.
	Account struct {
		AccountKey           proto.Account `json:"accountKey"`
		ConnectKey           string        `json:"connectKey"`
		MaxPinnedData        uint64        `json:"maxPinnedData"`
		QuotaMaxPinnedData   uint64        `json:"quotaMaxPinnedData"`
		ConnectKeyPinnedData uint64        `json:"connectKeyPinnedData"`
		Ready                bool          `json:"ready"`
		PinnedData           uint64        `json:"pinnedData"`
		PinnedSize           uint64        `json:"pinnedSize"`
		App                  AppMeta       `json:"app"`
		LastUsed             time.Time     `json:"lastUsed"`
	}

	// HostAccount represents an ephemeral account on a host.
	HostAccount struct {
		AccountKey             proto.Account
		HostKey                types.PublicKey
		ConsecutiveFailedFunds int
		NextFund               time.Time
		FullStorage            bool
	}

	// HostPool represents a balance pool on a host.
	HostPool struct {
		ID                     int
		HostKey                types.PublicKey
		PoolKey                types.PrivateKey
		ConsecutiveFailedFunds int
		NextFund               time.Time
		FullStorage            bool
	}

	// PendingAttachment represents an account that needs to be attached to a
	// pool on a specific host.
	PendingAttachment struct {
		AccountKey proto.Account
		PoolKey    types.PrivateKey
	}

	// UpdateAccountRequest is the request body for the
	// [PUT] /account/:accountkey/limit endpoint.
	UpdateAccountRequest struct {
		MaxPinnedData *uint64 `json:"maxPinnedData"`
	}

	// QuotaFundInfo contains funding info for a quota. Active is the number
	// of active funding units and FullStorage is the subset of those that
	// have reached their storage limit.
	QuotaFundInfo struct {
		QuotaName       string
		FundTargetBytes uint64
		Active          uint64
		FullStorage     uint64
	}

	// FundingEvent represents a single funding event for an account on a host.
	FundingEvent struct {
		ID                     int64                `json:"id"`
		AccountKey             proto.Account        `json:"accountKey"`
		HostKey                types.PublicKey      `json:"hostKey"`
		ContractID             types.FileContractID `json:"contractID"`
		AmountSC               types.Currency       `json:"amountSc"`
		EstimatedUploadBytes   uint64               `json:"estimatedUploadBytes"`
		EstimatedDownloadBytes uint64               `json:"estimatedDownloadBytes"`
		FundType               string               `json:"fundType"`         // accounts.FundingTypeAccount or accounts.FundingTypePool
		PoolID                 *int                 `json:"poolID,omitempty"` // non-nil when FundType is "pool"
		CreatedAt              time.Time            `json:"createdAt"`
	}

	// FundingCursor is used to paginate through funding events.
	FundingCursor struct {
		After time.Time `json:"after"`
		ID    int64     `json:"id"`
	}
)
