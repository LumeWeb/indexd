package sdk

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/indexd/api/app"
)

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
}

// Connect requests permission to connect an application to the indexer.
// If the app is already connected, it returns (true, nil).
// If the user rejects the connection,
func Connect(ctx context.Context, indexerURL string, appKey types.PrivateKey, meta app.RegisterAppRequest) (bool, error) {
	client, err := app.NewClient(indexerURL, appKey)
	if err != nil {
		return false, err
	}

	if ok, err := client.CheckAppAuth(ctx); err != nil {
		return false, fmt.Errorf("failed to check app auth: %w", err)
	} else if ok {
		return true, nil
	}

	resp, err := client.RequestAppConnection(ctx, meta)
	if err != nil {
		return false, fmt.Errorf("failed to request app connection: %w", err)
	}
	openBrowser(resp.ResponseURL)

	ctx, cancel := context.WithDeadline(ctx, resp.Expiration)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Second):
			if ok, err := client.CheckRequestStatus(ctx, resp.StatusURL); err != nil {
				return false, fmt.Errorf("failed to check request status: %w", err)
			} else if ok {
				return true, nil
			}
		}
	}
}
