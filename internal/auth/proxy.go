package auth

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// DetectSystemProxy returns the active system proxy if configured, checking:
// 1. Environment variables: HTTP_PROXY, HTTPS_PROXY, http_proxy, https_proxy, ALL_PROXY, all_proxy
// 2. Windows registry Internet Settings (ProxyEnable & ProxyServer) on Windows
// 3. macOS scutil --proxy on Darwin
func DetectSystemProxy() string {
	// 1. Check environment variables
	envVars := []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy",
	}
	for _, env := range envVars {
		if val := strings.TrimSpace(os.Getenv(env)); val != "" {
			return formatProxyURL(val)
		}
	}

	// 2. Windows registry proxy detection
	if runtime.GOOS == "windows" {
		if winProxy := detectWindowsRegistryProxy(); winProxy != "" {
			return winProxy
		}
	}

	// 3. macOS proxy detection
	if runtime.GOOS == "darwin" {
		if macProxy := detectDarwinProxy(); macProxy != "" {
			return macProxy
		}
	}

	return ""
}

func detectWindowsRegistryProxy() string {
	cmd := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, "/v", "ProxyEnable")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "0x1") {
		return ""
	}

	serverCmd := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, "/v", "ProxyServer")
	serverOut, err := serverCmd.Output()
	if err != nil {
		return ""
	}

	lines := strings.Split(string(serverOut), "\n")
	for _, line := range lines {
		if strings.Contains(line, "ProxyServer") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				raw := parts[len(parts)-1]
				if strings.Contains(raw, "=") {
					var httpProxy, socksProxy string
					for _, pair := range strings.Split(raw, ";") {
						kv := strings.Split(pair, "=")
						if len(kv) == 2 {
							proto := strings.ToLower(strings.TrimSpace(kv[0]))
							val := strings.TrimSpace(kv[1])
							if proto == "http" || proto == "https" {
								httpProxy = val
							} else if proto == "socks" {
								socksProxy = "socks5://" + strings.TrimPrefix(val, "socks://")
							}
						}
					}
					if httpProxy != "" {
						return formatProxyURL(httpProxy)
					}
					if socksProxy != "" {
						return socksProxy
					}
				}
				return formatProxyURL(raw)
			}
		}
	}
	return ""
}

func detectDarwinProxy() string {
	cmd := exec.Command("scutil", "--proxy")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	str := string(out)
	if strings.Contains(str, "HTTPEnable : 1") {
		var host, port string
		for _, line := range strings.Split(str, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "HTTPProxy :") {
				host = strings.TrimSpace(strings.TrimPrefix(line, "HTTPProxy :"))
			}
			if strings.HasPrefix(line, "HTTPPort :") {
				port = strings.TrimSpace(strings.TrimPrefix(line, "HTTPPort :"))
			}
		}
		if host != "" && port != "" {
			return formatProxyURL(host + ":" + port)
		}
	}
	return ""
}

func formatProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "socks5://") {
		raw = "http://" + raw
	}
	if _, err := url.Parse(raw); err == nil {
		return raw
	}
	return ""
}

// ProxyFunc returns a Proxy function for http.Transport that routes through proxyURL while bypassing localhost
func ProxyFunc(proxyURL string) func(*http.Request) (*url.URL, error) {
	if proxyURL == "" {
		return http.ProxyFromEnvironment
	}
	pURL, err := url.Parse(proxyURL)
	if err != nil {
		return http.ProxyFromEnvironment
	}
	return func(req *http.Request) (*url.URL, error) {
		host := req.URL.Hostname()
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return nil, nil
		}
		return pURL, nil
	}
}
