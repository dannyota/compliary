// Package fetch downloads framework documents from official publisher
// sources: direct downloads for public-domain material and operator-consented
// form flows for gated free material. Sources behind sign-in, purchase, or
// membership are never automated.
package fetch

import (
	"bytes"
	"fmt"
	"io"
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

func get(c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", url, err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("get %s: status %s", url, resp.Status)
	}
	return resp, nil
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

// downloadFile GETs url into dest atomically. A non-empty wantMagic is
// checked against the file head so an HTML error page is never saved as a
// document.
func downloadFile(c *http.Client, url, dest string, wantMagic []byte) error {
	resp, err := get(c, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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
			return fmt.Errorf("read head of %s: %w", url, err)
		}
		head = buf[:n]
		if !bytes.Equal(head, wantMagic) {
			tmp.Close()
			return fmt.Errorf("%s: unexpected content (magic %q), not saving", url, head)
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
