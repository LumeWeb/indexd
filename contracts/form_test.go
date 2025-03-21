package contracts

import (
	"context"
	"net"
	"slices"
	"testing"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/indexd/hosts"
	"go.uber.org/zap"
)

type formContractCall struct {
	hk       types.PublicKey
	addr     string
	settings proto.HostSettings
	params   proto.RPCFormContractParams
}

type contractFormerMock struct {
	calls []formContractCall
}

func (cf *contractFormerMock) Calls() []formContractCall {
	return slices.Clone(cf.calls)
}

func (cf *contractFormerMock) FormContract(ctx context.Context, hk types.PublicKey, addr string, settings proto.HostSettings, params proto.RPCFormContractParams) (rhp.RPCFormContractResult, error) {
	cf.calls = append(cf.calls, formContractCall{
		hk:       hk,
		addr:     addr,
		settings: settings,
		params:   params,
	})
	return rhp.RPCFormContractResult{
		FormationSet: rhp.TransactionSet{
			Transactions: []types.V2Transaction{
				{
					MinerFee: types.Siacoins(1),
				},
			},
		},
	}, nil
}

type scannerMock struct {
	settings map[types.PublicKey]proto.HostSettings

	store *storeMock
}

// Scanner is a convenience method to create a scanner from a store mock. The
// scanner contains all the settings of the hosts from the mocked store and will
// be updating the store upon scanning.
func (s *storeMock) Scanner() *scannerMock {
	scannerMock := &scannerMock{
		store:    s,
		settings: map[types.PublicKey]proto.HostSettings{},
	}
	for _, host := range s.hosts {
		scannerMock.settings[host.PublicKey] = host.Settings
	}
	return scannerMock
}

// ScanHost returns the preconfigured settings for the host or no settings to
// simulate a failing scan. Upon success, the underlying store is updated.
func (s *scannerMock) ScanHost(ctx context.Context, hk types.PublicKey) (proto.HostSettings, error) {
	settings, ok := s.settings[hk]
	if !ok {
		return proto.HostSettings{}, nil
	}
	s.store.UpdateHostSettings(hk, settings)
	return settings, nil
}

func TestPerformContractFormation(t *testing.T) {
	cmMock := &chainManagerMock{}
	syncerMock := &syncerMock{}

	goodUsability := hosts.Usability{
		Uptime:              true,
		MaxContractDuration: true,
		MaxCollateral:       true,
		ProtocolVersion:     true,
		PriceValidity:       true,
		AcceptingContracts:  true,

		ContractPrice:   true,
		Collateral:      true,
		StoragePrice:    true,
		IngressPrice:    true,
		EgressPrice:     true,
		FreeSectorPrice: true,
	}

	const (
		period = 100
		wanted = 5
	)
	hk1 := types.PublicKey{1}

	// prepare settings which will cause hosts to either be good for forming contracts or not
	badSettings := proto.HostSettings{AcceptingContracts: false}
	goodSettings := proto.HostSettings{
		AcceptingContracts: true,
		RemainingStorage:   minRemainingStorage,
	}

	// create the store with multiple hosts, all of which start with bad
	// settings since the scanner will update them
	store := &storeMock{
		hosts: map[types.PublicKey]hosts.Host{
			// good host
			hk1: {
				PublicKey: hk1,
				Networks: []net.IPNet{
					{IP: net.IP{127, 0, 0, 1}, Mask: net.CIDRMask(24, 32)},
				},
				Settings:  badSettings,
				Usability: goodUsability,
			},
		},
	}

	scanner := store.Scanner()
	scanner.settings[hk1] = goodSettings

	cf := &contractFormerMock{}
	contracts := newContractManager(cmMock, cf, scanner, store, syncerMock, &walletMock{})

	// perform formations
	if err := contracts.performContractFormation(context.Background(), period, wanted, zap.NewNop()); err != nil {
		t.Fatal(err)
	}

	// assert that the we attempted to form contracts with the right hosts,
	// settings and params
	calls := cf.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %v", len(calls))
	}
}
