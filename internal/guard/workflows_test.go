package guard

// The workflow tests keep the GitHub Actions supply chain pinned: every
// third-party action is referenced by a full commit SHA with its release in a
// comment, PINS.md records each SHA with that release, and the workflows run
// containers only through scripts/ci.sh (whose images are pinned, see
// TestCIScriptImagesPinned).

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// usesLine matches a `uses:` key of a workflow step or job (a list item or a
// plain key), with an optional trailing comment.
var usesLine = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*["']?([^"'\s#]+)["']?\s*(?:#\s*(.*))?$`)

// shaRef is owner/repo[/path]@<40 hex>.
var shaRef = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)(/[A-Za-z0-9_./-]+)?@([0-9a-f]{40})$`)

// releaseComment is the version comment after a SHA: v1, v1.2 or v1.2.3.
var releaseComment = regexp.MustCompile(`^(v\d+(?:\.\d+){0,2})\b`)

// actionPin is one third-party action reference of a workflow.
type actionPin struct {
	repo, sha, version string
}

// checkUses returns the action of a `uses:` value and comment, or why it is
// not pinned. Local actions (./...) return an empty pin and no problem.
func checkUses(ref, comment string) (actionPin, string) {
	if strings.HasPrefix(ref, "./") {
		return actionPin{}, ""
	}
	if strings.HasPrefix(ref, "docker://") {
		if why := checkPinned(strings.TrimPrefix(ref, "docker://")); why != "" {
			return actionPin{}, "docker action " + why
		}
		return actionPin{}, ""
	}
	m := shaRef.FindStringSubmatch(ref)
	if m == nil {
		return actionPin{}, "not pinned by a full commit SHA (owner/repo@<40 hex>)"
	}
	v := releaseComment.FindStringSubmatch(strings.TrimSpace(comment))
	if v == nil {
		return actionPin{}, "SHA without its release in a comment (# vX.Y.Z)"
	}
	return actionPin{repo: m[1], sha: m[3], version: v[1]}, ""
}

func TestUsesParser(t *testing.T) {
	sha := strings.Repeat("a", 40)
	cases := []struct {
		line    string
		bad     bool
		repo    string
		version string
	}{
		{"      - uses: actions/checkout@" + sha + " # v7.0.1", false, "actions/checkout", "v7.0.1"},
		{"    uses: org/repo/.github/workflows/x.yml@" + sha + " # v2", false, "org/repo", "v2"},
		{"      - uses: github/codeql-action/init@" + sha + " # v4.39.1", false, "github/codeql-action", "v4.39.1"},
		{"      - uses: 'actions/checkout@" + sha + "' # v7.0.1", false, "actions/checkout", "v7.0.1"},
		{"      - uses: actions/checkout@v7", true, "", ""},
		{"      - uses: actions/checkout@main # v7.0.1", true, "", ""},
		{"      - uses: actions/checkout@" + sha[:39] + " # v7", true, "", ""},
		{"      - uses: actions/checkout@" + sha, true, "", ""},
		{"      - uses: actions/checkout@" + sha + " # latest", true, "", ""},
		{"      - uses: ./.github/actions/local", false, "", ""},
		{"      - uses: docker://alpine:3", true, "", ""},
		{"      - uses: docker://alpine:3@" + testDigest, false, "", ""},
	}
	for _, c := range cases {
		m := usesLine.FindStringSubmatch(c.line)
		if m == nil {
			t.Errorf("%q: not recognised as a uses line", c.line)
			continue
		}
		pin, why := checkUses(m[1], m[2])
		if (why != "") != c.bad {
			t.Errorf("%q: problem %q, want bad=%v", c.line, why, c.bad)
		}
		if pin.repo != c.repo || pin.version != c.version {
			t.Errorf("%q: got %+v, want %s %s", c.line, pin, c.repo, c.version)
		}
	}
	if usesLine.MatchString("      - name: uses: nothing") {
		t.Error("a name containing 'uses:' is not a uses line")
	}
}

// workflowFiles returns the workflow and composite action files, relative.
func workflowFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pattern := range []string{".github/workflows/*.yml", ".github/workflows/*.yaml", ".github/actions/*/action.yml", ".github/actions/*/action.yaml"} {
		m, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range m {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

// workflowPins returns every third-party action pin of the workflows; a
// reference that is not pinned is reported as an error.
func workflowPins(t *testing.T) []actionPin {
	t.Helper()
	files := workflowFiles(t)
	if len(files) == 0 {
		t.Fatal("no workflows found under .github/workflows")
	}
	var pins []actionPin
	for _, rel := range files {
		for n, line := range strings.Split(readRoot(t, rel), "\n") {
			m := usesLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			pin, why := checkUses(m[1], m[2])
			if why != "" {
				t.Errorf("%s:%d: %s: %s", rel, n+1, m[1], why)
				continue
			}
			if pin.repo != "" {
				pins = append(pins, pin)
			}
		}
	}
	return pins
}

func TestWorkflowActionsPinnedBySHA(t *testing.T) {
	pins := workflowPins(t)
	if len(pins) == 0 {
		t.Fatal("no third-party actions found")
	}
	// one repository, one SHA: two releases of the same action would make
	// Dependabot's grouped updates and PINS.md ambiguous
	seen := map[string]actionPin{}
	for _, p := range pins {
		if q, ok := seen[p.repo]; ok && (q.sha != p.sha || q.version != p.version) {
			t.Errorf("%s is pinned twice: %s (%s) and %s (%s)", p.repo, q.sha, q.version, p.sha, p.version)
		}
		seen[p.repo] = p
	}
}

// pinsActionRow is a row of PINS.md's actions table: | `repo` | `version` | `sha` |
var pinsActionRow = regexp.MustCompile("(?m)^\\| `([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)` \\| `(v[0-9.]+)` \\| `([0-9a-f]{40})` \\|")

func TestPinsDocumentListsEveryAction(t *testing.T) {
	doc := map[string]actionPin{}
	for _, m := range pinsActionRow.FindAllStringSubmatch(readRoot(t, "PINS.md"), -1) {
		doc[m[1]] = actionPin{repo: m[1], version: m[2], sha: m[3]}
	}
	used := map[string]bool{}
	for _, p := range workflowPins(t) {
		used[p.repo] = true
		if d, ok := doc[p.repo]; !ok {
			t.Errorf("PINS.md does not record the action %s (%s %s)", p.repo, p.version, p.sha)
		} else if d.sha != p.sha || d.version != p.version {
			t.Errorf("PINS.md records %s at %s (%s), the workflows use %s (%s)", p.repo, d.sha, d.version, p.sha, p.version)
		}
	}
	for repo := range doc {
		if !used[repo] {
			t.Errorf("PINS.md records the action %s, which no workflow uses any more", repo)
		}
	}
}

// TestWorkflowsRunContainersThroughCIScript: containers in workflows come
// from scripts/ci.sh (pinned images, the same commands locally); a job
// container or service must be pinned by digest.
func TestWorkflowsRunContainersThroughCIScript(t *testing.T) {
	image := regexp.MustCompile(`^\s*image:\s*["']?([^"'\s#]+)`)
	for _, rel := range workflowFiles(t) {
		for n, line := range strings.Split(readRoot(t, rel), "\n") {
			if strings.Contains(line, "docker run") {
				t.Errorf("%s:%d: docker run in a workflow; add the check to scripts/ci.sh instead", rel, n+1)
			}
			if m := image.FindStringSubmatch(line); m != nil {
				if why := checkPinned(m[1]); why != "" {
					t.Errorf("%s:%d: image %s: %s", rel, n+1, m[1], why)
				}
			}
		}
	}
}

// TestWorkflowsHardened: least privilege and bounded jobs. Every workflow
// sets top-level permissions, every job a timeout, and every checkout
// leaves no credentials behind.
func TestWorkflowsHardened(t *testing.T) {
	for _, rel := range workflowFiles(t) {
		if strings.HasPrefix(rel, ".github/actions/") {
			continue
		}
		src := readRoot(t, rel)
		if !regexp.MustCompile(`(?m)^permissions:`).MatchString(src) {
			t.Errorf("%s: no top-level permissions", rel)
		}
		at := strings.Index(src, "\njobs:\n")
		if at < 0 {
			t.Errorf("%s: no jobs", rel)
			continue
		}
		jobs := regexp.MustCompile(`(?m)^  [a-z][a-z0-9_-]*:\n`).FindAllStringIndex(src[at:], -1)
		timeouts := regexp.MustCompile(`(?m)^    timeout-minutes: \d+$`).FindAllString(src, -1)
		calls := regexp.MustCompile(`(?m)^    uses: `).FindAllString(src, -1) // reusable workflow jobs take no timeout
		if len(timeouts)+len(calls) != len(jobs) {
			t.Errorf("%s: %d jobs, %d with timeout-minutes and %d calling a reusable workflow", rel, len(jobs), len(timeouts), len(calls))
		}
		lines := strings.Split(src, "\n")
		for i, l := range lines {
			if !strings.Contains(l, "uses: actions/checkout@") {
				continue
			}
			block := strings.Join(lines[i+1:min(i+6, len(lines))], "\n")
			if !strings.Contains(block, "persist-credentials: false") {
				t.Errorf("%s:%d: checkout without persist-credentials: false", rel, i+1)
			}
		}
	}
}

// TestCIScriptExecutable: the workflows run scripts/ci.sh directly.
func TestCIScriptExecutable(t *testing.T) {
	for _, rel := range []string{"scripts/ci.sh", "scripts/go.sh"} {
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable", rel)
		}
	}
}

// TestPinsDocumentListsCosign: the cosign release the release workflow
// installs (verified by cosign-installer) is an exact version recorded in
// PINS.md.
func TestPinsDocumentListsCosign(t *testing.T) {
	re := regexp.MustCompile(`(?m)^\s*cosign-release:\s*(\S+)\s*$`)
	pins := readRoot(t, "PINS.md")
	found := 0
	for _, rel := range workflowFiles(t) {
		for _, m := range re.FindAllStringSubmatch(readRoot(t, rel), -1) {
			found++
			if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(m[1]) {
				t.Errorf("%s: cosign-release %s is not an exact release", rel, m[1])
			}
			if !strings.Contains(pins, "| cosign | `"+m[1]+"` |") {
				t.Errorf("PINS.md does not record cosign %s", m[1])
			}
		}
	}
	if strings.Contains(strings.Join(workflowFiles(t), " "), "release.yml") && found == 0 {
		t.Error("the release workflow installs cosign without a pinned cosign-release")
	}
}
