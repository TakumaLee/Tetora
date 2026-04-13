package lifecycle

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// StartWatchdog launches a self-liveness monitor that periodically pings the
// local /healthz endpoint. If the endpoint fails to respond for consecutive
// checks exceeding the failure limit, the process exits so that the OS-level
// supervisor (launchd KeepAlive / systemd Restart=on-failure) can restart it.
func StartWatchdog(ctx context.Context, cfg config.WatchdogConfig, listenAddr string) {
	if !cfg.Enabled {
		return
	}

	interval := cfg.IntervalOrDefault()
	limit := cfg.FailureLimitOrDefault()
	timeout := cfg.TimeoutPerPingOrDefault()
	url := fmt.Sprintf("http://%s/healthz", listenAddr)

	log.Info("watchdog started", "interval", interval, "failureLimit", limit, "timeout", timeout)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		consecutive := 0
		client := &http.Client{Timeout: timeout}

		for {
			select {
			case <-ctx.Done():
				log.Info("watchdog stopped")
				return
			case <-ticker.C:
				if ok := pingHealthz(client, url); ok {
					if consecutive > 0 {
						log.Info("watchdog recovered", "previousFailures", consecutive)
					}
					consecutive = 0
				} else {
					consecutive++
					log.Warn("watchdog health check failed", "consecutive", consecutive, "limit", limit)
					if consecutive >= limit {
						log.Error("watchdog failure limit reached, exiting for supervisor restart",
							"consecutive", consecutive, "url", url)
						os.Exit(1)
					}
				}
			}
		}
	}()
}

func pingHealthz(client *http.Client, url string) bool {
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
