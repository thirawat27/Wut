// Package index is the packed knowledge file: tldr pages, a lexical index
// over them, and (from M4) their embeddings, in one immutable artifact.
//
// Why a bespoke file rather than a key-value store:
//
//   - It is derived data. It can always be deleted and rebuilt, so it needs no
//     transactions, no migrations, and no corruption-recovery story beyond
//     "run wut db sync".
//   - It is written once and read constantly, which is the opposite of what a
//     B-tree is tuned for.
//   - Vectors need to be contiguous. A KV store cannot give you 25,000 float
//     arrays back to back, and scanning them is the whole of semantic search.
//
// The file is replaced by atomic swap, so a half-written index is never
// observed, and every section carries a CRC so a damaged one produces a clear
// message rather than a panic.
package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Magic identifies the file. The trailing NUL makes a truncated read obvious.
var magic = [8]byte{'W', 'U', 'T', 'I', 'D', 'X', 0, 1}

// Schema is the format version. It is not migrated: a mismatch rebuilds.
const Schema uint16 = 1

// Section ids, in the order they appear in the file.
const (
	secPages    = 0 // per-page flate-compressed JSON, with an offset table
	secUnits    = 1 // (page, example) pairs — the things search returns
	secTerms    = 2 // sorted term dictionary
	secPostings = 3 // per-term posting lists, varint delta encoded
	secNames    = 4 // sorted (name, platform) for exact lookup
	secVectors  = 5 // int8 unit embeddings, contiguous
	// secModel holds one int8 vector per dictionary term, aligned with the
	// terms section.
	//
	// The unit vectors alone are not enough: a question has to be turned into
	// a vector in the same space before it can be compared to anything, and
	// that composition needs the terms' learned vectors. Storing them is what
	// makes the semantic index self-contained — no model file to download, to
	// version, or to accidentally mismatch.
	secModel     = 6
	sectionCount = 7
)

// sectionNames are used in error messages, because "section 3 is damaged" is
// not something a user can act on.
var sectionNames = [sectionCount]string{"pages", "units", "terms", "postings", "names", "vectors", "model"}

// castagnoli is the CRC table. Chosen for the hardware instruction, since the
// whole file is checked on open.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// section describes where one section lives.
type section struct {
	Offset uint64
	Length uint64
	CRC    uint32
	_      uint32 // padding, so the struct is a round 24 bytes
}

// header is the fixed-size prefix.
//
// Every count here is also derivable from the sections themselves. They are
// stored anyway so `wut db status` costs one read of the first 256 bytes
// rather than a full parse.
type header struct {
	Magic        [8]byte
	Schema       uint16
	Flags        uint16
	VecDim       uint16
	_            uint16
	BuiltAtUnix  int64
	PageCount    uint32
	ExampleCount uint32
	UnitCount    uint32
	TermCount    uint32
	VectorCount  uint32
	ReleaseLen   uint32
	Sections     [sectionCount]section
}

// headerSize is the encoded size of header, computed rather than assumed.
const headerSize = 8 + 2 + 2 + 2 + 2 + 8 + 4*6 + sectionCount*24

// Errors a caller is expected to handle.
var (
	// ErrNotIndex means the file is not one of ours at all.
	ErrNotIndex = errors.New("not a wut index file")
	// ErrSchemaMismatch means it is ours but from another version. The right
	// response is always to rebuild, never to migrate.
	ErrSchemaMismatch = errors.New("index was built by a different version of wut")
	// ErrDamaged means a section failed its checksum.
	ErrDamaged = errors.New("index is damaged")
)

func writeHeader(w io.Writer, h header) error {
	return binary.Write(w, binary.LittleEndian, &h)
}

func readHeader(r io.Reader) (header, error) {
	var h header
	if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return h, fmt.Errorf("%w: file is too short", ErrNotIndex)
		}
		return h, err
	}
	if h.Magic != magic {
		return h, ErrNotIndex
	}
	if h.Schema != Schema {
		return h, fmt.Errorf("%w: found schema %d, this build expects %d", ErrSchemaMismatch, h.Schema, Schema)
	}
	return h, nil
}

// unit is one searchable thing: a whole page, or one example on it.
//
// Examples are indexed separately because that is what a person is actually
// looking for. "compress a folder to tar.gz" should return the tar example,
// not the tar page and a hunt through it.
type unit struct {
	Page    uint32
	Example int32 // -1 for the page itself
	// Length is the token count, used for length normalisation in scoring.
	Length uint32
}

// putUvarint appends a varint, the encoding used throughout the posting lists.
func putUvarint(buf []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(buf, tmp[:n]...)
}
