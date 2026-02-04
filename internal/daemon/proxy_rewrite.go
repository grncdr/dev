package daemon

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxRewriteBodyBytes = 5 << 20 // 5 MiB

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
