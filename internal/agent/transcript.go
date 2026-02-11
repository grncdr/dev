package agent

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const GatewayTranscriptExchangeDelimiterPrefix = "===== END EXCHANGE"

func WriteGatewayHTTPTranscript(worktreePath, transcriptPath string, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte) error {
	return writeGatewayHTTPTranscript(worktreePath, transcriptPath, req, reqBody, resp, respBody)
}

func writeGatewayHTTPTranscript(worktreePath, transcriptPath string, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte) error {
	path, err := resolveGatewayTranscriptPath(worktreePath, transcriptPath)
	if err != nil {
		return err
	}
	entry := formatGatewayHTTPTranscript(req, reqBody, resp, respBody)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}

func resolveGatewayTranscriptPath(worktreePath, transcriptPath string) (string, error) {
	path := strings.TrimSpace(transcriptPath)
	if path == "" {
		return "", fmt.Errorf("missing transcript path")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	root := strings.TrimSpace(worktreePath)
	if root == "" {
		return "", fmt.Errorf("missing process worktree path")
	}
	return filepath.Join(root, filepath.Clean(path)), nil
}

func formatGatewayHTTPTranscript(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte) string {
	var b strings.Builder
	if req != nil {
		proto := strings.TrimSpace(req.Proto)
		if proto == "" {
			proto = "HTTP/1.1"
		}
		target := req.URL.RequestURI()
		if target == "" {
			target = "/"
		}
		fmt.Fprintf(&b, "%s %s %s\n", req.Method, target, proto)
		writeRequestHeaders(&b, req.Host, req.Header)
		b.WriteString("\n")
		b.WriteString(formatTranscriptBody(req.Header.Get("Content-Type"), reqBody))
		b.WriteString("\n\n\n")
	}
	if resp != nil {
		proto := strings.TrimSpace(resp.Proto)
		if proto == "" {
			proto = "HTTP/1.1"
		}
		fmt.Fprintf(&b, "%s %s\n", proto, resp.Status)
		writeHeaderMap(&b, resp.Header)
		b.WriteString("\n")
		b.WriteString(formatTranscriptBody(resp.Header.Get("Content-Type"), respBody))
		b.WriteString("\n\n")
	}
	b.WriteString(formatGatewayTranscriptExchangeDelimiter(time.Now().UTC()))
	b.WriteString("\n\n")
	return b.String()
}

func formatGatewayTranscriptExchangeDelimiter(at time.Time) string {
	return fmt.Sprintf("%s %s =====", GatewayTranscriptExchangeDelimiterPrefix, at.Format(time.RFC3339))
}

func writeRequestHeaders(b *strings.Builder, host string, header http.Header) {
	host = strings.TrimSpace(host)
	if host != "" {
		fmt.Fprintf(b, "Host: %s\n", host)
	}
	writeHeaderMap(b, header)
}

func writeHeaderMap(b *strings.Builder, header http.Header) {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		vals := header.Values(key)
		for _, val := range vals {
			fmt.Fprintf(b, "%s: %s\n", key, val)
		}
	}
}

func formatTranscriptBody(contentType string, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if isBinaryBody(contentType, body) {
		return fmt.Sprintf("[%d bytes binary data]", len(body))
	}
	return string(body)
}

func isBinaryBody(contentType string, body []byte) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.ToLower(strings.Split(contentType, ";")[0]))
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		if !utf8.Valid(body) || strings.ContainsRune(string(body), '\x00') {
			return true
		}
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return false
	}
	if strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") {
		return false
	}
	switch mediaType {
	case "application/json",
		"application/xml",
		"application/x-www-form-urlencoded",
		"application/javascript",
		"application/x-javascript",
		"application/graphql",
		"application/yaml",
		"application/x-yaml",
		"image/svg+xml":
		return false
	default:
		return true
	}
}
