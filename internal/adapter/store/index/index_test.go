package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

func page(name string, plat knowledge.Platform, desc string, examples ...[2]string) knowledge.Page {
	p := knowledge.Page{Name: name, Platform: plat, Description: desc}
	for _, e := range examples {
		p.Examples = append(p.Examples, knowledge.Example{Description: e[0], Command: e[1]})
	}
	return p
}

// corpus is a miniature tldr, shaped like the real one including the trap that
// broke the first version of the scorer: `compress` is both a common English
// word and an obscure real command.
func corpus() []knowledge.Page {
	return []knowledge.Page{
		page("tar", knowledge.PlatformCommon,
			"Archiving utility. Often combined with a compression method such as gzip.",
			[2]string{"create an archive and write it to a file", "tar cf {{target.tar}} {{file}}"},
			[2]string{"create a gzipped archive and write it to a file", "tar czf {{target.tar.gz}} {{folder}}"},
			[2]string{"extract a compressed archive into the current directory", "tar xzf {{source.tar.gz}}"},
		),
		page("compress", knowledge.PlatformLinux,
			"Compress files using the Lempel-Ziv-Welch algorithm.",
			[2]string{"compress specific files", "compress {{file}}"},
			[2]string{"display compression percentage", "compress -v {{file}}"},
		),
		page("zip", knowledge.PlatformCommon,
			"Package and compress files into a zip archive.",
			[2]string{"add a folder to an archive", "zip -r {{archive.zip}} {{folder}}"},
		),
		page("git", knowledge.PlatformCommon,
			"Distributed version control system.",
			[2]string{"undo the last commit keeping the changes", "git reset HEAD~"},
		),
		page("docker", knowledge.PlatformCommon,
			"Manage Docker containers and images.",
			[2]string{"list all containers running and stopped", "docker ps -a"},
		),
		page("find", knowledge.PlatformCommon,
			"Find files or directories under a directory tree.",
			[2]string{"find files larger than a given size", "find {{root}} -size +{{100M}}"},
		),
	}
}

func buildTestIndex(t *testing.T) *Reader {
	t.Helper()
	b := NewBuilder("test")
	for _, p := range corpus() {
		b.Add(p)
	}
	path := filepath.Join(t.TempDir(), "test.idx")
	if err := b.WriteTo(path); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r
}

func TestRoundTrip(t *testing.T) {
	r := buildTestIndex(t)
	st := r.Stats()
	if !st.Ready {
		t.Fatal("index is not ready")
	}
	if st.Pages != len(corpus()) {
		t.Errorf("pages = %d, want %d", st.Pages, len(corpus()))
	}
	if st.Release != "test" {
		t.Errorf("release = %q", st.Release)
	}

	p, ok, err := r.Lookup(context.Background(), "tar", []knowledge.Platform{knowledge.PlatformCommon})
	if err != nil || !ok {
		t.Fatalf("Lookup(tar) = ok %v, err %v", ok, err)
	}
	if p.Name != "tar" || len(p.Examples) != 3 {
		t.Errorf("page = %+v", p)
	}
	if p.Description == "" {
		t.Error("description was lost in the round trip")
	}
}

func TestLookupUnknownIsNotAnError(t *testing.T) {
	r := buildTestIndex(t)
	_, ok, err := r.Lookup(context.Background(), "definitely-not-a-command", nil)
	if err != nil {
		t.Errorf("err = %v, want nil: a missing page is a normal answer", err)
	}
	if ok {
		t.Error("ok = true for a command that is not there")
	}
}

func TestLookupIgnoresPathAndExtension(t *testing.T) {
	r := buildTestIndex(t)
	for _, name := range []string{"tar", "/usr/bin/tar", `C:\tools\tar.exe`} {
		if _, ok, _ := r.Lookup(context.Background(), name, nil); !ok {
			t.Errorf("Lookup(%q) did not find the page", name)
		}
	}
}

// searchCase is one entry in the retrieval benchmark: a question, and the
// command whose page should be in the top three.
type searchCase struct {
	question string
	wantPage string
}

// This is the E1 gate in miniature. The real one runs against the downloaded
// index with 200 questions; this runs on every build against a corpus small
// enough to reason about, and it exists because the first scorer put an
// obscure LZW tool above `tar` for "compress a folder to tar.gz".
var searchCases = []searchCase{
	{"compress a folder to tar.gz", "tar"},
	{"create a gzipped archive", "tar"},
	{"extract a tar.gz", "tar"},
	{"add a folder to a zip archive", "zip"},
	{"undo the last git commit", "git"},
	{"list docker containers", "docker"},
	{"find files larger than 100M", "find"},
}

func TestSearchTopThree(t *testing.T) {
	r := buildTestIndex(t)
	ctx := context.Background()

	hits := 0
	for _, tc := range searchCases {
		q := knowledge.ParseQuery(tc.question)
		results, err := r.Search(ctx, q, 3)
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.question, err)
		}
		found := false
		var got []string
		for _, h := range results {
			got = append(got, h.Page.Name)
			if h.Page.Name == tc.wantPage {
				found = true
			}
		}
		if found {
			hits++
		} else {
			t.Errorf("Search(%q) top-3 = %v, want %q among them", tc.question, got, tc.wantPage)
		}
	}
	t.Logf("top-3 hit rate: %d/%d", hits, len(searchCases))
}

// Every hit must carry a reason. A result the user cannot check is a result
// they have to verify themselves, which defeats the point.
func TestEverySearchHitExplainsItself(t *testing.T) {
	r := buildTestIndex(t)
	hits, err := r.Search(context.Background(), knowledge.ParseQuery("create a gzipped archive"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, h := range hits {
		if strings.TrimSpace(h.Reason) == "" {
			t.Errorf("hit %q has no reason", h.Command())
		}
		if h.Score <= 0 || h.Score > 1 {
			t.Errorf("hit %q has score %v, want 0 < s <= 1", h.Command(), h.Score)
		}
	}
}

// One page must not fill the result list. Five variations of the same command
// is not a set of options.
func TestResultsAreDiverse(t *testing.T) {
	r := buildTestIndex(t)
	hits, err := r.Search(context.Background(), knowledge.ParseQuery("archive a folder"), 6)
	if err != nil {
		t.Fatal(err)
	}
	perPage := map[string]int{}
	for _, h := range hits {
		perPage[h.Page.Name]++
	}
	for name, n := range perPage {
		if n > maxPerPage {
			t.Errorf("page %q produced %d of %d results, want at most %d", name, n, len(hits), maxPerPage)
		}
	}
}

// A damaged file must produce a message a user can act on, never a panic and
// never silent nonsense.
func TestDamagedIndexIsDetected(t *testing.T) {
	b := NewBuilder("test")
	for _, p := range corpus() {
		b.Add(p)
	}
	path := filepath.Join(t.TempDir(), "damaged.idx")
	if err := b.WriteTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a byte well past the header, inside a section.
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("Open accepted a corrupted index")
	}
	if !strings.Contains(err.Error(), "damaged") {
		t.Errorf("error = %v, want it to say the index is damaged", err)
	}
}

func TestNotAnIndexFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.idx")
	if err := os.WriteFile(path, []byte("this is not an index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a file that is not an index")
	}
}

func TestBuilderRefusesEmpty(t *testing.T) {
	b := NewBuilder("test")
	if err := b.WriteTo(filepath.Join(t.TempDir(), "empty.idx")); err == nil {
		t.Error("WriteTo wrote an index with no pages")
	}
}
