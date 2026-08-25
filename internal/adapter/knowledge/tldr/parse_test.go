package tldr

import (
	"testing"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

const tarPage = "# tar\n" +
	"\n" +
	"> Archiving utility.\n" +
	"> Often combined with a compression method, such as gzip or bzip2.\n" +
	"> More information: <https://www.gnu.org/software/tar>.\n" +
	"\n" +
	"- [c]reate an archive and write it to a [f]ile:\n" +
	"\n" +
	"`tar cf {{path/to/target.tar}} {{path/to/file1 path/to/file2 ...}}`\n" +
	"\n" +
	"- [c]reate a g[z]ipped archive and write it to a [f]ile:\n" +
	"\n" +
	"`tar czf {{path/to/target.tar.gz}} {{path/to/file1 path/to/file2 ...}}`\n" +
	"\n" +
	"- E[x]tract a (compressed) archive [f]ile into the current directory [v]erbosely:\n" +
	"\n" +
	"`tar xvf {{path/to/source.tar[.gz|.bz2|.xz]}}`\n"

func TestParsePage(t *testing.T) {
	p := ParsePage("tar", knowledge.PlatformCommon, tarPage)

	if p.Name != "tar" {
		t.Errorf("name = %q", p.Name)
	}
	if p.Platform != knowledge.PlatformCommon {
		t.Errorf("platform = %q", p.Platform)
	}
	want := "Archiving utility. Often combined with a compression method, such as gzip or bzip2."
	if p.Description != want {
		t.Errorf("description = %q, want %q", p.Description, want)
	}
	if p.MoreInfo != "https://www.gnu.org/software/tar" {
		t.Errorf("more info = %q", p.MoreInfo)
	}
	if len(p.Examples) != 3 {
		t.Fatalf("got %d examples, want 3", len(p.Examples))
	}
	if got := p.Examples[1].Command; got != "tar czf {{path/to/target.tar.gz}} {{path/to/file1 path/to/file2 ...}}" {
		t.Errorf("example 1 command = %q", got)
	}
	if got := p.Examples[1].Description; got != "[c]reate a g[z]ipped archive and write it to a [f]ile" {
		t.Errorf("example 1 description = %q", got)
	}
}

// The reference URL is not a description. Indexing it as one puts "https" and
// "www" into the term dictionary of thousands of pages.
func TestMoreInfoIsNotDescription(t *testing.T) {
	p := ParsePage("x", knowledge.PlatformCommon, "# x\n\n> Does a thing.\n> More information: <https://example.com>.\n")
	if p.Description != "Does a thing." {
		t.Errorf("description = %q", p.Description)
	}
	if p.MoreInfo != "https://example.com" {
		t.Errorf("more info = %q", p.MoreInfo)
	}
}

// Placeholders are kept exactly as written. Substituting something concrete
// would be WUT inventing an argument.
func TestPlaceholdersSurvive(t *testing.T) {
	p := ParsePage("cp", knowledge.PlatformCommon, "# cp\n\n> Copy.\n\n- Copy:\n\n`cp {{src}} {{dst}}`\n")
	if got := p.Examples[0].Command; got != "cp {{src}} {{dst}}" {
		t.Errorf("command = %q", got)
	}
}

func TestParseNeverPanics(t *testing.T) {
	inputs := []string{"", "#", "# ", ">", "- ", "`", "``", "# a\n> \n- \n`\n", "\n\n\n"}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParsePage(%q) panicked: %v", in, r)
				}
			}()
			_ = ParsePage("n", knowledge.PlatformCommon, in)
		}()
	}
}

func TestClassifyEntry(t *testing.T) {
	tests := []struct {
		entry    string
		wantOK   bool
		wantPlat knowledge.Platform
		wantName string
	}{
		{"pages/common/tar.md", true, knowledge.PlatformCommon, "tar"},
		{"tldr-main/pages/linux/apt.md", true, knowledge.PlatformLinux, "apt"},
		{"pages/windows/dir.md", true, knowledge.PlatformWindows, "dir"},
		{"pages/osx/brew.md", true, knowledge.PlatformOSX, "brew"},
		// Other languages are skipped: they would multiply the index with no
		// way to choose between them yet.
		{"pages.de/common/tar.md", false, "", ""},
		{"pages.zh/common/tar.md", false, "", ""},
		{"README.md", false, "", ""},
		{"pages/common/", false, "", ""},
		{"pages/nonsense/x.md", false, "", ""},
		{"../../etc/passwd.md", false, "", ""},
		{"LICENSE", false, "", ""},
	}
	for _, tc := range tests {
		plat, name, ok := classifyEntry(tc.entry)
		if ok != tc.wantOK {
			t.Errorf("classifyEntry(%q) ok = %v, want %v", tc.entry, ok, tc.wantOK)
			continue
		}
		if ok && (plat != tc.wantPlat || name != tc.wantName) {
			t.Errorf("classifyEntry(%q) = (%q, %q), want (%q, %q)", tc.entry, plat, name, tc.wantPlat, tc.wantName)
		}
	}
}
