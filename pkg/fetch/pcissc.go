package fetch

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dannyota/compliary/pkg/operator"
)

// PCI SSC serves standards behind a click-through license agreement
// (docs-prv wrapper page). Accepting it POSTs the licensee identity to
// cookie.php, which returns CloudFront signed cookies that unlock the
// document. The operator is the accepting party; the identity comes from
// their env file.
const (
	pciSessionURL = "https://docs-app.pcisecuritystandards.org/session.php"
	pciCookieURL  = "https://docs-app.pcisecuritystandards.org/cookie.php"
	pciLibraryURL = "https://docs-pub.pcisecuritystandards.org/doc_library.json"
	pciDocHost    = "https://docs-prv.pcisecuritystandards.org"
)

// pciDocs are the documents to fetch, keyed by their doc_library.json path.
var pciDocs = []struct {
	path string
	dest string
}{
	{"/PCI%20DSS/Standard/PCI-DSS-v4_0_1.pdf", "pcissc/PCI-DSS-v4_0_1.pdf"},
}

var csrfRe = regexp.MustCompile(`csrf_token="([0-9a-f]+)"`)

// oneOrMany accepts either a JSON array or a single object, mirroring the
// doc_library.json shape the PCI SSC wrapper page handles in JS.
type oneOrMany[T any] []T

func (o *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		var s []T
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*o = s
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = []T{v}
	return nil
}

type pciFile struct {
	Path string `json:"path"`
}

type pciVersion struct {
	Title string `json:"title"`
	Files struct {
		File oneOrMany[pciFile] `json:"file"`
	} `json:"files"`
}

type pciDoc struct {
	Name        string `json:"name"`
	Agreement   string `json:"agreement"`
	Protected   string `json:"protected"`
	LastUpdated string `json:"last_updated"`
	Versions    struct {
		Version oneOrMany[pciVersion] `json:"version"`
	} `json:"versions"`
}

type pciCategory struct {
	Reference string            `json:"reference"`
	Document  oneOrMany[pciDoc] `json:"document"`
}

type pciLibrary []pciCategory

// PCISSC accepts the PCI SSC license agreement as the operator and downloads
// the PCI DSS standard.
func PCISSC(c *http.Client, dataDir string, id *operator.Identity, report func(string)) error {
	pending := false
	for _, d := range pciDocs {
		if !exists(filepath.Join(dataDir, d.dest)) {
			pending = true
		}
	}
	if !pending {
		report("skip (exists): all PCI SSC documents")
		return nil
	}

	body, err := getBody(c, pciSessionURL)
	if err != nil {
		return fmt.Errorf("pcissc session: %w", err)
	}
	m := csrfRe.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("pcissc session: csrf token not found")
	}
	csrf := string(m[1])

	libRaw, err := getBody(c, pciLibraryURL)
	if err != nil {
		return fmt.Errorf("pcissc library: %w", err)
	}
	var lib pciLibrary
	if err := json.Unmarshal(libRaw, &lib); err != nil {
		return fmt.Errorf("pcissc library parse: %w", err)
	}

	for _, d := range pciDocs {
		dest := filepath.Join(dataDir, d.dest)
		if exists(dest) {
			report("skip (exists): " + d.dest)
			continue
		}
		agreement, err := agreementKey(lib, d.path)
		if err != nil {
			return fmt.Errorf("pcissc: %w", err)
		}
		if err := acceptAgreement(c, csrf, agreement, pciDocHost+d.path, id); err != nil {
			return fmt.Errorf("pcissc accept: %w", err)
		}
		if err := downloadFile(c, pciDocHost+d.path, dest, pdfMagic); err != nil {
			return fmt.Errorf("pcissc download: %w", err)
		}
		report("downloaded (license accepted): " + d.dest)
	}
	return nil
}

// agreementKey rebuilds the wrapper page's agreements_cookie_key for the
// document owning path: "<agreement>:<category>:<latest title>:<updated>",
// with the "pcidss" agreement aliased to "ip".
func agreementKey(lib pciLibrary, path string) (string, error) {
	for _, cat := range lib {
		for _, doc := range cat.Document {
			for _, ver := range doc.Versions.Version {
				for _, f := range ver.Files.File {
					if f.Path != path {
						continue
					}
					if doc.Protected != "yes" {
						return "", nil
					}
					agreement := doc.Agreement
					if agreement == "pcidss" {
						agreement = "ip"
					}
					latest := doc.Versions.Version[0].Title
					return agreement + ":" + cat.Reference + ":" + latest + ":" + doc.LastUpdated, nil
				}
			}
		}
	}
	return "", fmt.Errorf("document %s not in doc_library.json", path)
}

func acceptAgreement(c *http.Client, csrf, agreement, docURL string, id *operator.Identity) error {
	guid := make([]byte, 16)
	if _, err := rand.Read(guid); err != nil {
		return fmt.Errorf("guid: %w", err)
	}
	payload := map[string]any{
		"access_guid":   fmt.Sprintf("%x", guid),
		"agreement":     agreement,
		"f":             base64.StdEncoding.EncodeToString([]byte(docURL)),
		"company":       id.Company,
		"url":           id.Website,
		"contact_name":  id.FullName(),
		"contact_title": id.Title,
		"email":         id.Email,
		"phone":         id.Phone,
		"address1":      id.Address1,
		"address2":      id.Address2,
		"city":          id.City,
		"zip":           id.Zip,
		"region":        id.State,
		"country":       id.Country,
		"remember_me":   true,
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal agreement: %w", err)
	}
	form := url.Values{"csrf": {csrf}, "json": {string(blob)}}
	req, err := http.NewRequest(http.MethodPost, pciCookieURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build accept request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("post agreement: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post agreement: status %s", resp.Status)
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var cookies map[string]any
	if err := dec.Decode(&cookies); err != nil {
		return fmt.Errorf("agreement response: %w", err)
	}
	if len(cookies) == 0 {
		return fmt.Errorf("agreement response: no access cookies returned")
	}
	docHost, err := url.Parse(pciDocHost)
	if err != nil {
		return fmt.Errorf("parse doc host: %w", err)
	}
	var set []*http.Cookie
	for k, v := range cookies {
		set = append(set, &http.Cookie{Name: k, Value: fmt.Sprintf("%v", v), Path: "/"})
	}
	c.Jar.SetCookies(docHost, set)
	return nil
}
