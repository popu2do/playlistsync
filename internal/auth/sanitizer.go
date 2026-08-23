package auth

import (
	"regexp"
)

var (
	cookieSecretRegex = regexp.MustCompile(`(?i)\b(sp_dc|sapisid|__secure-[a-z0-9_-]+|sid|hsid|ssid|apisid|nid|login_info|account_chooser|auth_token|access_token|refresh_token|csrf_token)=("?[^\s;,"']+"?)`)
	authHeaderRegex   = regexp.MustCompile(`(?i)\b(sapisidhash|bearer|basic)\s+([^\s;,"']+)`)
	jsonSecretRegex   = regexp.MustCompile(`(?i)("(?:sp_dc|sapisid|accessToken|access_token|refreshToken|refresh_token|client_secret|clientSecret|secret|Cookie|cookie|authorization)"\s*:\s*)"([^"]+)"`)
	querySecretRegex  = regexp.MustCompile(`(?i)([?&])(token|secret|sapisid|auth)=([^&\s]+)`)
)

// SanitizeSensitive scrubs raw credentials, tokens, cookies, and sensitive authorization headers from strings
func SanitizeSensitive(input string) string {
	if input == "" {
		return ""
	}
	s := cookieSecretRegex.ReplaceAllString(input, "$1=[REDACTED]")
	s = authHeaderRegex.ReplaceAllString(s, "$1 [REDACTED]")
	s = jsonSecretRegex.ReplaceAllString(s, `$1"[REDACTED]"`)
	s = querySecretRegex.ReplaceAllString(s, "$1$2=[REDACTED]")
	return s
}
