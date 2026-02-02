package daemon

import (
	"net/http"
	"net/url"
	"strings"
)

func rewriteLocation(value, host string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil {
		return value, false
	}
	if parsed.IsAbs() {
		parsed.Host = host
		return parsed.String(), true
	}
	return value, false
}

func rewriteSetCookieDomain(header http.Header, apexZone, host string) {
	cookies := header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	updated := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		updated = append(updated, rewriteCookieDomain(cookie, apexZone, host))
	}
	header.Del("Set-Cookie")
	for _, cookie := range updated {
		header.Add("Set-Cookie", cookie)
	}
}

func rewriteCookieDomain(value, apexZone, host string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(trimmed), "domain=") {
			if domainMatches(trimmed, apexZone) {
				parts[i] = " Domain=" + host
			}
			return strings.Join(parts, ";")
		}
	}
	_ = apexZone
	return value
}

func domainMatches(domainAttr, apexZone string) bool {
	if apexZone == "" {
		return true
	}
	value := strings.TrimSpace(domainAttr[len("domain="):])
	value = strings.TrimPrefix(value, ".")
	apex := strings.TrimPrefix(apexZone, ".")
	if value == apex {
		return true
	}
	return strings.HasSuffix(value, "."+apex)
}
