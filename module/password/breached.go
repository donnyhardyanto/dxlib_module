package password

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 is the format breached-password lists are published in; it is not used to protect anything
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BreachedList answers whether a password is known from a breach.
type BreachedList interface {
	// Breached reports whether the password appears in the list. An error means the list could not be asked; the
	// password is then not set.
	Breached(ctx context.Context, password string) (bool, error)
}

// HashList is a BreachedList held in memory: the SHA-1 hashes of breached passwords, as the Pwned Passwords
// downloads publish them.
type HashList struct {
	hashes map[[sha1.Size]byte]struct{}
}

// NewHashList reads one upper- or lower-case hex SHA-1 per line, each optionally followed by ":count" (the Pwned
// Passwords download format). Blank lines and lines starting with # are skipped; any other line is an error naming
// its number.
func NewHashList(r io.Reader) (*HashList, error) {
	l := &HashList{hashes: map[[sha1.Size]byte]struct{}{}}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		h, _, _ := strings.Cut(line, ":")
		var k [sha1.Size]byte
		if len(h) != 2*sha1.Size {
			return nil, fmt.Errorf("password: breached list line %d: %q is not a 40-digit hex SHA-1", n, truncate(line, 60))
		}
		if _, err := hex.Decode(k[:], []byte(h)); err != nil {
			return nil, fmt.Errorf("password: breached list line %d: %q is not a 40-digit hex SHA-1: %w", n, truncate(line, 60), err)
		}
		l.hashes[k] = struct{}{}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("password: reading the breached list after line %d: %w", n, err)
	}
	return l, nil
}

// NewHashListOf returns a HashList of the given passwords (a service's own list of refused words, or a test's).
func NewHashListOf(passwords ...string) *HashList {
	l := &HashList{hashes: map[[sha1.Size]byte]struct{}{}}
	for _, p := range passwords {
		l.hashes[sha1.Sum([]byte(p))] = struct{}{} //nolint:gosec // list format, see the import
	}
	return l
}

// Len is the number of hashes in the list.
func (l *HashList) Len() int { return len(l.hashes) }

// Breached reports whether the password's SHA-1 is in the list.
func (l *HashList) Breached(_ context.Context, password string) (bool, error) {
	_, ok := l.hashes[sha1.Sum([]byte(password))] //nolint:gosec // list format, see the import
	return ok, nil
}

// RangeAPI asks a k-anonymity range service (the Pwned Passwords API, or a copy of it the service runs itself):
// only the first five hex digits of the password's SHA-1 leave the process, and the answer lists every suffix with
// that prefix. Responses are asked padded, so their size does not tell the prefix either.
type RangeAPI struct {
	base   *url.URL
	client *http.Client
	// MinCount is how often a password must have been seen to count as breached. Default 1.
	MinCount int
}

// PwnedPasswordsURL is the public Pwned Passwords range service.
const PwnedPasswordsURL = "https://api.pwnedpasswords.com"

// maxRangeResponseBytes bounds one range answer (a real one is about 30 to 40 KiB).
const maxRangeResponseBytes = 4 << 20

// NewRangeAPI returns a RangeAPI on baseURL (for example PwnedPasswordsURL; the request goes to baseURL +
// "/range/" + prefix). client may be nil: a client with a 10-second timeout is used.
func NewRangeAPI(baseURL string, client *http.Client) (*RangeAPI, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("password: range service URL %q must be an http or https URL with a host", truncate(baseURL, 200))
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &RangeAPI{base: u, client: client, MinCount: 1}, nil
}

// Breached asks the range service for the password's prefix and looks for its suffix in the answer.
func (r *RangeAPI) Breached(ctx context.Context, password string) (bool, error) {
	sum := sha1.Sum([]byte(password)) //nolint:gosec // list format, see the import
	full := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := full[:5], full[5:]
	endpoint := r.base.JoinPath("range", prefix).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("password: building the range request to %s: %w", endpoint, err)
	}
	req.Header.Set("Add-Padding", "true")
	req.Header.Set("User-Agent", "dxlib_module-password")
	resp, err := r.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("password: asking the breached-password range service %s: %w", r.base.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRangeResponseBytes+1))
	if err != nil {
		return false, fmt.Errorf("password: reading the answer of the range service %s (HTTP %d): %w", r.base.Host, resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("password: the range service %s answered HTTP %d: %s", r.base.Host, resp.StatusCode, truncate(strings.TrimSpace(string(body)), 200))
	}
	if len(body) > maxRangeResponseBytes {
		return false, fmt.Errorf("password: the range service %s answered more than %d bytes", r.base.Host, maxRangeResponseBytes)
	}
	minCount := r.MinCount
	if minCount < 1 {
		minCount = 1
	}
	for _, line := range strings.Split(string(body), "\n") {
		s, c, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(s, suffix) {
			continue
		}
		n, err := strconv.Atoi(c)
		if err != nil {
			return false, fmt.Errorf("password: the range service %s answered a malformed count %q", r.base.Host, truncate(c, 20))
		}
		// Padding lines carry the count 0.
		return n >= minCount, nil
	}
	return false, nil
}

var _ BreachedList = (*HashList)(nil)
var _ BreachedList = (*RangeAPI)(nil)
