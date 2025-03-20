package hosts

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"slices"

	proto4 "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/coreutils/rhp/v4/siamux"
)

type scanner struct{}

// Settings executes the RPCSettings RPC on the host.
func (c *scanner) Settings(ctx context.Context, hk types.PublicKey, addr string) (proto4.HostSettings, error) {
	t, err := siamux.Dial(ctx, addr, hk)
	if err != nil {
		return proto4.HostSettings{}, fmt.Errorf("failed to upgrade connection: %w", err)
	}
	defer t.Close()

	return rhp.RPCSettings(ctx, t)
}

var fallbackSites = []string{
	"https://1.1.1.1", // cloudflare
	"https://www.google.com",
	"https://www.amazon.com",
}

type pinger struct{}

// Online returns true if any of the fallback sites are reachable.
func (pinger) Online() bool {
	return slices.ContainsFunc(fallbackSites, isReachable)
}

// isReachable checks if the given URL is reachable via an HTTP HEAD request
func isReachable(url string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Head(url)
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true
	}
	return false
}
