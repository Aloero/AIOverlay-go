package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"aioverlay/internal/app"
	"aioverlay/internal/buildinfo"
	"aioverlay/internal/control"
	"aioverlay/internal/desktop"
	"aioverlay/internal/domain"
	"aioverlay/internal/platform"
)

func main() {
	configPath := flag.String("config", resourcePath("config.json"), "portable configuration")
	headless := flag.Bool("headless", false, "development HTTP interface, no desktop UI")
	hidden := flag.Bool("no-browser", false, "legacy flag: start embedded Studio hidden (never opens a browser)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a, err := app.New(*configPath, resourcePath("runtime", "catalog", "models-dev-api.json"))
	if err != nil {
		desktop.ShowError("AIOverlay · ошибка запуска", err.Error())
		return
	}
	defer a.Close()
	cfg := a.GetConfig()
	platform.ConfigureProcessIdentity(buildinfo.Company, buildinfo.ProductName)
	server := control.New(cfg.ControlCenter.ListenAddress, a)
	server.SetContext(ctx)
	defer server.CancelRun()
	studio := desktop.New(desktop.Options{Title: cfg.App.Name + " · Pipeline Studio", Visible: cfg.ControlCenter.AutoOpen && !*hidden, Stealth: cfg.Overlay.Stealth, Handler: server.Handler(), ConfigPath: *configPath, Quit: cancel})
	var ov platform.Overlay
	ov, err = platform.NewOverlay(cfg.Overlay, platform.Callbacks{
		Capture: func() ([]byte, error) { return server.Capture() },
		Analyze: func(image []byte) {
			if _, e := server.StartImage(image); e != nil && ov != nil {
				ov.SetMeta(e.Error())
			}
		},
		Control: studio.Show,
		Cancel:  server.CancelRun,
		Quit:    cancel,
	})
	if err != nil {
		log.Printf("overlay: %v", err)
	}
	server.Capture = func() ([]byte, error) {
		if *headless {
			return platform.CaptureScreen()
		}
		return studio.Capture(platform.CaptureScreen)
	}
	if ov != nil {
		a.OnConfigChanged(func(next domain.Config) { _ = ov.Apply(next.Overlay); studio.SetStealth(next.Overlay.Stealth) })
		go func() {
			if e := ov.Run(ctx); e != nil {
				log.Printf("overlay: %v", e)
			}
		}()
		go followJob(ctx, server, ov)
	}
	if *headless {
		fmt.Printf("Development UI: http://%s/\nConfig: %s\n", cfg.ControlCenter.ListenAddress, *configPath)
		go func() {
			if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
				log.Print(e)
				cancel()
			}
		}()
		<-ctx.Done()
	} else {
		if err := studio.Run(ctx); err != nil {
			log.Print(err)
			cancel()
		}
	}
	server.CancelRun()
	if ov != nil {
		_ = ov.Close()
	}
	shutdown, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	_ = server.Shutdown(shutdown)
}

func followJob(ctx context.Context, s *control.Server, ov platform.Overlay) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastID, lastText, lastMeta string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job := s.CurrentJob()
			if job.ID == "" {
				continue
			}
			if job.ID != lastID {
				lastID = job.ID
				lastText = "Анализирую экран…"
				lastMeta = "TRYING"
				ov.SetText(lastText)
				ov.SetMeta(lastMeta)
				ov.Show()
			}
			text := job.Text
			meta := "TRYING"
			if job.Error != "" {
				text = "Ошибка запроса\n" + job.Error
				meta = "ERROR"
			}
			for _, ev := range job.Trace.Events {
				if ev.Attempt != nil {
					meta = ev.Attempt.ProviderID + " · " + ev.Attempt.ModelID
					if job.Running {
						meta = "TRYING · " + meta
					}
				}
			}
			if !job.Running && job.Trace.Winner != nil {
				meta = job.Trace.Winner.ProviderID + " · " + job.Trace.Winner.ModelID
			}
			if text != "" && text != lastText {
				ov.SetText(text)
				lastText = text
			}
			if meta != lastMeta {
				ov.SetMeta(meta)
				lastMeta = meta
			}
		}
	}
}
func resourcePath(parts ...string) string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(append([]string{filepath.Dir(exe)}, parts...)...))
	}
	candidates = append(candidates, filepath.Join(parts...))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(parts...)
}
