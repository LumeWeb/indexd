package slabs_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/indexd/internal/testutils"
	"go.sia.tech/indexd/slabs"
	"lukechampine.com/frand"
)

func TestMigrationsE2E(t *testing.T) {
	// create cluster
	logger := testutils.NewLogger(false)
	cluster := testutils.NewCluster(t, testutils.WithLogger(logger), testutils.WithHosts(4), testutils.WithIndexer(testutils.WithSlabOptions(slabs.WithHealthCheckInterval(time.Second))))

	// create some more utxos
	indexer := cluster.Indexer
	cluster.ConsensusNode.MineBlocks(t, indexer.WalletAddr(), 5)
	time.Sleep(time.Second)

	// add an account
	a1 := types.GeneratePrivateKey()
	err := indexer.AccountsAdd(context.Background(), a1.PublicKey())
	if err != nil {
		t.Fatal(err)
	}

	// convenience variables
	acc := proto.Account(a1.PublicKey())
	app := indexer.App(a1)

	// fetch hosts
	time.Sleep(time.Second)
	hosts, err := app.Hosts(context.Background())
	if err != nil {
		t.Fatal(err)
	} else if len(hosts) != 4 {
		t.Fatalf("expected 4 hosts, got %d", len(hosts))
	}
	h1 := hosts[0]
	h2 := hosts[1]
	h3 := hosts[2]
	h4 := hosts[3]

	fmt.Println("h1", h1.PublicKey, "h2", h2.PublicKey, "h3", h3.PublicKey, "h4", h4.PublicKey)

	contracts, _ := indexer.Contracts(context.Background())
	for _, c := range contracts {
		fmt.Println("contract", c.ID, "host", c.HostKey, "good", c.Good)
	}

	// prepare sectors
	r1, s1 := newTestSector()
	r2, s2 := newTestSector()
	r3, s3 := newTestSector()

	fmt.Println("r1", r1, s1[:6])
	fmt.Println("r2", r2, s2[:6])
	fmt.Println("r3", r3, s3[:6])

	// upload sectors to hosts
	if _, err := indexer.HostClient(t, h1.PublicKey).WriteSector(context.Background(), h1.Settings.Prices, acc.Token(a1, h1.PublicKey), bytes.NewReader(s1[:]), proto.SectorSize); err != nil {
		t.Fatal(err)
	} else if _, err := indexer.HostClient(t, h2.PublicKey).WriteSector(context.Background(), h2.Settings.Prices, acc.Token(a1, h2.PublicKey), bytes.NewReader(s2[:]), proto.SectorSize); err != nil {
		t.Fatal(err)
	} else if _, err := indexer.HostClient(t, h3.PublicKey).WriteSector(context.Background(), h3.Settings.Prices, acc.Token(a1, h3.PublicKey), bytes.NewReader(s3[:]), proto.SectorSize); err != nil {
		t.Fatal(err)
	}

	// pin the slab
	slabID, err := app.PinSlab(context.Background(), slabs.SlabPinParams{
		EncryptionKey: frand.Entropy256(),
		MinShards:     1,
		Sectors: []slabs.SectorPinParams{
			{Root: r1, HostKey: h1.PublicKey},
			{Root: r2, HostKey: h2.PublicKey},
			{Root: r3, HostKey: h3.PublicKey},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// assert sectors were pinned
	time.Sleep(time.Second)
	pinned, err := app.Slab(context.Background(), slabID)
	if err != nil {
		t.Fatal(err)
	} else if len(pinned.Sectors) != 3 {
		t.Fatalf("expected 3 pinned sectors, got %d", len(pinned.Sectors))
	} else if pinned.Sectors[0].Root != r1 || pinned.Sectors[0].HostKey != h1.PublicKey {
		t.Fatalf("expected sector %s on host %s, got %s on host %s", r1, h1.PublicKey, pinned.Sectors[0].Root, pinned.Sectors[0].HostKey)
	}

	// add h1 to blocklist
	err = indexer.HostsBlocklistAdd(context.Background(), []types.PublicKey{h1.PublicKey}, "test blocklist reason")
	if err != nil {
		t.Fatal(err)
	}

	// assert sectors are still pinned
	time.Sleep(2 * time.Second)
	pinned, err = app.Slab(context.Background(), slabID)
	if err != nil {
		t.Fatal(err)
	} else if len(pinned.Sectors) != 3 {
		t.Fatalf("expected 3 pinned sectors, got %d", len(pinned.Sectors))
	} else if pinned.Sectors[0].Root != r1 || pinned.Sectors[0].HostKey != h4.PublicKey {
		t.Fatalf("expected sector %s on host %s, got %s on host %s", r1, h4.PublicKey, pinned.Sectors[0].Root, pinned.Sectors[0].HostKey)
	}
}

func newTestSector() (types.Hash256, [proto.SectorSize]byte) {
	var sector [proto.SectorSize]byte
	frand.Read(sector[:])
	root := proto.SectorRoot(&sector)
	return root, sector
}
