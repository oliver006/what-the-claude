package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	settings, err := loadSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load settings: %v\n", err)
		os.Exit(1)
	}

	listenAddr := flag.String("listen", "", "local listen address (overrides settings)")
	remoteURL := flag.String("remote", "", "remote endpoint base URL (overrides settings)")
	capture := flag.Bool("capture", settings.Capture, "write JSON log per session")
	flag.Parse()

	if *listenAddr != "" {
		settings.ListenAddr = *listenAddr
	}
	if *remoteURL != "" {
		settings.RemoteURL = *remoteURL
	}
	settings.Capture = *capture

	remote, err := url.Parse(settings.RemoteURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid remote URL: %v\n", err)
		os.Exit(1)
	}

	store := newStorage(sessionsDir())

	a := app.New()
	gui := newGUI(a, settings.ListenAddr, remote.String(), store)
	gui.loadFromDisk()

	handler := &proxy{
		remote:     remote,
		settings:   settings,
		store:      store,
		updateChan: gui.updateChan,
	}

	server := &http.Server{Addr: settings.ListenAddr, Handler: handler}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "http server error: %v\n", err)
			os.Exit(1)
		}
	}()

	go gui.listenForUpdates()
	go gui.periodicRefresh()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Printf("[main] received signal, shutting down...")
		server.Shutdown(context.Background())
		close(gui.updateChan)
		fyne.Do(func() {
			a.Quit()
		})
	}()

	gui.window.ShowAndRun()
}
