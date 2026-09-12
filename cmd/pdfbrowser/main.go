// Command pdfbrowser is a small local web app for dropping a PDF (or
// other test document) onto a page in a browser and seeing how this
// package renders it - a quick, visual way to sanity-check rendering
// changes during development without writing a one-off test or running
// one of the other cmd/ examples by hand for every page. It is a
// developer tool, not a production end-user viewer: it serves exactly
// one document at a time to whatever browser tab is talking to it, with
// no authentication, no support for multiple simultaneous documents,
// and no attempt to be safe to expose beyond localhost.
//
// # Usage
//
//	go run ./cmd/pdfbrowser [-port N] [-no-browser] [-scale N] [-use-system-fonts]
//
// With no -port, the OS picks an available port; either way, the URL to
// open is printed to stdout as soon as the server is listening (the
// primary way to find it - the browser launch below is only a
// convenience, not something to rely on). Drag a PDF file onto the page
// that opens; its pages appear as a thumbnail list down the left side
// (rendered lazily, as each thumbnail scrolls into view) with the first
// page shown full-size in the main viewer. "Previous"/"Next" step
// through pages, clicking a thumbnail jumps straight to that page, and
// "Quit" tells the server to shut itself down.
//
// -scale sets the device-pixels-per-PDF-point used to render the
// full-size page image (see pdfviewer.RenderOptions.Scale), for anyone
// who wants a sharper or cheaper-to-render initial view than
// defaultPageScale's default; the frontend's own "scale" query
// parameter (server.go's handlePage) still overrides this per request.
//
// -use-system-fonts sets the initial state of the web UI's "Use system
// fonts" checkbox (see pdfviewer.WithFontSubstitution) - handy for
// leaving it on by default across restarts without having to click it
// every time. The checkbox can still be toggled from the page
// afterward exactly as before.
//
// Unless -no-browser is given, pdfbrowser also makes a best-effort
// attempt to open the URL in the system's default web browser (see
// openBrowser in browser.go) - this is a convenience, not a
// requirement, so a platform this doesn't recognize (or a failure to
// launch one) is not treated as an error; the printed URL is always
// there as a fallback. Closing that tab or window then also stops the
// server, exactly as clicking "Quit" would - see this function's own
// browserSession and server.go's /api/browser-closed handler. That only
// applies to the tab pdfbrowser itself opened, though: the printed URL
// opened by hand, another tab, or -no-browser's whole run all leave the
// server alone when closed, since there is then no single tab whose
// closing should be allowed to mean "shut down."
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"time"
)

func main() {
	port := flag.Int("port", 0, "TCP port to listen on (0 = let the OS choose an available port)")
	noBrowser := flag.Bool("no-browser", false, "do not attempt to open a web browser automatically")
	scale := flag.Float64("scale", defaultPageScale, "device-pixels-per-PDF-point used to render the full-size page image")
	useSystemFonts := flag.Bool("use-system-fonts", false, "initial state of the web UI's \"Use system fonts\" checkbox")
	flag.Parse()

	if *scale <= 0 {
		fmt.Fprintf(os.Stderr, "pdfbrowser: -scale must be greater than 0\n")
		os.Exit(1)
	}

	if err := run(*port, !*noBrowser, *scale, *useSystemFonts); err != nil {
		fmt.Fprintf(os.Stderr, "pdfbrowser: %v\n", err)
		os.Exit(1)
	}
}

// run does the actual work of main, but takes plain parameters and
// returns an error instead of touching flag or os.Exit directly - the
// same "keep main thin" split used by the other cmd/ examples in this
// repository (see cmd/pdfpreview/main.go's doc comment on
// renderPreviewToFile for why).
func run(port int, launchBrowser bool, pageScale float64, useSystemFonts bool) error {
	// Listening on 127.0.0.1 rather than all interfaces (0.0.0.0) keeps
	// this development tool reachable only from the machine it runs on,
	// which matches its intended use (see this file's package doc
	// comment) and needs no extra configuration to get right.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("listening on port %d: %w", port, err)
	}

	// When port was 0, the OS chose a free port for us; either way,
	// listener.Addr() reports whichever port is actually in use.
	actualPort := listener.Addr().(*net.TCPAddr).Port
	pageURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	fmt.Printf("pdfbrowser listening on %s\n", pageURL)

	browserSession := ""
	if launchBrowser {
		// The token is only put in the URL given to a browser that this
		// process launched. The page uses it to report when that browser
		// tab is closed; URLs opened independently cannot stop the server.
		browserSession = newBrowserSession()
		browserURL, err := url.Parse(pageURL)
		if err != nil {
			return fmt.Errorf("building browser URL: %w", err)
		}
		query := browserURL.Query()
		query.Set("session", browserSession)
		browserURL.RawQuery = query.Encode()
		pageURL = browserURL.String()

		if err := openBrowser(pageURL); err != nil {
			// Not fatal - see this file's package doc comment. The user
			// already has the URL printed above to open by hand.
			browserSession = ""
			fmt.Fprintf(os.Stderr, "pdfbrowser: could not open a browser automatically: %v\n", err)
		}
	}

	srv := newBrowserServer(pageScale, useSystemFonts)

	// quit is closed exactly once, either by a client POSTing
	// /api/quit (see handleQuit in server.go) or by this process
	// receiving an interrupt signal (Ctrl-C) below - whichever happens
	// first wins, and quitOnce makes closing it a second time (the
	// signal firing after a quit request already arrived, say) safe
	// instead of a panic.
	quit := make(chan struct{})
	var quitOnce sync.Once
	triggerQuit := func() { quitOnce.Do(func() { close(quit) }) }

	httpServer := &http.Server{Handler: newMux(srv, triggerQuit, browserSession)}

	// httpServer.Serve blocks until the server stops, so it runs in its
	// own goroutine; serveErr carries its result back to the select
	// below once that happens.
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	select {
	case <-quit:
	case <-ctx.Done():
	case err := <-serveErr:
		// The server stopped on its own (a listener error, most
		// likely) before anyone asked it to - report that.
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}

	// Shutdown stops accepting new connections and waits (up to the
	// timeout below) for in-flight requests - such as the /api/quit
	// request that may have triggered this very shutdown - to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}

	if err := <-serveErr; err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
