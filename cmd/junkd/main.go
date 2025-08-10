package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/wallet"
	"go.sia.tech/indexd/api/app"
	"go.sia.tech/indexd/sdk"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"lukechampine.com/frand"
)

const (
	dataShards        = 2
	parityShards      = 4
	slabSize          = dataShards * proto.SectorSize
	redundantSlabSize = (dataShards + parityShards) * proto.SectorSize
	redundancy        = (dataShards + parityShards) / dataShards
)

var (
	appSecret  string
	indexerURL string

	logLevel zap.AtomicLevel
	logPath  string

	threads int

	elapsedMu sync.Mutex
	elapsed   []time.Duration
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

func init() {
	flag.StringVar(&indexerURL, "indexer.url", "http://localhost:9982", "the URL of the indexer API")
	flag.StringVar(&appSecret, "app.secret", "", "a secret used to derive the application key")

	flag.TextVar(&logLevel, "log.level", zap.NewAtomicLevelAt(zap.InfoLevel), "the log level to use")
	flag.StringVar(&logPath, "log.path", "", "the path to write the log to")

	flag.IntVar(&threads, "threads", 1, "the number of upload threads")

	flag.Parse()
}

func main() {
	log := newLogger()

	sk, err := loadPrivateKey()
	if err != nil {
		log.Fatal("failed to load private key", zap.Error(err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if connected, err := sdk.Connected(ctx, indexerURL, sk); err != nil {
		log.Fatal("failed to check app connection", zap.Error(err))
	} else if !connected {
		log.Info("app is not connected, requesting connection", zap.String("indexerURL", indexerURL))
		resp, err := sdk.ConnectApp(ctx, indexerURL, sk, app.RegisterAppRequest{
			Name:        "junkd Uploader",
			Description: "A tool to upload junk data to the indexer",
			LogoURL:     "https://example.com/logo.png",
			ServiceURL:  "https://example.com/service",
		})
		if err != nil {
			log.Fatal("failed to request app connection", zap.Error(err))
		}
		log.Info("waiting for app connection approval", zap.String("approvalURL", resp.ResponseURL), zap.Duration("expiration", time.Until(resp.Expiration)))

		authCtx, authCancel := context.WithDeadline(ctx, resp.Expiration)
		defer authCancel()

	top:
		for {
			select {
			case <-authCtx.Done():
				log.Fatal("timed out waiting for app connection approval", zap.Error(authCtx.Err()))
			case <-time.After(time.Second):
				if ok, err := sdk.Connected(authCtx, indexerURL, sk); err != nil {
					log.Fatal("failed to check app auth", zap.Error(err))
				} else if ok {
					break top
				}
			}
		}
	}

	log.Info("junkd connected")

	sdkClient, err := sdk.NewSDK(indexerURL, sk, sdk.WithLogger(log.Named("sdk")))
	if err != nil {
		log.Fatal("failed to create SDK client", zap.Error(err))
	}

	var wg sync.WaitGroup
	for n := 1; n <= threads; n++ {
		wg.Add(1)
		go func(log *zap.Logger) {
			defer wg.Done()
			log.Debug("starting upload thread")

		loop:
			for {
				// upload slab
				start := time.Now()
				slabs, err := sdkClient.Upload(ctx, io.LimitReader(frand.Reader, slabSize), sdk.WithRedundancy(dataShards, parityShards))
				if err != nil {
					log.Error("failed to upload slab, timing out for 5 minutes", zap.Error(err), zap.Duration("duration", time.Since(start)))
					if ok := <-waitFor(ctx, 5*time.Minute); ok {
						continue loop
					}
					break loop
				} else if len(slabs) != 1 {
					log.Error(fmt.Sprintf("expected 1 slab, got %d", len(slabs)))
					break loop
				}

				elapsedMu.Lock()
				elapsed = append(elapsed, time.Since(start))
				elapsedMu.Unlock()

				log.Info("upload completed", zap.Stringer("SlabID", slabs[0].ID), zap.Duration("duration", time.Since(start)), zap.String("speed", formatBpsString(redundantSlabSize, time.Since(start))))
			}
		}(log.Named(fmt.Sprintf("upload-thread-%d", n)))
	}
	go printUploadSpeeds(ctx, log)
	wg.Wait()

	log.Info("all upload threads finished, exiting")
}

func waitFor(ctx context.Context, d time.Duration) <-chan bool {
	c := make(chan bool, 1)
	go func() {
		select {
		case <-ctx.Done():
			c <- false
		case <-time.After(d):
			c <- true
		}
	}()
	return c
}

func loadPrivateKey() (types.PrivateKey, error) {
	if appSecret == "" {
		return types.PrivateKey{}, fmt.Errorf("app secret is required")
	}

	derived, err := pbkdf2.Key(sha256.New, appSecret, []byte("junkd-pk-salt"), 4096, 32)
	if err != nil {
		return types.PrivateKey{}, fmt.Errorf("failed to derive key: %w", err)
	}

	var seed [32]byte
	copy(seed[:], derived)
	return wallet.KeyFromSeed(&seed, 0), nil
}

func newLogger() *zap.Logger {
	cfg := zap.NewProductionEncoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncodeDuration = zapcore.MillisDurationEncoder
	return zap.New(zapcore.NewCore(zapcore.NewConsoleEncoder(cfg), zapcore.Lock(os.Stdout), logLevel))
}

func formatBpsString(b int64, t time.Duration) string {
	const units = "KMGTPE"
	const factor = 1000

	time := t.Truncate(time.Second).Seconds()
	if time <= 0 {
		return "0.00 bps"
	}

	// calculate bps
	speed := float64(b*8) / time

	// short-circuit for < 1000 bits/s
	if speed < factor {
		return fmt.Sprintf("%.2f bps", speed)
	}

	var i = -1
	for ; speed >= factor; i++ {
		speed /= factor
	}
	return fmt.Sprintf("%.2f %cbps", speed, units[i])
}

func printUploadSpeeds(ctx context.Context, log *zap.Logger) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			elapsedMu.Lock()
			if len(elapsed) > 1000 {
				elapsed = elapsed[len(elapsed)-1000:]
			}
			times := elapsed
			elapsedMu.Unlock()

			var avg time.Duration
			if len(times) == 0 {
				avg = time.Second
			} else {
				for _, t := range times {
					avg += t
				}
				avg /= time.Duration(len(times))
			}
			log.Info("average upload time", zap.String("averageSpeed", formatBpsString(int64(redundantSlabSize), avg)))
		}
	}
}
