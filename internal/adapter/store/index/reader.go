package index

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thirawat27/wut/internal/core/knowledge"
	"github.com/thirawat27/wut/internal/port"
)

// Reader answers queries against a packed index.
//
// The whole file is read into memory on open. At the size this corpus reaches
// — roughly ten megabytes — that is faster than any lazy scheme, costs one
// allocation, and removes an entire class of partial-read bug. If the corpus
// ever grows past what is comfortable to hold, the sections are already laid
// out for random access and only this constructor has to change.
type Reader struct {
	path    string
	raw     []byte
	head    header
	release string
	modelID string

	pageOffsets []uint32
	pagesBlob   []byte

	units []unit
	terms []string

	postingOffsets []uint32
	postingsBlob   []byte

	names []string

	vectors  []int8
	termVecs []int8

	mu        sync.Mutex
	pageCache map[uint32]knowledge.Page
	sizeBytes int64
}

var _ port.KnowledgeSource = (*Reader)(nil)

// Open reads and validates an index file.
func Open(path string) (*Reader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{path: path, raw: data, pageCache: map[uint32]knowledge.Page{}, sizeBytes: int64(len(data))}

	h, err := readHeader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.head = h

	base := headerSize
	if len(data) < base+int(h.ReleaseLen) {
		return nil, fmt.Errorf("%w: truncated header", ErrDamaged)
	}
	field := string(data[base : base+int(h.ReleaseLen)])
	if i := strings.IndexByte(field, 0); i >= 0 {
		r.release, r.modelID = field[:i], field[i+1:]
	} else {
		r.release = field
	}

	sections := make([][]byte, sectionCount)
	for i, s := range h.Sections {
		end := s.Offset + s.Length
		if end > uint64(len(data)) {
			return nil, fmt.Errorf("%w: %s section runs past the end of the file", ErrDamaged, sectionNames[i])
		}
		blob := data[s.Offset:end]
		if crc32.Checksum(blob, castagnoli) != s.CRC {
			return nil, fmt.Errorf("%w: the %s section failed its checksum", ErrDamaged, sectionNames[i])
		}
		sections[i] = blob
	}

	if err := r.decode(sections); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Reader) decode(sections [][]byte) error {
	var err error
	if r.pageOffsets, r.pagesBlob, err = splitUint32Table(sections[secPages]); err != nil {
		return fmt.Errorf("%w: pages: %w", ErrDamaged, err)
	}
	if r.postingOffsets, r.postingsBlob, err = splitUint32Table(sections[secPostings]); err != nil {
		return fmt.Errorf("%w: postings: %w", ErrDamaged, err)
	}
	if r.terms, err = decodeStrings(sections[secTerms]); err != nil {
		return fmt.Errorf("%w: terms: %w", ErrDamaged, err)
	}
	if r.names, err = decodeStrings(sections[secNames]); err != nil {
		return fmt.Errorf("%w: names: %w", ErrDamaged, err)
	}
	if r.units, err = decodeUnits(sections[secUnits], int(r.head.UnitCount)); err != nil {
		return fmt.Errorf("%w: units: %w", ErrDamaged, err)
	}
	if raw := sections[secModel]; len(raw) > 0 {
		r.termVecs = make([]int8, len(raw))
		for i, b := range raw {
			r.termVecs[i] = int8(b)
		}
	}
	if raw := sections[secVectors]; len(raw) > 0 {
		r.vectors = make([]int8, len(raw))
		for i, b := range raw {
			r.vectors[i] = int8(b)
		}
	}
	if len(r.terms)+1 != len(r.postingOffsets) {
		return fmt.Errorf("%w: %d terms but %d posting offsets", ErrDamaged, len(r.terms), len(r.postingOffsets))
	}
	return nil
}

// ModelID reports which embedder built the stored vectors, or "" when there
// are none.
func (r *Reader) ModelID() string { return r.modelID }

// VectorDim reports the stored vector width.
func (r *Reader) VectorDim() int { return int(r.head.VecDim) }

// Ready reports a usable index.
func (r *Reader) Ready() bool { return r != nil && len(r.units) > 0 }

// Stats describes what is loaded.
func (r *Reader) Stats() port.KnowledgeStats {
	if r == nil {
		return port.KnowledgeStats{}
	}
	return port.KnowledgeStats{
		Ready:     r.Ready(),
		Pages:     int(r.head.PageCount),
		Examples:  int(r.head.ExampleCount),
		Vectors:   int(r.head.VectorCount),
		Release:   r.release,
		BuiltAt:   time.Unix(r.head.BuiltAtUnix, 0),
		Path:      r.path,
		SizeBytes: r.sizeBytes,
	}
}

// Page decodes one page by index, memoised.
func (r *Reader) Page(i uint32) (knowledge.Page, error) {
	r.mu.Lock()
	if p, ok := r.pageCache[i]; ok {
		r.mu.Unlock()
		return p, nil
	}
	r.mu.Unlock()

	if int(i)+1 >= len(r.pageOffsets) {
		return knowledge.Page{}, fmt.Errorf("%w: page %d is out of range", ErrDamaged, i)
	}
	start := r.pageOffsets[i]
	if int(start)+4 > len(r.pagesBlob) {
		return knowledge.Page{}, fmt.Errorf("%w: page %d offset is past the section", ErrDamaged, i)
	}
	n := binary.LittleEndian.Uint32(r.pagesBlob[start:])
	body := r.pagesBlob[start+4:]
	if int(n) > len(body) {
		return knowledge.Page{}, fmt.Errorf("%w: page %d claims %d bytes", ErrDamaged, i, n)
	}
	zr := flate.NewReader(bytes.NewReader(body[:n]))
	raw, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		return knowledge.Page{}, fmt.Errorf("%w: page %d: %w", ErrDamaged, i, err)
	}
	var p knowledge.Page
	if err := json.Unmarshal(raw, &p); err != nil {
		return knowledge.Page{}, fmt.Errorf("%w: page %d: %w", ErrDamaged, i, err)
	}

	r.mu.Lock()
	// A bounded cache: the corpus is small, but a long-lived daemon that
	// touched every page would otherwise hold the whole thing decompressed.
	if len(r.pageCache) > 512 {
		r.pageCache = map[uint32]knowledge.Page{}
	}
	r.pageCache[i] = p
	r.mu.Unlock()
	return p, nil
}

// Lookup finds a page by name, preferring the given platforms in order.
func (r *Reader) Lookup(_ context.Context, name string, platforms []knowledge.Platform) (knowledge.Page, bool, error) {
	if !r.Ready() {
		return knowledge.Page{}, false, nil
	}
	want := strings.ToLower(normalizeCommandName(name))
	if want == "" {
		return knowledge.Page{}, false, nil
	}
	if len(platforms) == 0 {
		platforms = []knowledge.Platform{knowledge.PlatformCommon}
	}

	// names is sorted by (name, platform), so the whole block for one command
	// is contiguous and a binary search lands inside it.
	lo := sort.Search(len(r.names), func(i int) bool {
		return nameOf(r.names[i]) >= want
	})
	var matches []uint32
	for i := lo; i < len(r.names) && nameOf(r.names[i]) == want; i++ {
		matches = append(matches, uint32(i))
	}
	if len(matches) == 0 {
		return knowledge.Page{}, false, nil
	}

	for _, plat := range platforms {
		for _, idx := range matches {
			if platformOf(r.names[idx]) == string(plat) {
				p, err := r.Page(idx)
				return p, err == nil, err
			}
		}
	}
	// The command exists but not for a preferred platform. Returning it anyway
	// is more useful than pretending it does not exist — a Linux page still
	// explains what `tar` does on a Mac.
	p, err := r.Page(matches[0])
	return p, err == nil, err
}

func nameOf(entry string) string {
	if i := strings.IndexByte(entry, 0); i >= 0 {
		return entry[:i]
	}
	return entry
}

func platformOf(entry string) string {
	if i := strings.IndexByte(entry, 0); i >= 0 {
		return entry[i+1:]
	}
	return ""
}

// normalizeCommandName strips a path and a Windows extension.
func normalizeCommandName(s string) string {
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		s = strings.TrimSuffix(strings.ToLower(s), ext)
	}
	return s
}

// postingsFor returns the unit ids for a term.
func (r *Reader) postingsFor(term string) []uint32 {
	i := sort.SearchStrings(r.terms, term)
	if i >= len(r.terms) || r.terms[i] != term {
		return nil
	}
	start, end := r.postingOffsets[i], r.postingOffsets[i+1]
	if int(end) > len(r.postingsBlob) || start > end {
		return nil
	}
	buf := r.postingsBlob[start:end]
	count, n := binary.Uvarint(buf)
	if n <= 0 {
		return nil
	}
	buf = buf[n:]
	out := make([]uint32, 0, count)
	prev := uint32(0)
	for i := uint64(0); i < count; i++ {
		delta, n := binary.Uvarint(buf)
		if n <= 0 {
			break
		}
		buf = buf[n:]
		prev += uint32(delta)
		out = append(out, prev)
	}
	return out
}

// ── decoding helpers ────────────────────────────────────────────────────────

func splitUint32Table(blob []byte) ([]uint32, []byte, error) {
	if len(blob) < 4 {
		return nil, nil, errors.New("section is too short for a table header")
	}
	n := binary.LittleEndian.Uint32(blob)
	need := 4 + int(n)*4
	if need > len(blob) {
		return nil, nil, fmt.Errorf("table claims %d entries, section holds %d bytes", n, len(blob))
	}
	vals := make([]uint32, n)
	for i := range vals {
		vals[i] = binary.LittleEndian.Uint32(blob[4+i*4:])
	}
	return vals, blob[need:], nil
}

func decodeStrings(blob []byte) ([]string, error) {
	offsets, body, err := splitUint32Table(blob)
	if err != nil {
		return nil, err
	}
	if len(offsets) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(offsets)-1)
	for i := 0; i+1 < len(offsets); i++ {
		start, end := offsets[i], offsets[i+1]
		if int(end) > len(body) || start > end {
			return nil, fmt.Errorf("string %d spans [%d,%d) of %d bytes", i, start, end, len(body))
		}
		out = append(out, string(body[start:end]))
	}
	return out, nil
}

func decodeUnits(blob []byte, count int) ([]unit, error) {
	out := make([]unit, 0, count)
	for i := 0; i < count; i++ {
		page, n1 := binary.Uvarint(blob)
		if n1 <= 0 {
			return nil, fmt.Errorf("unit %d: truncated page id", i)
		}
		blob = blob[n1:]
		ex, n2 := binary.Uvarint(blob)
		if n2 <= 0 {
			return nil, fmt.Errorf("unit %d: truncated example id", i)
		}
		blob = blob[n2:]
		length, n3 := binary.Uvarint(blob)
		if n3 <= 0 {
			return nil, fmt.Errorf("unit %d: truncated length", i)
		}
		blob = blob[n3:]
		out = append(out, unit{Page: uint32(page), Example: int32(ex) - 1, Length: uint32(length)})
	}
	return out, nil
}
