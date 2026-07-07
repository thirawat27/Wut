package corrector

import (
	"testing"
)

func TestCheckDangerousDetectsRootDelete(t *testing.T) {
	c := New()
	corr, err := c.Correct("rm -rf /")
	if err != nil {
		t.Fatal(err)
	}
	if corr == nil || !corr.IsDangerous {
		t.Fatal("rm -rf / should be detected as dangerous")
	}
}

func TestCheckDangerousDetectsDiskOverwrite(t *testing.T) {
	c := New()
	corr, err := c.Correct("dd if=/dev/zero of=/dev/sda")
	if err != nil {
		t.Fatal(err)
	}
	if corr == nil || !corr.IsDangerous {
		t.Fatal("dd to /dev/sda should be detected as dangerous")
	}
}

func TestCheckDangerousPassesSafeCommands(t *testing.T) {
	c := New()
	corr, err := c.Correct("ls -la")
	if err != nil {
		t.Fatal(err)
	}
	if corr != nil && corr.IsDangerous {
		t.Fatal("ls -la should not be flagged as dangerous")
	}
}

func TestCorrectTypo(t *testing.T) {
	c := New()
	corr, err := c.Correct("gti status")
	if err != nil {
		t.Fatal(err)
	}
	if corr == nil {
		t.Fatal("gti status should trigger a correction")
	}
	if corr.Corrected != "git status" {
		t.Fatalf("expected 'git status', got %q", corr.Corrected)
	}
}

func TestCorrectRecognizesCorrectCommands(t *testing.T) {
	c := New()
	corr, err := c.Correct("git status")
	if err != nil {
		t.Fatal(err)
	}
	if corr != nil {
		t.Fatalf("expected no correction for 'git status', got %+v", corr)
	}
}

func TestSuggestAlternative(t *testing.T) {
	c := New()
	alts := c.SuggestAlternative("ls")
	if len(alts) == 0 {
		t.Fatal("expected alternatives for 'ls'")
	}
}

func TestCheckMissingPrefix(t *testing.T) {
	c := New()
	corr := c.checkMissingPrefix("status")
	if corr == nil {
		t.Fatal("'status' should be detected as missing 'git' prefix")
	}
	if corr.Corrected != "git status" {
		t.Fatalf("expected 'git status', got %q", corr.Corrected)
	}
}

func TestCorrectShortFlags(t *testing.T) {
	c := New()
	corr, err := c.Correct("docker ps -axz")
	if err != nil {
		t.Fatal(err)
	}
	if corr == nil {
		t.Fatal("docker ps -axz should trigger short flag correction")
	}
}

func TestConfidenceScore(t *testing.T) {
	s := confidenceScore("gti", 1)
	if s < 0.3 || s > 1.0 {
		t.Fatalf("confidenceScore should be between 0.3 and 1.0, got %f", s)
	}
}

func TestMaxDistForLen(t *testing.T) {
	tests := []struct {
		word string
		want int
	}{
		{"a", 1},
		{"ab", 1},
		{"abc", 1},
		{"abcd", 2},
		{"abcdef", 2},
		{"abcdefg", 3},
	}
	for _, tt := range tests {
		if got := maxDistForLen(tt.word); got != tt.want {
			t.Fatalf("maxDistForLen(%q) = %d, want %d", tt.word, got, tt.want)
		}
	}
}

func TestDangerousPatternsCoverKnownCases(t *testing.T) {
	c := New()
	dangerousCmds := []string{
		"rm -rf /",
		"rm -rf /*",
		"> /dev/sda",
		"mkfs.ext3 /dev/sda",
		"chmod -R 777 /",
	}
	for _, cmd := range dangerousCmds {
		corr, err := c.Correct(cmd)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", cmd, err)
		}
		if corr == nil || !corr.IsDangerous {
			t.Fatalf("%q should be detected as dangerous", cmd)
		}
	}
}

func TestPrecompiledRegexDetectsRootDelete(t *testing.T) {
	dangerousOnce.Do(initDangerousRegex)
	if dangerousRe == nil {
		t.Fatal("dangerousRe should be initialized")
	}
	if !dangerousRe.MatchString("rm -rf /") {
		t.Fatal("dangerousRe should match 'rm -rf /'")
	}
	if dangerousDiskRe == nil {
		t.Fatal("dangerousDiskRe should be initialized")
	}
	if !dangerousDiskRe.MatchString("dd if=/dev/zero > /dev/sda") {
		t.Fatal("dangerousDiskRe should match disk overwrite pattern")
	}
}
