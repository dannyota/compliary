// Package fetch downloads framework documents from official publisher
// sources: direct downloads for public-domain material and operator-consented
// form flows for gated free material. Sources behind sign-in, purchase, or
// membership are never automated.
package fetch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"time"
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// NewClient returns an HTTP client with a cookie jar, suitable for the
// session-and-form flows publishers use.
func NewClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Minute}, nil
}

// retryableStatus returns true for HTTP status codes that warrant a retry.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

// retryableError returns true for transient connection-level errors.
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// get performs an HTTP GET with up to 3 attempts on transient failures
// (connection errors and HTTP 429/502/503/504). Non-retryable 4xx/5xx
// errors fail immediately.
func get(c *http.Client, url string) (*http.Response, error) {
	const maxAttempts = 3
	backoff := []time.Duration{1 * time.Second, 4 * time.Second}

	var lastErr error
	for attempt := range maxAttempts {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request %s: %w", url, err)
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.Do(req)
		if err != nil {
			if !retryableError(err) {
				return nil, fmt.Errorf("get %s: %w", url, err)
			}
			lastErr = fmt.Errorf("get %s: %w", url, err)
			if attempt < maxAttempts-1 {
				time.Sleep(backoff[attempt])
			}
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		resp.Body.Close()
		if !retryableStatus(resp.StatusCode) {
			return nil, fmt.Errorf("get %s: status %s", url, resp.Status)
		}
		lastErr = fmt.Errorf("get %s: status %s", url, resp.Status)
		if attempt < maxAttempts-1 {
			time.Sleep(backoff[attempt])
		}
	}
	return nil, lastErr
}

func getBody(c *http.Client, url string) ([]byte, error) {
	resp, err := get(c, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return body, nil
}

// saveResponse writes resp.Body to dest atomically: temp file in the same
// directory, magic-byte validation, then atomic rename. The caller is
// responsible for closing resp.Body (typically via defer).
func saveResponse(resp *http.Response, dest string, wantMagic []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	head := make([]byte, 0, len(wantMagic))
	if len(wantMagic) > 0 {
		buf := make([]byte, len(wantMagic))
		n, err := io.ReadAtLeast(resp.Body, buf, len(wantMagic))
		if err != nil {
			tmp.Close()
			return fmt.Errorf("read head of %s: %w", resp.Request.URL, err)
		}
		head = buf[:n]
		if !bytes.Equal(head, wantMagic) {
			tmp.Close()
			return fmt.Errorf("%s: unexpected content (magic %q), not saving", resp.Request.URL, head)
		}
	}
	if _, err := tmp.Write(head); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dest, err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("rename into %s: %w", dest, err)
	}
	return nil
}

// downloadFile GETs url into dest atomically. A non-empty wantMagic is
// checked against the file head so an HTML error page is never saved as a
// document.
func downloadFile(c *http.Client, url, dest string, wantMagic []byte) error {
	resp, err := get(c, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return saveResponse(resp, dest, wantMagic)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
