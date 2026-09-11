package main

import (
	"os/exec"
	"runtime"
)

// openBrowser makes a best-effort attempt to open url in the system's
// default web browser, using whichever OS-provided command does that on
// the current platform. It only starts the command and does not wait
// for the browser itself to open or for the command to exit, since on
// every platform below the command returns as soon as it has handed the
// URL off (to an already-running browser, most commonly), not when the
// browser window actually appears.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// rundll32 with url.dll's FileProtocolHandler is the standard
		// way to open a URL with whatever the user has set as their
		// default browser, without depending on cmd.exe's "start"
		// built-in (which has its own, harder to get right from
		// exec.Command, argument-quoting rules).
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		// Most other Unix-like systems (Linux distributions in
		// particular) ship xdg-open as the desktop-environment-neutral
		// way to open a URL.
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
