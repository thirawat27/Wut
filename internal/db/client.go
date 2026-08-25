// Package db provides TLDR Pages API client for WUT
package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wut/internal/performance"
)

const (
	baseRawURL  = "https://raw.githubusercontent.com/tldr-pages/tldr/main"
	userAgent   = "wut/1.0.1 (command-line assistant; +https://github.com/thirawat27/wut)"
	maxBodySize = 64 * 1024 // 64KB limit for TLDR page content

	// pageRequestTimeout bounds a single page fetch end to end. It is sized for
	// one small markdown file and is deliberately not reused for bulk transfers.
	pageRequestTimeout = 5 * time.Second
	// archiveResponseHeaderTimeout bounds how long the server may take to start
	// responding to a bulk download. The body transfer itself is bounded by the
	// caller's context, not by a fixed whole-exchange deadline, because archive
	// size and link speed vary by orders of magnitude.
	archiveResponseHeaderTimeout = 30 * time.Second
	// Platforms available in tldr-pages
	PlatformCommon  = "common"
	PlatformLinux   = "linux"
	PlatformMacOS   = "osx"
	PlatformWindows = "windows"
	PlatformSunOS   = "sunos"
	PlatformAndroid = "android"
	PlatformFreeBSD = "freebsd"
	PlatformNetBSD  = "netbsd"
	PlatformOpenBSD = "openbsd"
)

var (
	errPageNotFound    = errors.New("page not found")
	errRemoteTemporary = errors.New("remote temporarily unavailable")
	defaultCommandRank = buildDefaultCommandRank(getDefaultCommands())
)

// Client represents the TLDR API client
type Client struct {
	httpClient    *http.Client
	baseURL       string
	language      string
	storage       *Storage
	offlineMode   atomic.Bool // atomic to prevent data races across goroutines
	autoDetect    bool
	cacheInMemory bool
	// memoryCache is bounded: a long-lived TUI session can walk thousands of
	// pages, and an unbounded map would hold every raw page body for the life of
	// the process. byName indexes the same entries so a lookup that only knows
	// the command name does not have to scan the whole cache.
	memoryCache map[string]*Page
	byName      map[string]*Page
	cacheMu     sync.RWMutex
	matcher     *performance.FastMatcher
	matchCache  *performance.LRUCache[string, []string]

	commandsMu        sync.RWMutex
	availableCommands []string

	onlineMu         sync.RWMutex
	onlineCached     bool
	onlineCheckedAt  time.Time
	onlineCheckTTL   time.Duration
	remoteFailureTTL time.Duration
}

// Page represents a TLDR page with parsed content
type Page struct {
	Name        string
	Platform    string
	Language    string
	Description string
	Examples    []Example
	RawContent  string
}

// variableRe is used to format TLDR command examples
var variableRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// Example represents a command example from TLDR
type Example struct {
	Description string
	Command     string
}

// ClientOption is a functional option for Client
type ClientOption func(*Client)

// WithStorage sets the local storage for offline support
func WithStorage(storage *Storage) ClientOption {
	return func(c *Client) {
		c.storage = storage
	}
}

// WithOfflineMode enables offline-only mode
func WithOfflineMode(offline bool) ClientOption {
	return func(c *Client) {
		c.offlineMode.Store(offline)
	}
}

// WithAutoDetect enables auto-detection of online/offline mode
func WithAutoDetect(auto bool) ClientOption {
	return func(c *Client) {
		c.autoDetect = auto
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithLanguage sets the preferred language
func WithLanguage(lang string) ClientOption {
	return func(c *Client) {
		c.language = lang
	}
}

// NewClient creates a new TLDR API client
func NewClient(opts ...ClientOption) *Client {
	lang := "en"

	c := &Client{
		httpClient: &http.Client{
			// Whole-exchange budget for a single ~4KB page. Callers that
			// transfer more than one page (see NewArchiveHTTPClient) must not
			// reuse this client.
			Timeout: pageRequestTimeout,
		},
		baseURL:          baseRawURL,
		language:         lang,
		autoDetect:       true,
		cacheInMemory:    true,
		memoryCache:      make(map[string]*Page),
		byName:           make(map[string]*Page),
		matcher:          performance.NewFastMatcher(false, 0.2, 3),
		matchCache:       performance.NewLRUCache[string, []string](256, 16),
		onlineCheckTTL:   15 * time.Second,
		remoteFailureTTL: 5 * time.Second,
	}
	c.offlineMode.Store(false)

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// maxMemoryCachedPages bounds the in-process page cache. Each entry holds the
// page's raw markdown, so an unbounded map grows with every page a session
// visits and is never released.
const maxMemoryCachedPages = 512

// cacheLookup returns a cached page by its full "lang/platform/name" key.
func (c *Client) cacheLookup(key string) (*Page, bool) {
	if !c.cacheInMemory {
		return nil, false
	}
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	page, ok := c.memoryCache[key]
	return page, ok
}

// cacheLookupByName returns any cached page for a command, regardless of
// platform, via the name index. The previous implementation scanned every
// cached entry on each call while holding the read lock.
func (c *Client) cacheLookupByName(name string) (*Page, bool) {
	if !c.cacheInMemory {
		return nil, false
	}
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	page, ok := c.byName[name]
	return page, ok
}

// cacheStore records a page in both the primary cache and the name index,
// evicting arbitrary entries once the cache is full. Exact LRU ordering is not
// worth the bookkeeping here: the cache exists to avoid repeated decoding
// within one session, not to guarantee a hit rate.
func (c *Client) cacheStore(key string, page *Page) {
	if !c.cacheInMemory || page == nil {
		return
	}

	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	if _, exists := c.memoryCache[key]; !exists {
		for len(c.memoryCache) >= maxMemoryCachedPages {
			for evictKey, evicted := range c.memoryCache {
				delete(c.memoryCache, evictKey)
				if evicted != nil && c.byName[evicted.Name] == evicted {
					delete(c.byName, evicted.Name)
				}
				break
			}
		}
	}

	c.memoryCache[key] = page
	c.byName[page.Name] = page
}

// NewArchiveHTTPClient returns a client for bulk downloads.
//
// It intentionally leaves http.Client.Timeout unset: that field covers the whole
// exchange including the body, so a value sized for a 4KB page aborts a
// multi-megabyte archive mid-stream. Stalls are caught by the transport's
// response-header timeout, and the total budget is the caller's context.
func NewArchiveHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = archiveResponseHeaderTimeout

	return &http.Client{Transport: transport}
}

// SetHTTPClient sets a custom HTTP client (useful for testing)
func (c *Client) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

// SetStorage sets the local storage
func (c *Client) SetStorage(storage *Storage) {
	c.storage = storage
	c.clearCommandCaches()
}

// SetOfflineMode enables or disables offline-only mode
func (c *Client) SetOfflineMode(offline bool) {
	c.offlineMode.Store(offline)
}

// SetAutoDetect enables or disables auto-detection
func (c *Client) SetAutoDetect(auto bool) {
	c.autoDetect = auto
}

// IsOfflineMode returns true if client is in offline mode
func (c *Client) IsOfflineMode() bool {
	return c.offlineMode.Load()
}

// IsOnline checks if the client can connect to the internet
func (c *Client) IsOnline(ctx context.Context) bool {
	if c.offlineMode.Load() {
		return false
	}

	c.onlineMu.RLock()
	if !c.onlineCheckedAt.IsZero() && time.Since(c.onlineCheckedAt) < c.onlineCheckTTL {
		online := c.onlineCached
		c.onlineMu.RUnlock()
		return online
	}
	c.onlineMu.RUnlock()

	// Try to fetch a small page to check connectivity
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/pages/%s/%s.md", c.baseURL, PlatformCommon, "ls")
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.setOnlineStatus(false)
		return false
	}
	defer resp.Body.Close()

	online := resp.StatusCode == http.StatusOK
	c.setOnlineStatus(online)
	return online
}

// GetPage retrieves a TLDR page for a specific command and platform
// Auto-detects online/offline and falls back to local storage automatically
func (c *Client) GetPage(ctx context.Context, command, platform string) (*Page, error) {
	lang := c.language
	if lang == "" {
		lang = "en"
	}
	cacheKey := fmt.Sprintf("%s/%s/%s", lang, platform, command)

	// Check memory cache first
	if page, ok := c.cacheLookup(cacheKey); ok {
		return page, nil
	}

	// Check local storage second
	if c.storage != nil {
		page, err := c.storage.GetPage(command, platform, lang)
		if err == nil {
			c.cacheStore(cacheKey, page)
			return page, nil
		}
	}

	// If offline mode, don't try remote
	if c.offlineMode.Load() {
		return nil, fmt.Errorf("page not found in local storage (offline mode): %s/%s", platform, command)
	}

	// Try to fetch from remote
	var langDir string
	if lang == "en" {
		langDir = "pages"
	} else {
		langDir = "pages." + lang
	}
	url := pageURL(c.baseURL, langDir, platform, command)
	content, err := c.fetch(ctx, url)

	if err != nil && lang != "en" {
		// Fallback to english if not found
		if errors.Is(err, errPageNotFound) {
			fallbackURL := pageURL(c.baseURL, "pages", platform, command)
			content, err = c.fetch(ctx, fallbackURL)
			if err == nil {
				lang = "en"
			}
		}
	}

	if err != nil {
		// Remote availability error - auto fall back to offline mode if autoDetect is enabled
		if c.autoDetect && isRemoteError(err) {
			c.markRemoteUnavailable()
			c.offlineMode.Store(true)
			return nil, fmt.Errorf("offline mode: page not found in local storage: %s/%s (use 'wut db sync' to download)", platform, command)
		}
		return nil, err
	}

	// Parse and save
	page := c.parsePage(content, command, platform, lang)

	// Save to local storage if available
	if c.storage != nil {
		_ = c.storage.SavePage(page)
	}

	c.cacheStore(cacheKey, page)
	c.rememberAvailableCommand(page.Name)

	return page, nil
}

// SearchPages searches for TLDR pages across all platforms
func (c *Client) SearchPages(ctx context.Context, query string) ([]Page, error) {
	// Try local storage first if offline mode or auto-detect
	if c.offlineMode.Load() || (c.autoDetect && !c.IsOnline(ctx)) {
		if c.storage != nil {
			storedPages, err := c.storage.SearchLocalLimited(query, 50)
			if err == nil && len(storedPages) > 0 {
				pages := make([]Page, len(storedPages))
				for i, sp := range storedPages {
					pages[i] = Page{
						Name:        sp.Name,
						Platform:    sp.Platform,
						Description: sp.Description,
						Examples:    sp.Examples,
						RawContent:  sp.RawContent,
					}
				}
				return pages, nil
			}
		}
	}

	platforms := []string{
		PlatformCommon,
		PlatformLinux,
		PlatformMacOS,
		PlatformWindows,
	}

	var pages []Page
	seen := make(map[string]bool)

	for _, platform := range platforms {
		page, err := c.GetPage(ctx, query, platform)
		if err != nil {
			continue
		}

		// Avoid duplicates
		key := page.Name + page.Description
		if !seen[key] {
			seen[key] = true
			pages = append(pages, *page)
		}
	}

	return pages, nil
}

// GetPageAnyPlatform tries to get a page from any available platform
// Auto-detects online/offline and falls back automatically
func (c *Client) GetPageAnyPlatform(ctx context.Context, command string) (*Page, error) {
	// Check memory cache first, by name index rather than a full scan
	if page, ok := c.cacheLookupByName(command); ok {
		return page, nil
	}

	// Check local storage second
	lang := c.language
	if lang == "" {
		lang = "en"
	}
	if c.storage != nil {
		page, err := c.storage.GetPageAnyPlatform(command, lang)
		if err == nil {
			c.cacheStore(fmt.Sprintf("%s/%s/%s", page.Language, page.Platform, page.Name), page)
			return page, nil
		}
	}

	// If offline mode, don't try remote
	if c.offlineMode.Load() {
		return nil, fmt.Errorf("page not found in local storage (offline mode): %s", command)
	}

	// Try to fetch from remote with auto fallback
	platforms := []string{
		PlatformCommon,
		PlatformLinux,
		PlatformMacOS,
		PlatformWindows,
		PlatformFreeBSD,
		PlatformOpenBSD,
		PlatformNetBSD,
		PlatformSunOS,
		PlatformAndroid,
	}

	for _, platform := range platforms {
		page, err := c.GetPage(ctx, command, platform)
		if err == nil {
			return page, nil
		}
		if isRemoteError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("%w for command: %s", errPageNotFound, command)
}

// pageURL builds a TLDR page URL with each user-controlled segment escaped.
//
// The command name comes from the command line, so interpolating it raw let a
// name containing "../" or a query string reshape the request path.
func pageURL(baseURL, langDir, platform, command string) string {
	return fmt.Sprintf("%s/%s/%s/%s.md",
		strings.TrimSuffix(baseURL, "/"),
		url.PathEscape(langDir),
		url.PathEscape(platform),
		url.PathEscape(command),
	)
}

// fetch retrieves raw content from the given URL
func (c *Client) fetch(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: failed to fetch: %w", errRemoteTemporary, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.setOnlineStatus(true)
		return "", errPageNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: unexpected status code: %d", errRemoteTemporary, resp.StatusCode)
	}

	limitedReader := io.LimitReader(resp.Body, maxBodySize)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("%w: failed to read body: %w", errRemoteTemporary, err)
	}

	c.setOnlineStatus(true)
	return string(body), nil
}

// parsePage parses raw markdown content into a Page struct
func (c *Client) parsePage(content, name, platform, language string) *Page {
	if language == "" {
		language = "en"
	}
	page := &Page{
		Name:       name,
		Platform:   platform,
		Language:   language,
		RawContent: content,
		Examples:   []Example{},
	}

	lines := strings.Split(content, "\n")
	var inExample bool
	var currentExample Example

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		// Title line (starts with #)
		if after, ok := strings.CutPrefix(line, "# "); ok {
			page.Name = after
			continue
		}

		// Description line (starts with >)
		if after, ok := strings.CutPrefix(line, "> "); ok {
			page.Description = after
			continue
		}

		// Example description (starts with -)
		if strings.HasPrefix(line, "- ") {
			// Save previous example if exists
			if currentExample.Command != "" {
				page.Examples = append(page.Examples, currentExample)
			}
			currentExample = Example{
				Description: strings.TrimPrefix(line, "- "),
			}
			inExample = true
			continue
		}

		// Example command (starts with `)
		if inExample && strings.HasPrefix(line, "`") && strings.HasSuffix(line, "`") {
			cmd := strings.Trim(line, "`")
			// Replace {{variable}} with <variable>
			cmd = formatCommand(cmd)
			currentExample.Command = cmd
			inExample = false

			// Save the example
			if currentExample.Description != "" {
				page.Examples = append(page.Examples, currentExample)
				currentExample = Example{}
			}
		}
	}

	return page
}

// formatCommand formats a command by replacing {{variable}} placeholders
func formatCommand(cmd string) string {
	// Replace {{variable}} with <variable>
	return variableRe.ReplaceAllString(cmd, "<$1>")
}

// GetAvailableCommands returns a list of available commands from local storage
// or a default list if local storage is empty
func (c *Client) GetAvailableCommands(ctx context.Context) ([]string, error) {
	c.commandsMu.RLock()
	if len(c.availableCommands) > 0 {
		commands := append([]string(nil), c.availableCommands...)
		c.commandsMu.RUnlock()
		return commands, nil
	}
	c.commandsMu.RUnlock()

	// Try local storage first
	if c.storage != nil {
		commands, err := c.storage.ListCommands(0)
		if err == nil && len(commands) > 0 {
			c.commandsMu.Lock()
			c.availableCommands = append([]string(nil), commands...)
			c.commandsMu.Unlock()
			return commands, nil
		}
	}

	// Return default list
	commands := getDefaultCommands()
	c.commandsMu.Lock()
	c.availableCommands = append([]string(nil), commands...)
	c.commandsMu.Unlock()
	return commands, nil
}

// FindCommandMatches returns ranked command-name suggestions for a query.
func (c *Client) FindCommandMatches(ctx context.Context, query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	matchLimit := max(limit, 50)

	commands, err := c.GetAvailableCommands(ctx)
	if err != nil {
		return nil, err
	}
	if query == "" {
		commands = rankBrowseCommands(commands)
		if len(commands) > limit {
			return commands[:limit], nil
		}
		return commands, nil
	}

	cacheKey := strings.ToLower(strings.TrimSpace(query))
	if cached, ok := c.matchCache.Get(cacheKey); ok {
		if len(cached) > limit {
			return append([]string(nil), cached[:limit]...), nil
		}
		return append([]string(nil), cached...), nil
	}

	matches := c.matcher.MatchMultiple(cacheKey, commands)
	results := make([]string, 0, min(len(matches), matchLimit))
	seen := make(map[string]struct{}, limit)

	for _, match := range matches {
		if _, ok := seen[match.Target]; ok {
			continue
		}
		seen[match.Target] = struct{}{}
		results = append(results, match.Target)
		if len(results) >= matchLimit {
			c.matchCache.Set(cacheKey, append([]string(nil), results...), 5*time.Minute)
			return append([]string(nil), results[:limit]...), nil
		}
	}

	queryLower := cacheKey
	for _, command := range commands {
		if _, ok := seen[command]; ok {
			continue
		}
		if strings.Contains(strings.ToLower(command), queryLower) {
			results = append(results, command)
			if len(results) >= matchLimit {
				break
			}
		}
	}

	c.matchCache.Set(cacheKey, append([]string(nil), results...), 5*time.Minute)
	if len(results) > limit {
		return append([]string(nil), results[:limit]...), nil
	}
	return results, nil
}

// getDefaultCommands returns the default list of common commands
func getDefaultCommands() []string {
	return []string{
		"git", "docker", "npm", "node", "python", "pip", "cargo",
		"kubectl", "helm", "terraform", "ansible", "vagrant",
		"ls", "cd", "pwd", "cat", "less", "head", "tail",
		"grep", "find", "sed", "awk", "sort", "wc",
		"tar", "zip", "unzip", "gzip",
		"chmod", "chown", "mkdir", "rm", "cp", "mv",
		"ps", "htop", "kill", "killall",
		"ssh", "scp", "rsync", "curl", "wget", "ping", "netstat",
		"vim", "vi", "nano",
		"make", "cmake", "gcc", "clang",
		"ffmpeg",
	}
}

func buildDefaultCommandRank(commands []string) map[string]int {
	ranks := make(map[string]int, len(commands))
	for i, command := range commands {
		ranks[command] = len(commands) - i
	}
	return ranks
}

func rankBrowseCommands(commands []string) []string {
	ranked := append([]string(nil), commands...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := browseCommandScore(ranked[i])
		right := browseCommandScore(ranked[j])
		if left == right {
			return ranked[i] < ranked[j]
		}
		return left > right
	})
	return ranked
}

func browseCommandScore(command string) int {
	score := 0
	command = strings.TrimSpace(command)
	if command == "" {
		return -1000
	}

	if rank, ok := defaultCommandRank[command]; ok {
		score += 10_000 + rank
	}

	first := command[0]
	switch {
	case (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z'):
		score += 200
	case first >= '0' && first <= '9':
		score += 75
	default:
		score -= 300
	}

	score += max(0, 40-len(command))

	if strings.IndexFunc(command, func(r rune) bool {
		return !(r == '-' || r == '+' || r == '.' || r == '_' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z'))
	}) == -1 {
		score += 25
	}

	return score
}

// HasLocalStorage returns true if client has local storage configured
func (c *Client) HasLocalStorage() bool {
	return c.storage != nil
}

// GetStorage returns the local storage
func (c *Client) GetStorage() *Storage {
	return c.storage
}

// ClearMemoryCache clears the in-memory cache
func (c *Client) ClearMemoryCache() {
	c.cacheMu.Lock()
	c.memoryCache = make(map[string]*Page)
	c.byName = make(map[string]*Page)
	c.cacheMu.Unlock()
	c.clearCommandCaches()
}

// GetMemoryCacheSize returns the number of pages in memory cache
func (c *Client) GetMemoryCacheSize() int {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	return len(c.memoryCache)
}

func (c *Client) setOnlineStatus(online bool) {
	c.onlineMu.Lock()
	c.onlineCached = online
	c.onlineCheckedAt = time.Now()
	c.onlineMu.Unlock()
	if online {
		c.offlineMode.Store(false)
	}
}

func (c *Client) markRemoteUnavailable() {
	age := c.onlineCheckTTL - c.remoteFailureTTL
	if age < 0 {
		age = 0
	}

	c.onlineMu.Lock()
	c.onlineCached = false
	c.onlineCheckedAt = time.Now().Add(-age)
	c.onlineMu.Unlock()
}

func isRemoteError(err error) bool {
	return errors.Is(err, errRemoteTemporary)
}

func (c *Client) clearCommandCaches() {
	c.commandsMu.Lock()
	c.availableCommands = nil
	c.commandsMu.Unlock()
	if c.matchCache != nil {
		c.matchCache.Clear()
	}
}

func (c *Client) rememberAvailableCommand(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}

	c.commandsMu.Lock()
	defer c.commandsMu.Unlock()

	for _, existing := range c.availableCommands {
		if existing == command {
			return
		}
	}

	c.availableCommands = append(c.availableCommands, command)
}
