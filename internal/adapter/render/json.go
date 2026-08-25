package render

import (
	"encoding/json"
	"io"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/pkg/wutjson"
)

// JSON emits the versioned public schema.
type JSON struct {
	Out    io.Writer
	Indent bool
}

// NewJSON builds a JSON renderer. Indented by default: the output is read by
// people at least as often as by programs, and `jq` does not care either way.
func NewJSON(out io.Writer) *JSON { return &JSON{Out: out, Indent: true} }

// Result writes a result payload.
func (j *JSON) Result(kind wutjson.Kind, query string, cands []candidate.Candidate, notes ...string) error {
	return j.write(wutjson.From(kind, query, cands, notes...))
}

// Error writes an error payload, so a script gets structured output on both
// paths rather than JSON on success and prose on failure.
func (j *JSON) Error(code int, err error, hint string) error {
	return j.write(wutjson.NewError(code, err, hint))
}

// Any writes an arbitrary payload for the commands whose output is not a
// candidate list — doctor, status, config.
func (j *JSON) Any(v any) error { return j.write(v) }

func (j *JSON) write(v any) error {
	enc := json.NewEncoder(j.Out)
	if j.Indent {
		enc.SetIndent("", "  ")
	}
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
