// SPDX-License-Identifier: GPL-3.0-only
package relay

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Daofengql/EmbedDesk.Relay/licenses"
)

// Run executes the relay command using the supplied command-line arguments.
func Run(args []string) error {
	if len(args) == 1 && args[0] == "--licenses" {
		if err := printLicenses(licenses.Files, "."); err != nil {
			return err
		}
		return printLicenses(consoleAssets, "dist/licenses")
	}
	config, err := loadRelayConfig()
	if err != nil {
		return fmt.Errorf("configuration failed: %w", err)
	}
	if len(args) > 0 {
		if len(args) != 3 || args[0] != "--connection-code" {
			return errors.New("usage: relay [--licenses | --connection-code PUBLIC_URL NAME]")
		}
		code, err := relayConnectionCode(args[2], args[1], config.AdmissionKey)
		if err != nil {
			return fmt.Errorf("connection code export failed: %w", err)
		}
		fmt.Println(code)
		return nil
	}
	r, err := newRelayWithConfig(config)
	if err != nil {
		return errors.New("database initialization failed")
	}
	defer r.close()
	server := &http.Server{Addr: config.Listen, Handler: r.router(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
			_ = r.close()
		case <-shutdown:
		}
	}()
	defer close(shutdown)
	fmt.Printf("EmbedDesk relay protocol 1; bandwidth %d bits/s; HTTP only; console enabled: %t\n", config.BandwidthBitsPerSecond, config.ConsoleEnabled)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP listener failed")
	}
	return nil
}

func printLicenses(files fs.FS, root string) error {
	return fs.WalkDir(files, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Protocol-only builds contain no generated frontend notices.
			if path == "dist/licenses" && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !entry.IsDir() {
			data, err := fs.ReadFile(files, path)
			if err != nil {
				return err
			}
			fmt.Printf("\n=== %s ===\n%s\n", path, data)
		}
		return nil
	})
}
