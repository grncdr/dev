package agent

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const maxRewriteBodyBytes = 5 << 20 // 5 MiB

func RewriteLocationForTunnel(value, localHost, publicHost, localApex, publicApex string) (string, bool) {
	return rewriteLocationForTunnel(value, localHost, publicHost, localApex, publicApex)
}

func RewriteLocationForTunnelWithPeerSubdomains(value, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) (string, bool) {
	return rewriteLocationForTunnelWithPeerSubdomains(value, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains)
}

func RewriteSetCookieDomainForTunnel(header http.Header, localHost, publicHost, localApex, publicApex string) {
	rewriteSetCookieDomainForTunnel(header, localHost, publicHost, localApex, publicApex)
}

func RewriteSetCookieDomainForTunnelWithPeerSubdomains(header http.Header, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) {
	rewriteSetCookieDomainForTunnelWithPeerSubdomains(header, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains)
}

func RewriteRequestCookieDomainForTunnel(header http.Header, publicHost, localHost, publicApex, localApex string) {
	rewriteRequestCookieDomainForTunnel(header, publicHost, localHost, publicApex, localApex)
}

func RewriteRequestOriginForTunnel(header http.Header, publicHost, localHost, publicApex, localApex string) {
	rewriteRequestOriginForTunnel(header, publicHost, localHost, publicApex, localApex)
}

func ReplaceHostLocalToPublic(hostPort, localHost, publicHost, localApex, publicApex string) (string, bool) {
	return replaceHostLocalToPublic(hostPort, localHost, publicHost, localApex, publicApex)
}

func DerivePublicApex(localHost, publicHost, localApex string) (string, bool) {
	return derivePublicApex(localHost, publicHost, localApex)
}

func RewriteResponseBody(resp *http.Response, localHost, publicHost string) error {
	return rewriteResponseBody(resp, localHost, publicHost)
}

func RewriteResponseBodyForTunnel(resp *http.Response, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) error {
	return rewriteResponseBodyForTunnel(resp, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains)
}

func rewriteLocation(value, localApex, publicApex string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil {
		return value, false
	}
	if !parsed.IsAbs() {
		return value, false
	}
	rewritten, ok := replaceHostApex(parsed.Host, localApex, publicApex)
	if ok {
		parsed.Host = rewritten
		return parsed.String(), true
	}
	return value, false
}

func rewriteLocationForTunnel(value, localHost, publicHost, localApex, publicApex string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil {
		return value, false
	}
	if !parsed.IsAbs() {
		return value, false
	}
	if rewritten, ok := replaceHostLocalToPublic(parsed.Host, localHost, publicHost, localApex, publicApex); ok {
		parsed.Host = rewritten
		return parsed.String(), true
	}
	return value, false
}

func rewriteLocationForTunnelWithPeerSubdomains(value, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil {
		return value, false
	}
	if !parsed.IsAbs() {
		return value, false
	}
	if rewritten, ok := replaceHostLocalToPublicWithPeerSubdomains(parsed.Host, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains); ok {
		parsed.Host = rewritten
		return parsed.String(), true
	}
	return value, false
}

func rewriteSetCookieDomain(header http.Header, localApex, publicApex string) {
	cookies := header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	updated := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		updated = append(updated, rewriteCookieDomain(cookie, localApex, publicApex))
	}
	header.Del("Set-Cookie")
	for _, cookie := range updated {
		header.Add("Set-Cookie", cookie)
	}
}

func rewriteSetCookieDomainForTunnel(header http.Header, localHost, publicHost, localApex, publicApex string) {
	cookies := header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	updated := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		updated = append(updated, rewriteCookieDomainForTunnel(cookie, localHost, publicHost, localApex, publicApex))
	}
	header.Del("Set-Cookie")
	for _, cookie := range updated {
		header.Add("Set-Cookie", cookie)
	}
}

func rewriteSetCookieDomainForTunnelWithPeerSubdomains(header http.Header, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) {
	cookies := header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	updated := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		updated = append(updated, rewriteCookieDomainForTunnelWithPeerSubdomains(cookie, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains))
	}
	header.Del("Set-Cookie")
	for _, cookie := range updated {
		header.Add("Set-Cookie", cookie)
	}
}

func rewriteRequestCookieDomain(header http.Header, publicApex, localApex string) {
	cookies := header.Values("Cookie")
	if len(cookies) == 0 {
		return
	}
	updated := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		updated = append(updated, rewriteRequestCookieDomainValue(cookie, publicApex, localApex))
	}
	header.Del("Cookie")
	for _, cookie := range updated {
		header.Add("Cookie", cookie)
	}
}

func rewriteRequestCookieDomainForTunnel(header http.Header, publicHost, localHost, publicApex, localApex string) {
	cookies := header.Values("Cookie")
	if len(cookies) == 0 {
		return
	}
	updated := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		updated = append(updated, rewriteRequestCookieDomainValueForTunnel(cookie, publicHost, localHost, publicApex, localApex))
	}
	header.Del("Cookie")
	for _, cookie := range updated {
		header.Add("Cookie", cookie)
	}
}

func rewriteRequestOrigin(header http.Header, publicApex, localApex string) {
	origin := strings.TrimSpace(header.Get("Origin"))
	if origin == "" || strings.EqualFold(origin, "null") {
		return
	}
	parsed, err := url.Parse(origin)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return
	}
	rewrittenHost, ok := replaceHostApex(parsed.Host, publicApex, localApex)
	if !ok {
		return
	}
	parsed.Host = rewrittenHost
	header.Set("Origin", parsed.String())
}

func rewriteRequestOriginForTunnel(header http.Header, publicHost, localHost, publicApex, localApex string) {
	origin := strings.TrimSpace(header.Get("Origin"))
	if origin == "" || strings.EqualFold(origin, "null") {
		return
	}
	parsed, err := url.Parse(origin)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return
	}
	rewrittenHost, ok := replaceHostPublicToLocal(parsed.Host, publicHost, localHost, publicApex, localApex)
	if !ok {
		return
	}
	parsed.Host = rewrittenHost
	header.Set("Origin", parsed.String())
}

func rewriteRequestCookieDomainValue(value, publicApex, localApex string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "$domain=") {
			continue
		}
		raw := strings.TrimSpace(trimmed[len("$Domain="):])
		quoted := strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") && len(raw) >= 2
		domain := raw
		if quoted {
			domain = raw[1 : len(raw)-1]
		}
		rewritten, ok := replaceDomainApex(domain, publicApex, localApex)
		if !ok {
			continue
		}
		if quoted {
			rewritten = `"` + rewritten + `"`
		}
		parts[i] = " $Domain=" + rewritten
	}
	return strings.Join(parts, ";")
}

func rewriteRequestCookieDomainValueForTunnel(value, publicHost, localHost, publicApex, localApex string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "$domain=") {
			continue
		}
		raw := strings.TrimSpace(trimmed[len("$Domain="):])
		quoted := strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") && len(raw) >= 2
		domain := raw
		if quoted {
			domain = raw[1 : len(raw)-1]
		}
		rewritten, ok := replaceDomainPublicToLocal(domain, publicHost, localHost, publicApex, localApex)
		if !ok {
			continue
		}
		if quoted {
			rewritten = `"` + rewritten + `"`
		}
		parts[i] = " $Domain=" + rewritten
	}
	return strings.Join(parts, ";")
}

func rewriteCookieDomain(value, localApex, publicApex string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(trimmed), "domain=") {
			domainValue := strings.TrimSpace(trimmed[len("domain="):])
			if rewritten, ok := replaceDomainApex(domainValue, localApex, publicApex); ok {
				parts[i] = " Domain=" + rewritten
			}
			return strings.Join(parts, ";")
		}
	}
	return value
}

func rewriteCookieDomainForTunnel(value, localHost, publicHost, localApex, publicApex string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(trimmed), "domain=") {
			continue
		}
		domainValue := strings.TrimSpace(trimmed[len("domain="):])
		if rewritten, ok := replaceDomainLocalToPublic(domainValue, localHost, publicHost, localApex, publicApex); ok {
			parts[i] = " Domain=" + rewritten
		}
		return strings.Join(parts, ";")
	}
	return value
}

func rewriteCookieDomainForTunnelWithPeerSubdomains(value, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(trimmed), "domain=") {
			continue
		}
		domainValue := strings.TrimSpace(trimmed[len("domain="):])
		if rewritten, ok := replaceDomainLocalToPublicWithPeerSubdomains(domainValue, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains); ok {
			parts[i] = " Domain=" + rewritten
		}
		return strings.Join(parts, ";")
	}
	return value
}

func replaceHostApex(hostPort, localApex, publicApex string) (string, bool) {
	host := hostPort
	port := ""
	if strings.Contains(hostPort, ":") {
		if h, p, err := net.SplitHostPort(hostPort); err == nil {
			host = h
			port = p
		}
	}
	rewritten, ok := replaceDomainApex(host, localApex, publicApex)
	if !ok {
		return hostPort, false
	}
	if port != "" {
		return net.JoinHostPort(rewritten, port), true
	}
	return rewritten, true
}

func replaceHostLocalToPublic(hostPort, localHost, publicHost, localApex, publicApex string) (string, bool) {
	rewritten, ok := replaceHostApex(hostPort, localApex, publicApex)
	if !ok {
		return hostPort, false
	}
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK {
		return rewritten, true
	}
	if withLabel, ok := replaceHostGatewayLabelForApex(rewritten, publicApex, localLabel, publicLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func replaceHostLocalToPublicWithPeerSubdomains(hostPort, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) (string, bool) {
	rewritten, ok := replaceHostApex(hostPort, localApex, publicApex)
	if !ok {
		return hostPort, false
	}
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK {
		return rewritten, true
	}
	if withLabel, ok := replaceHostGatewayLabelForApex(rewritten, publicApex, localLabel, publicLabel); ok {
		return withLabel, true
	}
	if !shouldRewritePeerHost(hostPort, localApex, localLabel, rewritePeerSubdomains) {
		return rewritten, true
	}
	if withLabel, ok := replaceHostGatewayLabelForApexAnySource(rewritten, publicApex, publicLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func replaceHostLocalPeerToPublic(hostPort, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) (string, bool) {
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK || !shouldRewritePeerHost(hostPort, localApex, localLabel, rewritePeerSubdomains) {
		return hostPort, false
	}
	rewritten, ok := replaceHostApex(hostPort, localApex, publicApex)
	if !ok {
		return hostPort, false
	}
	if withLabel, ok := replaceHostGatewayLabelForApexAnySource(rewritten, publicApex, publicLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func replaceHostPublicToLocal(hostPort, publicHost, localHost, publicApex, localApex string) (string, bool) {
	rewritten, ok := replaceHostApex(hostPort, publicApex, localApex)
	if !ok {
		return hostPort, false
	}
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK {
		return rewritten, true
	}
	if withLabel, ok := replaceHostGatewayLabelForApex(rewritten, localApex, publicLabel, localLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func replaceDomainLocalToPublic(domain, localHost, publicHost, localApex, publicApex string) (string, bool) {
	rewritten, ok := replaceDomainApex(domain, localApex, publicApex)
	if !ok {
		return domain, false
	}
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK {
		return rewritten, true
	}
	if withLabel, ok := replaceDomainGatewayLabelForApex(rewritten, publicApex, localLabel, publicLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func replaceDomainLocalToPublicWithPeerSubdomains(domain, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) (string, bool) {
	rewritten, ok := replaceDomainApex(domain, localApex, publicApex)
	if !ok {
		return domain, false
	}
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK {
		return rewritten, true
	}
	if withLabel, ok := replaceDomainGatewayLabelForApex(rewritten, publicApex, localLabel, publicLabel); ok {
		return withLabel, true
	}
	if !shouldRewritePeerDomain(domain, localApex, localLabel, rewritePeerSubdomains) {
		return rewritten, true
	}
	if withLabel, ok := replaceDomainGatewayLabelForApexAnySource(rewritten, publicApex, publicLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func replaceDomainPublicToLocal(domain, publicHost, localHost, publicApex, localApex string) (string, bool) {
	rewritten, ok := replaceDomainApex(domain, publicApex, localApex)
	if !ok {
		return domain, false
	}
	localLabel, publicLabel, labelOK := gatewayLabelPair(localHost, publicHost, localApex, publicApex)
	if !labelOK {
		return rewritten, true
	}
	if withLabel, ok := replaceDomainGatewayLabelForApex(rewritten, localApex, publicLabel, localLabel); ok {
		return withLabel, true
	}
	return rewritten, true
}

func gatewayLabelPair(localHost, publicHost, localApex, publicApex string) (localLabel, publicLabel string, ok bool) {
	localLabels := strings.Split(normalizeProxyHost(localHost), ".")
	publicLabels := strings.Split(normalizeProxyHost(publicHost), ".")
	localApexLabels := strings.Split(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(localApex)), "."), ".")
	publicApexLabels := strings.Split(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(publicApex)), "."), ".")
	if len(localLabels) <= len(localApexLabels) || len(publicLabels) <= len(publicApexLabels) {
		return "", "", false
	}
	localIdx := len(localLabels) - len(localApexLabels) - 1
	publicIdx := len(publicLabels) - len(publicApexLabels) - 1
	if localIdx < 0 || publicIdx < 0 {
		return "", "", false
	}
	return localLabels[localIdx], publicLabels[publicIdx], true
}

func replaceHostGatewayLabelForApex(hostPort, apex, fromLabel, toLabel string) (string, bool) {
	host := hostPort
	port := ""
	if strings.Contains(hostPort, ":") {
		if h, p, err := net.SplitHostPort(hostPort); err == nil {
			host = h
			port = p
		}
	}
	rewritten, ok := replaceDomainGatewayLabelForApex(host, apex, fromLabel, toLabel)
	if !ok {
		return hostPort, false
	}
	if port != "" {
		return net.JoinHostPort(rewritten, port), true
	}
	return rewritten, true
}

func replaceHostGatewayLabelForApexAnySource(hostPort, apex, toLabel string) (string, bool) {
	host := hostPort
	port := ""
	if strings.Contains(hostPort, ":") {
		if h, p, err := net.SplitHostPort(hostPort); err == nil {
			host = h
			port = p
		}
	}
	rewritten, ok := replaceDomainGatewayLabelForApexAnySource(host, apex, toLabel)
	if !ok {
		return hostPort, false
	}
	if port != "" {
		return net.JoinHostPort(rewritten, port), true
	}
	return rewritten, true
}

func replaceDomainGatewayLabelForApex(domain, apex, fromLabel, toLabel string) (string, bool) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return domain, false
	}
	prefixDot := strings.HasPrefix(domain, ".")
	base := strings.TrimPrefix(strings.ToLower(domain), ".")
	apexBase := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(apex)), ".")
	if base == "" || apexBase == "" {
		return domain, false
	}
	labels := strings.Split(base, ".")
	apexLabels := strings.Split(apexBase, ".")
	if len(labels) <= len(apexLabels) {
		return domain, false
	}
	for i := 1; i <= len(apexLabels); i++ {
		if labels[len(labels)-i] != apexLabels[len(apexLabels)-i] {
			return domain, false
		}
	}
	labelIdx := len(labels) - len(apexLabels) - 1
	if labelIdx < 0 || !strings.EqualFold(labels[labelIdx], strings.ToLower(strings.TrimSpace(fromLabel))) {
		return domain, false
	}
	labels[labelIdx] = strings.ToLower(strings.TrimSpace(toLabel))
	out := strings.Join(labels, ".")
	if prefixDot {
		return "." + out, true
	}
	return out, true
}

func replaceDomainGatewayLabelForApexAnySource(domain, apex, toLabel string) (string, bool) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return domain, false
	}
	prefixDot := strings.HasPrefix(domain, ".")
	base := strings.TrimPrefix(strings.ToLower(domain), ".")
	apexBase := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(apex)), ".")
	if base == "" || apexBase == "" {
		return domain, false
	}
	labels := strings.Split(base, ".")
	apexLabels := strings.Split(apexBase, ".")
	if len(labels) <= len(apexLabels) {
		return domain, false
	}
	for i := 1; i <= len(apexLabels); i++ {
		if labels[len(labels)-i] != apexLabels[len(apexLabels)-i] {
			return domain, false
		}
	}
	labelIdx := len(labels) - len(apexLabels) - 1
	if labelIdx < 0 {
		return domain, false
	}
	labels[labelIdx] = strings.ToLower(strings.TrimSpace(toLabel))
	out := strings.Join(labels, ".")
	if prefixDot {
		return "." + out, true
	}
	return out, true
}

func shouldRewritePeerHost(hostPort, localApex, localLabel string, rewritePeerSubdomains []string) bool {
	host := normalizeProxyHost(hostPort)
	domain, ok := parseDomainWithApex(host, localApex)
	if !ok {
		return false
	}
	return shouldRewritePeerLabels(domain.prefixLabels, domain.routeLabel, localLabel, rewritePeerSubdomains)
}

func shouldRewritePeerDomain(domain, localApex, localLabel string, rewritePeerSubdomains []string) bool {
	parsed, ok := parseDomainWithApex(domain, localApex)
	if !ok {
		return false
	}
	return shouldRewritePeerLabels(parsed.prefixLabels, parsed.routeLabel, localLabel, rewritePeerSubdomains)
}

func shouldRewritePeerLabels(prefixLabels []string, routeLabel, localLabel string, rewritePeerSubdomains []string) bool {
	if strings.EqualFold(strings.TrimSpace(routeLabel), strings.TrimSpace(localLabel)) {
		return false
	}
	policy := parsePeerSubdomainPolicy(rewritePeerSubdomains)
	if !policy.enabled {
		return false
	}
	if policy.all {
		return true
	}
	if len(prefixLabels) == 0 {
		return false
	}
	_, ok := policy.subdomains[prefixLabels[0]]
	return ok
}

type peerSubdomainPolicy struct {
	enabled    bool
	all        bool
	subdomains map[string]struct{}
}

func parsePeerSubdomainPolicy(values []string) peerSubdomainPolicy {
	policy := peerSubdomainPolicy{subdomains: map[string]struct{}{}}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		policy.enabled = true
		if normalized == "*" {
			policy.all = true
			continue
		}
		policy.subdomains[normalized] = struct{}{}
	}
	return policy
}

type parsedDomainApex struct {
	prefixLabels []string
	routeLabel   string
}

func parseDomainWithApex(domain, apex string) (parsedDomainApex, bool) {
	base := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	apexBase := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(apex)), ".")
	if base == "" || apexBase == "" {
		return parsedDomainApex{}, false
	}
	labels := strings.Split(base, ".")
	apexLabels := strings.Split(apexBase, ".")
	if len(labels) <= len(apexLabels) {
		return parsedDomainApex{}, false
	}
	for i := 1; i <= len(apexLabels); i++ {
		if labels[len(labels)-i] != apexLabels[len(apexLabels)-i] {
			return parsedDomainApex{}, false
		}
	}
	labelIdx := len(labels) - len(apexLabels) - 1
	if labelIdx < 0 {
		return parsedDomainApex{}, false
	}
	return parsedDomainApex{
		prefixLabels: labels[:labelIdx],
		routeLabel:   labels[labelIdx],
	}, true
}

func replaceDomainApex(domain, localApex, publicApex string) (string, bool) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return domain, false
	}
	prefixDot := strings.HasPrefix(domain, ".")
	left := strings.TrimPrefix(strings.ToLower(domain), ".")
	from := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(localApex)), ".")
	to := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(publicApex)), ".")
	if from == "" || to == "" {
		return domain, false
	}
	switch {
	case left == from:
		left = to
	case strings.HasSuffix(left, "."+from):
		left = strings.TrimSuffix(left, "."+from) + "." + to
	default:
		return domain, false
	}
	if prefixDot {
		return "." + left, true
	}
	return left, true
}

func derivePublicApex(localHost, publicHost, localApex string) (string, bool) {
	localLabels := strings.Split(normalizeProxyHost(localHost), ".")
	publicLabels := strings.Split(normalizeProxyHost(publicHost), ".")
	localApexLabels := strings.Split(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(localApex)), "."), ".")
	if len(localLabels) == 0 || len(publicLabels) == 0 || len(localApexLabels) == 0 {
		return "", false
	}
	if len(localLabels) <= len(localApexLabels) {
		return "", false
	}
	prefixCount := len(localLabels) - len(localApexLabels)
	if prefixCount <= 0 || len(publicLabels) <= prefixCount {
		return "", false
	}
	return strings.Join(publicLabels[prefixCount:], "."), true
}

func rewriteResponseBody(resp *http.Response, localHost, publicHost string) error {
	localHost = strings.TrimSpace(strings.ToLower(localHost))
	publicHost = strings.TrimSpace(strings.ToLower(publicHost))
	if resp == nil || resp.Body == nil || localHost == "" || publicHost == "" || localHost == publicHost {
		return nil
	}
	if !shouldRewriteBody(resp) {
		return nil
	}
	encoding, ok := supportedEncoding(resp.Header.Get("Content-Encoding"))
	if !ok {
		return nil
	}

	rawBody, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	decoded, err := decodeBody(rawBody, encoding)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		return nil
	}
	if len(decoded) > maxRewriteBodyBytes {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		resp.ContentLength = int64(len(rawBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rawBody)))
		return nil
	}

	rewritten := rewriteBodyContent(decoded, localHost, publicHost)
	if bytes.Equal(decoded, rewritten) {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		resp.ContentLength = int64(len(rawBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rawBody)))
		return nil
	}

	encoded, err := encodeBody(rewritten, encoding)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		resp.ContentLength = int64(len(rawBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rawBody)))
		return nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(encoded))
	resp.ContentLength = int64(len(encoded))
	resp.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
	stripStrongETag(resp.Header)
	return nil
}

func rewriteResponseBodyForTunnel(resp *http.Response, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) error {
	localHost = strings.TrimSpace(strings.ToLower(localHost))
	publicHost = strings.TrimSpace(strings.ToLower(publicHost))
	if resp == nil || resp.Body == nil || localHost == "" || publicHost == "" || localHost == publicHost {
		return nil
	}
	if !shouldRewriteBody(resp) {
		return nil
	}
	encoding, ok := supportedEncoding(resp.Header.Get("Content-Encoding"))
	if !ok {
		return nil
	}

	rawBody, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	decoded, err := decodeBody(rawBody, encoding)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		return nil
	}
	if len(decoded) > maxRewriteBodyBytes {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		resp.ContentLength = int64(len(rawBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rawBody)))
		return nil
	}

	rewritten := rewriteBodyContentForTunnel(decoded, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains)
	if bytes.Equal(decoded, rewritten) {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		resp.ContentLength = int64(len(rawBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rawBody)))
		return nil
	}

	encoded, err := encodeBody(rewritten, encoding)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
		resp.ContentLength = int64(len(rawBody))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rawBody)))
		return nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(encoded))
	resp.ContentLength = int64(len(encoded))
	resp.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
	stripStrongETag(resp.Header)
	return nil
}

func shouldRewriteBody(resp *http.Response) bool {
	contentLength := resp.ContentLength
	if contentLength <= 0 || contentLength > maxRewriteBodyBytes {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if contentType == "" {
		return false
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "no-transform") {
		return false
	}
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	if strings.HasSuffix(contentType, "+json") || strings.HasSuffix(contentType, "+xml") {
		return true
	}
	switch contentType {
	case "application/json", "application/javascript", "application/x-javascript", "application/xml", "application/xhtml+xml", "image/svg+xml":
		return true
	default:
		return false
	}
}

func supportedEncoding(value string) (string, bool) {
	encoding := strings.ToLower(strings.TrimSpace(value))
	switch encoding {
	case "", "identity":
		return "", true
	case "gzip", "deflate":
		return encoding, true
	default:
		return "", false
	}
}

func decodeBody(raw []byte, encoding string) ([]byte, error) {
	switch encoding {
	case "":
		return raw, nil
	case "gzip":
		r, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	case "deflate":
		r, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	default:
		return nil, io.ErrUnexpectedEOF
	}
}

func encodeBody(decoded []byte, encoding string) ([]byte, error) {
	switch encoding {
	case "":
		return decoded, nil
	case "gzip":
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(decoded); err != nil {
			_ = w.Close()
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "deflate":
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(decoded); err != nil {
			_ = w.Close()
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, io.ErrUnexpectedEOF
	}
}

func rewriteBodyContent(decoded []byte, localHost, publicHost string) []byte {
	localHTTPS := "https://" + localHost
	localHTTP := "http://" + localHost
	localSchemeLess := "//" + localHost
	escapedHTTPS := "https:\\/\\/" + localHost
	escapedHTTP := "http:\\/\\/" + localHost
	escapedSchemeLess := "\\/\\/" + localHost
	publicHTTPS := "https://" + publicHost
	publicSchemeLess := "//" + publicHost
	escapedPublicHTTPS := "https:\\/\\/" + publicHost
	escapedPublicSchemeLess := "\\/\\/" + publicHost

	replacer := strings.NewReplacer(
		localHTTPS, publicHTTPS,
		localHTTP, publicHTTPS,
		localSchemeLess, publicSchemeLess,
		escapedHTTPS, escapedPublicHTTPS,
		escapedHTTP, escapedPublicHTTPS,
		escapedSchemeLess, escapedPublicSchemeLess,
		localHost, publicHost,
	)
	out := replacer.Replace(string(decoded))
	return []byte(out)
}

var (
	unescapedURLHostPattern = regexp.MustCompile(`((?:https?://)|(?://))([a-z0-9.-]+(?::[0-9]+)?)`)
	escapedURLHostPattern   = regexp.MustCompile(`((?:https?:\\\/\\\/)|(?:\\\/\\\/))([a-z0-9.-]+(?::[0-9]+)?)`)
)

func rewriteBodyContentForTunnel(decoded []byte, localHost, publicHost, localApex, publicApex string, rewritePeerSubdomains []string) []byte {
	out := string(rewriteBodyContent(decoded, localHost, publicHost))
	replaceHost := func(hostPort string) string {
		rewritten, ok := replaceHostLocalPeerToPublic(hostPort, localHost, publicHost, localApex, publicApex, rewritePeerSubdomains)
		if !ok {
			return hostPort
		}
		return rewritten
	}

	out = unescapedURLHostPattern.ReplaceAllStringFunc(out, func(match string) string {
		parts := unescapedURLHostPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return parts[1] + replaceHost(parts[2])
	})

	out = escapedURLHostPattern.ReplaceAllStringFunc(out, func(match string) string {
		parts := escapedURLHostPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return parts[1] + replaceHost(parts[2])
	})

	return []byte(out)
}

func stripStrongETag(header http.Header) {
	etag := strings.TrimSpace(header.Get("ETag"))
	if etag == "" {
		return
	}
	if strings.HasPrefix(etag, "W/") {
		return
	}
	header.Del("ETag")
}

func normalizeProxyHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, ":")
	}
	return strings.TrimSuffix(host, ".")
}
