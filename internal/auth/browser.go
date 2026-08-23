package auth

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

var (
	browserLookupFn   = FindBrowserPath
	browserLauncherFn = defaultBrowserLauncher
)

func defaultBrowserLauncher(browserExe string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(browserExe, args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// SetBrowserLauncherForTesting overrides browser execution delegates to avoid opening real windows in tests
func SetBrowserLauncherForTesting(lookup func() (string, error), launcher func(string, []string) (*exec.Cmd, error)) func() {
	origLookup := browserLookupFn
	origLauncher := browserLauncherFn
	if lookup != nil {
		browserLookupFn = lookup
	}
	if launcher != nil {
		browserLauncherFn = launcher
	}
	return func() {
		browserLookupFn = origLookup
		browserLauncherFn = origLauncher
	}
}

// FindBrowserPath locates installed Chrome, Edge, Chromium, or Brave executable
func FindBrowserPath() (string, error) {
	for _, envKey := range []string{"BROWSER_PATH", "CHROME_PATH", "EDGE_PATH"} {
		if envVal := os.Getenv(envKey); envVal != "" {
			if _, err := os.Stat(envVal); err == nil {
				return envVal, nil
			}
		}
	}

	switch runtime.GOOS {
	case "windows":
		candidates := []string{
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("LocalAppData") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\BraveSoftware\Brave-Browser\Application\brave.exe`,
			os.Getenv("LocalAppData") + `\BraveSoftware\Brave-Browser\Application\brave.exe`,
		}
		for _, c := range candidates {
			if c != "" {
				if _, err := os.Stat(c); err == nil {
					return c, nil
				}
			}
		}
	case "darwin":
		candidates := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		for _, c := range candidates {
			if c != "" {
				if _, err := os.Stat(c); err == nil {
					return c, nil
				}
			}
		}
	default:
		candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave", "brave-browser"}
		for _, c := range candidates {
			if p, err := exec.LookPath(c); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no supported browser executable found (Chrome or Edge required)")
}

// closeBrowserGracefully attempts to close browser via CDP command first, then terminates process tree
func closeBrowserGracefully(cmd *exec.Cmd, port int) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Try sending Browser.close command to CDP target if port is provided
	if port > 0 {
		_ = closeCDPBrowser(port)
	}

	// Give the browser process up to 1.5 seconds to exit gracefully
	for i := 0; i < 15; i++ {
		if cmd.ProcessState != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	killProcessTree(cmd)
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)).Run()
	} else {
		_ = cmd.Process.Kill()
	}
}
