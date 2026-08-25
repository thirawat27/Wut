// Package tldr fetches the tldr-pages archive and turns it into an index.
//
// Every limit and timeout here exists because of a specific defect in the prototype:
//
//   - the prototype used one HTTP client with a 5-second whole-exchange timeout for both
//     4 KB page fetches and a multi-megabyte archive download, so `wut db sync`
//     could not finish on any normal connection. Timeout policy belongs to the
//     call, not to a shared client.
//   - the prototype copied the download and read each zip entry with no size cap, so a
//     hijacked release asset could exhaust disk or memory.
//   - the prototype preferred a `./tldr-main` directory in the *current working
//     directory* over the official archive, which meant `wut db sync` produced
//     a different database depending on where you ran it, and any directory
//     containing a planted tree could seed the command database.
package tldr

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/thirawat27/wut/internal/adapter/model/embed"
	"github.com/thirawat27/wut/internal/adapter/store/index"
	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/port"
)

// The one host WUT downloads pages from, named in code rather than config so
// it cannot be pointed somewhere else by a file.
const (
	archiveURL = "https://github.com/tldr-pages/tldr/releases/latest/download/tldr.zip"
	userAgent  = "wut/2 (+https://github.com/thirawat27/wut)"
)

// Limits.
const (
	// maxArchiveBytes bounds the download. The real archive is a few
	// megabytes; this is generous and still finite.
	maxArchiveBytes = 256 << 20
	// maxEntryBytes bounds one page. A tldr page is a couple of kilobytes, so
	// anything approaching this is not a page.
	maxEntryBytes = 1 << 20
	// maxEntries bounds a zip with an absurd number of members.
	maxEntries = 200000

	archiveTimeout        = 10 * time.Minute
	responseHeaderTimeout = 30 * time.Second
)

// Fetcher downloads and builds.
type Fetcher struct {
	// Client is used for the archive. It deliberately has no whole-exchange
	// Timeout: that is what broke the prototype. The transport bounds how long the
	// *server* may take to respond, and the context bounds the whole
	// operation, which is the right shape for a large download.
	Client *http.Client
}

var _ port.Syncer = (*Fetcher)(nil)

// NewFetcher returns a fetcher with the archive-appropriate client.
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: responseHeaderTimeout,
				Proxy:                 http.ProxyFromEnvironment,
			},
		},
	}
}

func report(progress func(string), format string, args ...any) {
	if progress != nil {
		progress(fmt.Sprintf(format, args...))
	}
}

// Sync downloads the archive, parses it, and writes an index.
//
// On any failure the existing index is left exactly as it was. A sync that
// half-succeeds and leaves the user worse off than before is the one outcome
// this function must never produce.
func (f *Fetcher) Sync(ctx context.Context, indexPath string, opts port.SyncOptions) (port.SyncResult, error) {
	started := time.Now()

	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()

	archive, digest, size, source, err := f.obtainArchive(ctx, opts)
	if err != nil {
		return port.SyncResult{}, err
	}
	if opts.FromArchive == "" {
		defer os.Remove(archive)
	}

	report(opts.Progress, "parsing pages")
	builder, err := buildFromZip(archive, digest)
	if err != nil {
		return port.SyncResult{}, err
	}
	if builder.Len() == 0 {
		return port.SyncResult{}, errors.New("the archive contained no pages; refusing to replace a working index with an empty one")
	}

	if opts.Embed {
		report(opts.Progress, "learning the vocabulary")
		if err := trainAndAttach(builder); err != nil {
			// Semantic search is an enhancement. Losing it should cost the
			// user nothing else, so the index is still written without it.
			report(opts.Progress, "semantic index skipped: %v", err)
		}
	}

	report(opts.Progress, "writing index")
	if err := builder.WriteTo(indexPath); err != nil {
		return port.SyncResult{}, err
	}

	res := port.SyncResult{
		IndexPath: indexPath,
		Pages:     builder.Len(),
		Bytes:     size,
		Digest:    digest,
		Source:    source,
		Took:      time.Since(started),
	}
	if stat, err := os.Stat(indexPath); err == nil {
		res.Bytes = stat.Size()
	}
	return res, nil
}

// trainAndAttach builds the semantic index from the corpus that was just
// parsed.
//
// Training on the corpus rather than downloading a model is what makes Tier 1
// available on every machine with no extra artifact: two passes over forty
// thousand short documents, a few hundred milliseconds, and the vectors know
// this vocabulary specifically.
func trainAndAttach(b *index.Builder) error {
	units := b.Units()
	if len(units) == 0 {
		return errors.New("no units to train on")
	}

	trainer := embed.NewTrainer()
	tokenised := make([][]string, len(units))
	for i, u := range units {
		tokenised[i] = knowledge.Tokenize(u.Text)
		trainer.Add(tokenised[i])
	}
	model := trainer.Finish()
	if model.Terms() == 0 {
		return errors.New("the corpus produced no usable terms")
	}

	vectors := make([][]int8, len(units))
	for i := range units {
		vectors[i] = embed.Quantize(model.EmbedTerms(tokenised[i]))
	}
	b.SetVectors(vectors, embed.Dimensions)
	b.SetTermVectors(model.QuantizedTerms())
	b.SetModelID(embed.ID)
	return nil
}

// obtainArchive returns a path to a local copy of the zip.
func (f *Fetcher) obtainArchive(ctx context.Context, opts port.SyncOptions) (path, digest string, size int64, source string, err error) {
	if opts.FromArchive != "" {
		// An explicit flag, never an implicit fallback. The prototype's defect was
		// not that a local source existed; it was that one could be picked up
		// silently from the working directory.
		report(opts.Progress, "using local archive %s", opts.FromArchive)
		sum, n, err := hashFile(opts.FromArchive)
		if err != nil {
			return "", "", 0, "", err
		}
		return opts.FromArchive, sum, n, "local:" + opts.FromArchive, nil
	}

	report(opts.Progress, "downloading tldr pages")
	tmp, digest, size, err := f.download(ctx)
	if err != nil {
		return "", "", 0, "", err
	}
	return tmp, digest, size, archiveURL, nil
}

func (f *Fetcher) download(ctx context.Context) (string, string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/zip")

	resp, err := f.Client.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("download tldr archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("download tldr archive: %s returned %s", archiveURL, resp.Status)
	}

	tmp, err := os.CreateTemp("", "wut-tldr-*.zip")
	if err != nil {
		return "", "", 0, err
	}
	name := tmp.Name()

	hash := sha256.New()
	// LimitReader caps what an unexpected or hostile response can cost.
	limited := io.LimitReader(resp.Body, maxArchiveBytes+1)
	written, err := io.Copy(io.MultiWriter(tmp, hash), limited)
	closeErr := tmp.Close()
	if err != nil {
		os.Remove(name)
		return "", "", 0, fmt.Errorf("download tldr archive: %w", err)
	}
	if closeErr != nil {
		os.Remove(name)
		return "", "", 0, closeErr
	}
	if written > maxArchiveBytes {
		os.Remove(name)
		return "", "", 0, fmt.Errorf("tldr archive is larger than the %d MB limit", maxArchiveBytes>>20)
	}
	return name, hex.EncodeToString(hash.Sum(nil)), written, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxArchiveBytes+1))
	if err != nil {
		return "", 0, err
	}
	if n > maxArchiveBytes {
		return "", 0, fmt.Errorf("archive is larger than the %d MB limit", maxArchiveBytes>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// buildFromZip parses every page in the archive.
func buildFromZip(archivePath, digest string) (*index.Builder, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open tldr archive: %w", err)
	}
	defer zr.Close()

	release := digest
	if len(release) > 12 {
		release = release[:12]
	}
	builder := index.NewBuilder(release)

	if len(zr.File) > maxEntries {
		return nil, fmt.Errorf("archive holds %d entries, which is not a tldr release", len(zr.File))
	}

	for _, entry := range zr.File {
		plat, name, ok := classifyEntry(entry.Name)
		if !ok {
			continue
		}
		if entry.UncompressedSize64 > maxEntryBytes {
			// Skip rather than abort: one oversized member should not cost the
			// user the other four thousand pages.
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes))
		rc.Close()
		if err != nil {
			continue
		}
		page := ParsePage(name, plat, string(raw))
		if page.Name != "" {
			builder.Add(page)
		}
	}
	return builder, nil
}

// classifyEntry decides whether a zip member is an English tldr page, and for
// which platform.
//
// Only English for now. Other languages live in pages.<lang>/ directories and
// would multiply the index size without a way to choose between them yet.
func classifyEntry(entryName string) (knowledge.Platform, string, bool) {
	clean := path.Clean(strings.ReplaceAll(entryName, "\\", "/"))
	// A zip member must never escape its own tree. This archive is trusted
	// today and the check costs nothing.
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", "", false
	}
	if !strings.HasSuffix(clean, ".md") {
		return "", "", false
	}
	parts := strings.Split(clean, "/")
	if len(parts) < 3 {
		return "", "", false
	}
	// .../pages/<platform>/<name>.md
	var platIdx = -1
	for i, p := range parts {
		if p == "pages" {
			platIdx = i + 1
			break
		}
	}
	if platIdx <= 0 || platIdx+1 >= len(parts) {
		return "", "", false
	}
	// pages.<lang> directories are skipped by the exact "pages" match above.
	plat := knowledge.Platform(parts[platIdx])
	switch plat {
	case knowledge.PlatformCommon, knowledge.PlatformLinux, knowledge.PlatformOSX,
		knowledge.PlatformWindows, knowledge.PlatformSunOS, knowledge.PlatformAndroid,
		knowledge.PlatformFreeBSD, knowledge.PlatformNetBSD, knowledge.PlatformOpenBSD:
	default:
		return "", "", false
	}
	name := strings.TrimSuffix(parts[len(parts)-1], ".md")
	if name == "" {
		return "", "", false
	}
	return plat, name, true
}
