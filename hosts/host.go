package hosts

import (
	"net"
	"time"

	proto4 "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
)

type (
	// Host is a host on the network.
	Host struct {
		PublicKey              types.PublicKey     `json:"publicKey"`
		LastAnnouncement       time.Time           `json:"lastAnnouncement"`
		LastSuccessfulScan     time.Time           `json:"lastSuccessfulScan"`
		NextScan               time.Time           `json:"nextScan"`
		TotalScans             int                 `json:"totalScans"`
		FailedScans            int                 `json:"failedScans"`
		ConsecutiveFailedScans int                 `json:"consecutiveFailedScans"`
		Addresses              []chain.NetAddress  `json:"addresses"`
		Networks               []net.IPNet         `json:"networks"`
		Settings               proto4.HostSettings `json:"settings"`
	}
)
