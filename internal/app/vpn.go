package app

import (
	"context"

	"aioverlay/internal/domain"
	"aioverlay/internal/tunnel"
)

func (a *App) ValidateVPN(ctx context.Context, cfg domain.VPNConfig) error {
	return tunnel.Validate(ctx, cfg)
}

func (a *App) VPNStatus() any { a.mu.RLock(); tun := a.tunnel; a.mu.RUnlock(); return tun.Status() }
