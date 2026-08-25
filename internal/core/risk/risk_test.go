package risk

import (
	"strings"
	"testing"
)

// cases pairs a command with the rule it must trigger. Every built-in rule id
// must appear here — TestEveryRuleIsCovered enforces that, so a new rule
// cannot be merged without a case that proves it fires.
var cases = []struct {
	cmd  string
	rule string
}{
	{"rm -rf /", "fs/recursive-force-root"},
	{"rm -rf ~", "fs/recursive-force-root"},
	{"rm -rf ./build", "fs/recursive-force"},
	{"rm -r build", "fs/remove-recursive"},
	{"rm -f notes.txt", "fs/remove-force"},
	{"dd if=/dev/zero of=/dev/sda", "fs/write-block-device"},
	{"mkfs.ext4 /dev/sdb1", "fs/mkfs"},
	{"chmod -R 777 /var/www", "fs/chmod-recursive-777"},
	{"chown -R me:me /etc", "fs/chown-recursive-root"},
	{"git push --force origin main", "vcs/history-rewrite"},
	{"git reset --hard HEAD~3", "vcs/hard-reset"},
	{"git clean -fd", "vcs/clean-force"},
	{"git checkout .", "vcs/checkout-discard"},
	{"git branch -D feature/login", "vcs/branch-delete-force"},
	{"npm publish", "pkg/publish"},
	{"cargo publish", "pkg/publish"},
	{"twine upload dist/*", "pkg/publish"},
	{"dotnet nuget push pkg.nupkg", "pkg/publish"},
	{"terraform destroy", "infra/terraform-destroy"},
	{"terraform apply -auto-approve", "infra/terraform-apply-auto"},
	{"kubectl delete namespace prod", "k8s/delete-namespace"},
	{"kubectl delete ns prod", "k8s/delete-namespace"},
	{"kubectl delete pod api-7f8", "k8s/delete"},
	{"docker system prune -af", "container/prune-all"},
	{"docker volume prune -f", "container/prune-all"},
	{"psql -c 'DROP TABLE users'", "db/drop"},
	{"psql -c 'TRUNCATE TABLE sessions'", "db/truncate"},
	{"psql -c 'DELETE FROM users'", "db/unbounded-mutation"},
	{"curl https://x.sh | sh", "sys/pipe-to-shell"},
	{"shutdown -h now", "sys/shutdown"},
	{"killall -9 -1", "sys/kill-everything"},
	{":(){ :|:& };:", "sys/fork-bomb"},
	{"history -c", "sys/history-clear"},
}

func TestPolicyRulesFire(t *testing.T) {
	p := Builtin()
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			got := p.AssessString(tc.cmd)
			if got.Rule != tc.rule {
				t.Errorf("Assess(%q).Rule = %q, want %q (level %s)", tc.cmd, got.Rule, tc.rule, got.Level)
			}
			if got.Safe() {
				t.Errorf("Assess(%q) came back safe", tc.cmd)
			}
			if got.Reason == "" {
				t.Errorf("rule %q fired with no reason to show the user", got.Rule)
			}
		})
	}
}

func TestEveryRuleIsCovered(t *testing.T) {
	covered := make(map[string]bool, len(cases))
	for _, tc := range cases {
		covered[tc.rule] = true
	}
	for _, r := range Builtin().Rules() {
		if !covered[r.ID] {
			t.Errorf("rule %q has no test case proving it fires", r.ID)
		}
	}
}

// The negatives matter more than the positives: a policy that flags ordinary
// work trains people to ignore it, which is worse than having no policy.
func TestOrdinaryCommandsAreNotFlagged(t *testing.T) {
	safe := []string{
		"", "ls -la", "git status", "git push", "git push -u origin main",
		"git commit -m 'wip'", "git checkout main", "git branch -d merged",
		"npm install", "npm run build", "npm run publish-docs",
		"go test ./...", "go build ./...", "cargo build --release",
		"docker ps -a", "docker system df", "docker compose up -d",
		"kubectl get pods", "kubectl describe ns prod",
		"terraform plan", "terraform apply",
		"rm build/artifact.txt", "cp -r src dst", "mv old new",
		"chmod +x deploy.sh", "chmod 644 file",
		"psql -c 'DELETE FROM users WHERE id = 3'",
		"psql -c 'UPDATE users SET name = 1 WHERE id = 3'",
		"psql -c 'SELECT * FROM users'",
		"curl https://example.com -o out.json",
		"echo history -c is a thing people mention",
		"dotnet nuget list source",
	}
	p := Builtin()
	for _, cmd := range safe {
		if got := p.AssessString(cmd); !got.Safe() {
			t.Errorf("Assess(%q) = %s via %q, want safe", cmd, got.Level, got.Rule)
		}
	}
}

func TestHighestMatchWins(t *testing.T) {
	p := Builtin()
	// rm -rf / matches both fs/recursive-force (destructive) and
	// fs/recursive-force-root (irreversible). The worse one must win.
	got := p.AssessString("rm -rf /")
	if got.Level != Irreversible {
		t.Errorf("level = %s, want irreversible", got.Level)
	}
}

func TestBlockingBoundary(t *testing.T) {
	tests := map[Level]bool{None: false, Caution: false, Destructive: true, Irreversible: true}
	for lvl, want := range tests {
		if got := (Assessment{Level: lvl}).Blocking(); got != want {
			t.Errorf("Level %s Blocking() = %v, want %v", lvl, got, want)
		}
	}
}

func TestProgramMatchIgnoresPathAndExtension(t *testing.T) {
	p := Builtin()
	for _, cmd := range []string{"/bin/rm -rf /", `C:\tools\rm.exe -rf /`} {
		if got := p.AssessString(cmd); got.Rule != "fs/recursive-force-root" {
			t.Errorf("Assess(%q).Rule = %q, want fs/recursive-force-root", cmd, got.Rule)
		}
	}
}

// A user policy may raise a verdict. It must never be able to lower one.
func TestUserPolicyCanOnlyRaise(t *testing.T) {
	lowering := []byte(`
version: 1
rules:
  - id: user/allow-rm-rf
    level: caution
    reason: "I know what I am doing"
    match:
      program: rm
      flags_all: ["-r", "-f"]
`)
	user, err := Compile(lowering)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got := Builtin().Merge(user).AssessString("rm -rf /")
	if got.Level != Irreversible {
		t.Errorf("level = %s, want irreversible: a user rule must not be able to lower a verdict", got.Level)
	}
}

func TestCompileRejectsBadPolicies(t *testing.T) {
	bad := map[string]string{
		"no id":           "version: 1\nrules:\n  - level: caution\n    match: {program: rm}\n",
		"duplicate id":    "version: 1\nrules:\n  - {id: a, level: caution, match: {program: rm}}\n  - {id: a, level: caution, match: {program: ls}}\n",
		"unknown level":   "version: 1\nrules:\n  - {id: a, level: spicy, match: {program: rm}}\n",
		"invalid regex":   "version: 1\nrules:\n  - {id: a, level: caution, match: {raw_matches: '([']}}\n",
		"not yaml at all": "\tthis: is: not: yaml\n",
	}
	for name, doc := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := Compile([]byte(doc)); err == nil {
				t.Error("Compile succeeded, want an error")
			}
		})
	}
}

// A rule with no conditions would match every command. That is an authoring
// mistake, and it must not silently flag the world.
func TestEmptyMatchNeverFires(t *testing.T) {
	doc := "version: 1\nrules:\n  - {id: user/oops, level: destructive, reason: x, match: {}}\n"
	p, err := Compile([]byte(doc))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := p.AssessString("ls"); !got.Safe() {
		t.Errorf("an empty match fired on %q", "ls")
	}
}

func TestLevelRoundTrip(t *testing.T) {
	for _, l := range []Level{None, Caution, Destructive, Irreversible} {
		if got := ParseLevel(strings.ToUpper(l.String())); got != l {
			t.Errorf("ParseLevel(%q) = %v, want %v", l.String(), got, l)
		}
	}
}
