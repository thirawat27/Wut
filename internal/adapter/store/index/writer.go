package index

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

// Builder accumulates pages and writes an index.
type Builder struct {
	pages   []knowledge.Page
	release string
	builtAt time.Time
	// vectors is filled by the embedder at M4; an index with none simply has
	// an empty vectors section and falls back to lexical search.
	vectors [][]int8
	vecDim  int
	modelID string
	// termVectors is looked up by term when the model section is written.
	termVectors map[string][]int8
}

// NewBuilder starts an index build.
func NewBuilder(release string) *Builder {
	return &Builder{release: release, builtAt: time.Now().UTC()}
}

// Add appends a page. Pages with no name are dropped rather than indexed as
// an empty string, which would match everything.
func (b *Builder) Add(p knowledge.Page) {
	if strings.TrimSpace(p.Name) == "" {
		return
	}
	b.pages = append(b.pages, p)
}

// Len reports how many pages have been added.
func (b *Builder) Len() int { return len(b.pages) }

// SetVectors attaches embeddings, one per unit, in unit order.
func (b *Builder) SetVectors(vecs [][]int8, dim int) {
	b.vectors, b.vecDim = vecs, dim
}

// SetTermVectors attaches the trained term vectors that let a question be
// embedded at query time.
func (b *Builder) SetTermVectors(v map[string][]int8) { b.termVectors = v }

// SetModelID records which model produced the vectors.
//
// The reader refuses to compare a query vector against stored vectors built by
// a different model. Without this the mismatch is silent: cosine similarity
// over two unrelated vector spaces returns plausible numbers and nonsense
// rankings, which is far worse than no semantic search at all.
func (b *Builder) SetModelID(id string) { b.modelID = id }

// Units returns the searchable units in the order the index will store them,
// so an embedder can produce vectors that line up.
func (b *Builder) Units() []UnitRef {
	b.sortPages()
	var out []UnitRef
	for pi, p := range b.pages {
		out = append(out, UnitRef{Page: uint32(pi), Example: -1, Text: p.Name + " " + p.Description})
		for ei, ex := range p.Examples {
			out = append(out, UnitRef{
				Page:    uint32(pi),
				Example: int32(ei),
				Text:    ex.Description + " " + ex.Command,
			})
		}
	}
	return out
}

// UnitRef is a searchable unit with the text that represents it.
type UnitRef struct {
	Page    uint32
	Example int32
	Text    string
}

// sortPages puts pages in a stable order so two builds of the same input
// produce byte-identical files, which is what makes the content hash in the
// filename meaningful.
func (b *Builder) sortPages() {
	// Sorted by the *lowercased* name, because that is what Lookup binary
	// searches on. Sorting by the original case and searching case-folded is
	// a silent failure: every page with a capital letter becomes unfindable by
	// name while still turning up in search results.
	sort.SliceStable(b.pages, func(i, j int) bool {
		li, lj := strings.ToLower(b.pages[i].Name), strings.ToLower(b.pages[j].Name)
		if li != lj {
			return li < lj
		}
		return b.pages[i].Platform < b.pages[j].Platform
	})
}

// WriteTo serialises the index.
func (b *Builder) WriteTo(path string) error {
	if len(b.pages) == 0 {
		return errors.New("refusing to write an index with no pages")
	}
	b.sortPages()

	pagesBlob, pageOffsets, err := b.encodePages()
	if err != nil {
		return err
	}
	units, unitsBlob := b.encodeUnits()
	terms, postings := b.buildPostings(units)
	termsBlob := encodeStrings(terms)
	namesBlob := b.encodeNames()
	vectorsBlob := b.encodeVectors()
	modelBlob := b.encodeTermVectors(terms)

	// Page offsets ride along at the front of the pages section so the reader
	// needs only one section to resolve a page.
	pagesSection := append(encodeUint32s(pageOffsets), pagesBlob...)

	h := header{
		Magic:        magic,
		Schema:       Schema,
		VecDim:       uint16(b.vecDim),
		BuiltAtUnix:  b.builtAt.Unix(),
		PageCount:    uint32(len(b.pages)),
		ExampleCount: uint32(b.exampleCount()),
		UnitCount:    uint32(len(units)),
		TermCount:    uint32(len(terms)),
		VectorCount:  uint32(len(b.vectors)),
		ReleaseLen:   uint32(len(b.releaseField())),
	}

	blobs := [sectionCount][]byte{
		secPages:    pagesSection,
		secUnits:    unitsBlob,
		secTerms:    termsBlob,
		secPostings: postings,
		secNames:    namesBlob,
		secVectors:  vectorsBlob,
		secModel:    modelBlob,
	}

	offset := uint64(headerSize + len(b.releaseField()))
	for i, blob := range blobs {
		h.Sections[i] = section{
			Offset: offset,
			Length: uint64(len(blob)),
			CRC:    crc32.Checksum(blob, castagnoli),
		}
		offset += uint64(len(blob))
	}

	var buf bytes.Buffer
	if err := writeHeader(&buf, h); err != nil {
		return err
	}
	buf.WriteString(b.releaseField())
	for _, blob := range blobs {
		buf.Write(blob)
	}
	return writeFileAtomic(path, buf.Bytes())
}

// releaseField packs the corpus digest and the model id into one string, so
// the header stays a fixed size and both travel with the index.
func (b *Builder) releaseField() string {
	if b.modelID == "" {
		return b.release
	}
	return b.release + string(rune(0)) + b.modelID
}

func (b *Builder) exampleCount() int {
	n := 0
	for _, p := range b.pages {
		n += len(p.Examples)
	}
	return n
}

// encodePages compresses each page individually.
//
// Per-page rather than whole-section: compressing the lot would be smaller,
// but then reading one page means decompressing all of them, and `wut explain`
// reads exactly one.
func (b *Builder) encodePages() (blob []byte, offsets []uint32, err error) {
	var out bytes.Buffer
	for _, p := range b.pages {
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, nil, err
		}
		var comp bytes.Buffer
		zw, err := flate.NewWriter(&comp, flate.BestCompression)
		if err != nil {
			return nil, nil, err
		}
		if _, err := zw.Write(raw); err != nil {
			return nil, nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, nil, err
		}
		offsets = append(offsets, uint32(out.Len()))
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(comp.Len()))
		out.Write(lenBuf[:])
		out.Write(comp.Bytes())
	}
	// A terminating offset makes the reader's length arithmetic uniform.
	offsets = append(offsets, uint32(out.Len()))
	return out.Bytes(), offsets, nil
}

func (b *Builder) encodeUnits() ([]unit, []byte) {
	var units []unit
	var buf []byte
	for pi, p := range b.pages {
		add := func(ex int32, text string) {
			u := unit{Page: uint32(pi), Example: ex, Length: uint32(len(knowledge.Tokenize(text)))}
			units = append(units, u)
			buf = putUvarint(buf, uint64(u.Page))
			buf = putUvarint(buf, uint64(uint32(u.Example)+1)) // -1 becomes 0
			buf = putUvarint(buf, uint64(u.Length))
		}
		add(-1, p.Name+" "+p.Description)
		for ei, ex := range p.Examples {
			add(int32(ei), ex.Description+" "+ex.Command)
		}
	}
	return units, buf
}

// buildPostings builds the inverted index.
func (b *Builder) buildPostings(units []unit) ([]string, []byte) {
	posting := map[string][]uint32{}
	for i, u := range units {
		text := b.unitText(u)
		for _, term := range knowledge.Tokenize(text) {
			posting[term] = append(posting[term], uint32(i))
		}
		// The page name is a term in its own right, always, even if the
		// tokenizer would have dropped it. "ls" is two characters and is the
		// most useful query anyone types.
		name := strings.ToLower(b.pages[u.Page].Name)
		if name != "" {
			posting[name] = appendUnique(posting[name], uint32(i))
		}
	}

	terms := make([]string, 0, len(posting))
	for t := range posting {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	// Each list is delta encoded, which is what keeps the whole postings
	// section to a couple of megabytes for a corpus this size.
	var buf []byte
	var offsets []uint32
	for _, t := range terms {
		docs := posting[t]
		sort.Slice(docs, func(i, j int) bool { return docs[i] < docs[j] })
		offsets = append(offsets, uint32(len(buf)))
		buf = putUvarint(buf, uint64(len(docs)))
		prev := uint32(0)
		for _, d := range docs {
			buf = putUvarint(buf, uint64(d-prev))
			prev = d
		}
	}
	offsets = append(offsets, uint32(len(buf)))
	return terms, append(encodeUint32s(offsets), buf...)
}

func appendUnique(list []uint32, v uint32) []uint32 {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func (b *Builder) unitText(u unit) string {
	p := b.pages[u.Page]
	if u.Example < 0 {
		return p.Name + " " + p.Description
	}
	if int(u.Example) < len(p.Examples) {
		ex := p.Examples[u.Example]
		return p.Name + " " + ex.Description + " " + ex.Command
	}
	return p.Name
}

// encodeNames writes the sorted lookup table: "name\x00platform" per entry.
func (b *Builder) encodeNames() []byte {
	entries := make([]string, len(b.pages))
	for i, p := range b.pages {
		entries[i] = strings.ToLower(p.Name) + string(rune(0)) + string(p.Platform)
	}
	return encodeStrings(entries)
}

// encodeTermVectors writes one vector per dictionary term, in the same order
// as the terms section, so the reader can index into it directly.
func (b *Builder) encodeTermVectors(terms []string) []byte {
	if len(b.termVectors) == 0 || b.vecDim == 0 {
		return nil
	}
	buf := make([]byte, len(terms)*b.vecDim)
	for i, t := range terms {
		v := b.termVectors[t]
		for d := 0; d < b.vecDim && d < len(v); d++ {
			buf[i*b.vecDim+d] = byte(v[d])
		}
	}
	return buf
}

func (b *Builder) encodeVectors() []byte {
	if len(b.vectors) == 0 || b.vecDim == 0 {
		return nil
	}
	buf := make([]byte, 0, len(b.vectors)*b.vecDim)
	for _, v := range b.vectors {
		for i := 0; i < b.vecDim; i++ {
			if i < len(v) {
				buf = append(buf, byte(v[i]))
			} else {
				buf = append(buf, 0)
			}
		}
	}
	return buf
}

// encodeStrings writes a length-prefixed offset table followed by the bytes.
func encodeStrings(list []string) []byte {
	var offsets []uint32
	var body []byte
	for _, s := range list {
		offsets = append(offsets, uint32(len(body)))
		body = append(body, s...)
	}
	offsets = append(offsets, uint32(len(body)))
	return append(encodeUint32s(offsets), body...)
}

func encodeUint32s(vals []uint32) []byte {
	buf := make([]byte, 4+len(vals)*4)
	binary.LittleEndian.PutUint32(buf, uint32(len(vals)))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[4+i*4:], v)
	}
	return buf
}

// writeFileAtomic writes through a temp file and renames.
//
// A half-written index must never be observed: the reader would report it as
// damaged and the user would be told to sync again, which they just did.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wut-index-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("replace existing index: %w", err)
		}
	}
	return os.Rename(name, path)
}
