package app

import (
	"context"
	"fmt"

	"aioverlay/internal/domain"
	"aioverlay/internal/tunnel"
)

func (a *App) ValidateVPN(ctx context.Context, cfg domain.VPNConfig) error {
	return tunnel.Validate(ctx, cfg)
}

func (a *App) VPNStatus() any { a.mu.RLock(); tun := a.tunnel; a.mu.RUnlock(); return tun.Status() }

func (a *App) ProbeVPN(ctx context.Context, target string) (tunnel.ProbeResult, error) {
	if !a.operation.TryRLock() {
		return tunnel.ProbeResult{}, fmt.Errorf("settings are being applied")
	}
	defer a.operation.RUnlock()
	a.mu.RLock()
	tun := a.tunnel
	a.mu.RUnlock()
	return tun.Probe(ctx, target)
}
