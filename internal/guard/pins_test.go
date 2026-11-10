package guard

// The pin tests keep the build reproducible: every container image is pinned
// by digest (the tag stays next to it for humans), the Tailwind CLI is checked
// against a per-architecture sha256, and the Go toolchain is one exact patch
// version everywhere. scripts/update-pins.sh refreshes the pins; PINS.md
// records them.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readRoot returns the file rel (relative to the repository root).
func readRoot(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pinnedRef is name:tag@sha256:<64 hex>. The registry part may carry a port
// (host:5000/name:tag@...), so the tag is looked for after the last slash.
var pinnedRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

// checkPinned returns why ref is not pinned by digest with its tag kept, or "".
func checkPinned(ref string) string {
	if !pinnedRef.MatchString(ref) {
		return "not pinned by @sha256: digest"
	}
	name := ref[:strings.Index(ref, "@")]
	if !strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") {
		return "digest without the tag (keep name:tag@sha256:... so humans see the version)"
	}
	return ""
}

// fromInstr is one FROM of a Dockerfile.
type fromInstr struct {
	line  int    // 1-based line of the FROM
	image string // the image reference after ARG expansion
	stage string // the AS name, lower case, or ""
}

// logicalLines joins continuation lines (trailing backslash) and drops
// comment lines (also those inside a continuation, as Docker does). Each
// result carries the line number where it starts.
func logicalLines(src string) (lines []string, starts []int) {
	var cur strings.Builder
	start := 0
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cur.Len() == 0 {
			if trimmed == "" {
				continue
			}
			start = i + 1
		}
		if strings.HasSuffix(trimmed, `\`) {
			cur.WriteString(strings.TrimSuffix(trimmed, `\`))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(trimmed)
		lines = append(lines, cur.String())
		starts = append(starts, start)
		cur.Reset()
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
		starts = append(starts, start)
	}
	return lines, starts
}

// argRef matches $NAME, ${NAME} and ${NAME:-default}.
var argRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandArgs replaces ARG references by their values; an unknown ARG without
// a default is an error (the image could be anything at build time).
func expandArgs(s string, args map[string]string) (string, error) {
	var bad error
	out := argRef.ReplaceAllStringFunc(s, func(m string) string {
		sm := argRef.FindStringSubmatch(m)
		name := sm[1] + sm[3]
		if v, ok := args[name]; ok && v != "" {
			return v
		}
		if strings.Contains(m, ":-") {
			return sm[2]
		}
		bad = fmt.Errorf("ARG %s has no value", name)
		return m
	})
	return out, bad
}

// parseFroms returns every FROM of a Dockerfile, with ARGs declared before
// the first FROM expanded (the only ARGs a FROM can see). References to an
// earlier stage (FROM build) and scratch are left out: they pull nothing.
func parseFroms(src string) ([]fromInstr, error) {
	lines, starts := logicalLines(src)
	args := map[string]string{}
	stages := map[string]bool{}
	var out []fromInstr
	seenFrom := false
	for i, l := range lines {
		fields := strings.Fields(l)
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			if seenFrom {
				continue
			}
			for _, f := range fields[1:] {
				name, val, _ := strings.Cut(f, "=")
				args[name] = strings.Trim(val, `"'`)
			}
		case "FROM":
			seenFrom = true
			rest := fields[1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
				if !strings.Contains(rest[0], "=") && len(rest) > 1 {
					rest = rest[1:] // --platform linux/amd64
				}
				rest = rest[1:]
			}
			if len(rest) == 0 {
				return nil, fmt.Errorf("line %d: FROM without an image", starts[i])
			}
			img, err := expandArgs(rest[0], args)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", starts[i], err)
			}
			stage := ""
			if len(rest) >= 3 && strings.EqualFold(rest[1], "AS") {
				stage = strings.ToLower(rest[2])
			}
			if !stages[strings.ToLower(img)] && img != "scratch" {
				out = append(out, fromInstr{line: starts[i], image: img, stage: stage})
			}
			if stage != "" {
				stages[stage] = true
			}
		}
	}
	return out, nil
}

// unpinnedFroms lists the offending FROMs of a Dockerfile.
func unpinnedFroms(src string) ([]string, error) {
	froms, err := parseFroms(src)
	if err != nil {
		return nil, err
	}
	if len(froms) == 0 {
		return nil, fmt.Errorf("no FROM found")
	}
	var bad []string
	for _, f := range froms {
		if why := checkPinned(f.image); why != "" {
			bad = append(bad, fmt.Sprintf("line %d: %s: %s", f.line, f.image, why))
		}
	}
	return bad, nil
}

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestDockerfileParserCatchesUnpinnedImages keeps the parser honest on the
// forms the real Dockerfile might grow into.
func TestDockerfileParserCatchesUnpinnedImages(t *testing.T) {
	cases := []struct {
		name, src string
		bad       int
	}{
		{"plain tag", "FROM golang:1.23\n", 1},
		{"no tag", "FROM debian\n", 1},
		{"digest without tag", "FROM debian@" + testDigest + "\n", 1},
		{"short digest", "FROM debian:12@sha256:0123\n", 1},
		{"pinned", "FROM debian:12@" + testDigest + "\n", 0},
		{"lower case and AS", "from golang:1.23 as build\n", 1},
		{"platform flag", "FROM --platform=$BUILDPLATFORM golang:1.23 AS build\n", 1},
		{"platform flag pinned", "FROM --platform=$BUILDPLATFORM golang:1.23@" + testDigest + " AS build\n", 0},
		{"platform flag spaced", "FROM --platform linux/amd64 alpine:3\n", 1},
		{"registry with port", "FROM reg.example:5000/team/img@" + testDigest + "\n", 1},
		{"registry with port pinned", "FROM reg.example:5000/team/img:1@" + testDigest + "\n", 0},
		{"later stage unpinned", "FROM a:1@" + testDigest + " AS one\nRUN x\nFROM b:2\n", 1},
		{"stage reference", "FROM a:1@" + testDigest + " AS Base\nFROM base\nCOPY --from=base / /\n", 0},
		{"scratch", "FROM a:1@" + testDigest + " AS b\nFROM scratch\n", 0},
		{"arg image unpinned", "ARG IMG=golang:1.23\nFROM ${IMG}\n", 1},
		{"arg image pinned", "ARG IMG=golang:1.23@" + testDigest + "\nFROM $IMG AS x\n", 0},
		{"arg default", "FROM ${IMG:-alpine:3}\n", 1},
		{"continuation", "FROM \\\n  alpine:3 \\\n  AS x\n", 1},
		{"comment ignored", "# FROM alpine:3\nFROM a:1@" + testDigest + "\n", 0},
	}
	for _, c := range cases {
		bad, err := unpinnedFroms(c.src)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(bad) != c.bad {
			t.Errorf("%s: %d offending FROMs %v, want %d", c.name, len(bad), bad, c.bad)
		}
	}
	if _, err := unpinnedFroms("ARG X\nFROM $X\n"); err == nil {
		t.Error("ARG without value accepted as a FROM image")
	}
	if froms, _ := parseFroms("ARG V=1\nFROM a:${V}@" + testDigest + "\nARG V=2\nFROM b:$V@" + testDigest + "\n"); len(froms) != 2 ||
		froms[0].image != "a:1@"+testDigest || froms[1].image != "b:1@"+testDigest {
		t.Errorf("ARG after the first FROM must not change FROM images: %+v", froms)
	}
}

func TestDockerfilePinsEveryFromByDigest(t *testing.T) {
	src := readRoot(t, "Dockerfile")
	bad, err := unpinnedFroms(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Error("Dockerfile " + b)
	}
	froms, _ := parseFroms(src)
	if len(froms) < 3 {
		t.Errorf("expected the css, build and runtime images, got %+v", froms)
	}
	// scripts/go.sh must run the same pinned golang image as the build stage
	goImage := ""
	for _, f := range froms {
		if strings.HasPrefix(f.image, "golang:") {
			goImage = f.image
		}
	}
	if goImage == "" {
		t.Fatal("no golang stage in the Dockerfile")
	}
	if goSh := readRoot(t, "scripts/go.sh"); !strings.Contains(goSh, " "+goImage+" ") {
		t.Errorf("scripts/go.sh does not run %s", goImage)
	}
}

// tailwindArgs are the checksum ARGs of the css stage, per Docker TARGETARCH.
var tailwindArgs = map[string]string{
	"amd64": "TAILWIND_SHA256_AMD64",
	"arm64": "TAILWIND_SHA256_ARM64",
}

func TestDockerfileVerifiesTailwindChecksum(t *testing.T) {
	lines, _ := logicalLines(readRoot(t, "Dockerfile"))
	values := map[string]string{}
	var download string
	for _, l := range lines {
		fields := strings.Fields(l)
		if strings.EqualFold(fields[0], "ARG") && len(fields) == 2 {
			name, val, _ := strings.Cut(fields[1], "=")
			values[name] = val
		}
		if strings.EqualFold(fields[0], "RUN") && strings.Contains(l, "tailwindcss-linux-") {
			download = l
		}
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(values["TAILWIND_VERSION"]) {
		t.Errorf("TAILWIND_VERSION %q is not an exact release", values["TAILWIND_VERSION"])
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for arch, name := range tailwindArgs {
		if !hex64.MatchString(values[name]) {
			t.Errorf("ARG %s (%s) = %q: want a sha256", name, arch, values[name])
		}
		if !strings.Contains(download, "${"+name+"}") {
			t.Errorf("the Tailwind download step does not use ${%s}", name)
		}
	}
	if values[tailwindArgs["amd64"]] == values[tailwindArgs["arm64"]] {
		t.Error("both architectures have the same checksum")
	}
	if download == "" {
		t.Fatal("no Tailwind download step")
	}
	if !strings.Contains(download, "sha256sum -c") {
		t.Error("the Tailwind download step does not run sha256sum -c")
	}
	// the binary is only made executable after the check (inside one RUN with &&)
	check, chmod := strings.Index(download, "sha256sum -c"), strings.Index(download, "chmod +x")
	if chmod < check {
		t.Error("chmod +x runs before the checksum check")
	}
	// an unknown architecture must fail instead of skipping the check
	if !strings.Contains(download, "exit 1") {
		t.Error("the Tailwind step does not fail on an unsupported architecture")
	}
}

var (
	goLine        = regexp.MustCompile(`(?m)^go (\d+\.\d+\.\d+)$`)
	toolchainLine = regexp.MustCompile(`(?m)^toolchain go(\d+\.\d+\.\d+)$`)
	goImageTag    = regexp.MustCompile(`golang:(\d+\.\d+\.\d+)(?:-[a-z0-9]+)?@sha256:[0-9a-f]{64}`)
)

func TestGoVersionPinnedExactly(t *testing.T) {
	mod := readRoot(t, "go.mod")
	g := goLine.FindStringSubmatch(mod)
	tc := toolchainLine.FindStringSubmatch(mod)
	if g == nil {
		t.Fatal("go.mod: the go line is not an exact patch version (go X.Y.Z)")
	}
	if tc == nil {
		t.Fatal("go.mod: no toolchain goX.Y.Z line")
	}
	// the go line is the language minimum (go mod tidy drops a toolchain
	// line equal to it, so it is X.Y.0); it must stay on the toolchain's minor
	minor := func(v string) string { return v[:strings.LastIndex(v, ".")] }
	if minor(g[1]) != minor(tc[1]) {
		t.Errorf("go.mod: go %s and toolchain go%s are different minor versions", g[1], tc[1])
	}
	for _, rel := range []string{"Dockerfile", "scripts/go.sh"} {
		m := goImageTag.FindAllStringSubmatch(readRoot(t, rel), -1)
		if len(m) != 1 {
			t.Errorf("%s: want exactly one pinned golang:X.Y.Z image, found %d", rel, len(m))
			continue
		}
		if m[0][1] != tc[1] {
			t.Errorf("%s uses golang %s, go.mod toolchain is go%s", rel, m[0][1], tc[1])
		}
	}
}

// dockerRunImage returns the image of a `docker run` command line: the first
// argument that is not an option or an option's value.
func dockerRunImage(cmd string) string {
	fields := strings.Fields(cmd)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] != "docker" || fields[i+1] != "run" {
			continue
		}
		// options of docker run that take a separate value
		withValue := map[string]bool{"-v": true, "--volume": true, "-e": true, "--env": true, "-w": true,
			"--workdir": true, "--user": true, "-u": true, "--name": true, "-p": true, "--publish": true,
			"--network": true, "--entrypoint": true, "--platform": true, "--mount": true}
		for j := i + 2; j < len(fields); j++ {
			f := fields[j]
			if strings.HasPrefix(f, "-") {
				if withValue[f] {
					j++
				}
				continue
			}
			return strings.Trim(f, "`")
		}
	}
	return ""
}

func TestDockerRunImageParser(t *testing.T) {
	for cmd, want := range map[string]string{
		`docker run --rm -v vol:/data -v "$PWD":/backup alpine tar czf x`:                 "alpine",
		`docker run --rm --user root -v a:/b keinos/sqlite3:3.54.1 sqlite3 /data/x.db`:    "keinos/sqlite3:3.54.1",
		`exec docker run --rm -w /src -e CGO_ENABLED=0 golang:1.23@` + testDigest + ` go`: "golang:1.23@" + testDigest,
		`docker compose up -d`: "",
	} {
		if got := dockerRunImage(cmd); got != want {
			t.Errorf("%s: got %q, want %q", cmd, got, want)
		}
	}
}

// TestTailwindSumsMatchPinsDocByArchitecture: each Dockerfile Tailwind
// checksum is the one PINS.md records for that same architecture — the
// containment check above cannot catch a swapped pair (both sums would
// still appear in both files).
func TestTailwindSumsMatchPinsDocByArchitecture(t *testing.T) {
	arg := regexp.MustCompile(`(?m)^ARG (TAILWIND_SHA256_(AMD64|ARM64))=([0-9a-f]{64})$`)
	args := map[string]string{}
	for _, m := range arg.FindAllStringSubmatch(readRoot(t, "Dockerfile"), -1) {
		args[m[2]] = m[3]
	}
	row := regexp.MustCompile(`(?m)^\| (amd64|arm64) \| ` + "`" + `tailwindcss-linux-(x64|arm64)` + "`" + ` \| ` + "`" + `([0-9a-f]{64})` + "`" + ` \|`)
	doc, asset := map[string]string{}, map[string]string{}
	for _, m := range row.FindAllStringSubmatch(readRoot(t, "PINS.md"), -1) {
		doc[m[1]] = m[3]
		asset[m[1]] = m[2]
	}
	if len(args) != 2 || len(doc) != 2 {
		t.Fatalf("tailwind pins: %d ARGs, %d PINS.md rows (want 2 each)", len(args), len(doc))
	}
	if asset["amd64"] != "x64" || asset["arm64"] != "arm64" {
		t.Fatalf("PINS.md rows name the wrong assets: %v", asset)
	}
	if doc["amd64"] == doc["arm64"] {
		t.Fatal("both architectures record the same checksum")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if a := args[strings.ToUpper(arch)]; a == "" || a != doc[arch] {
			t.Errorf("%s: Dockerfile ARG %q does not match PINS.md %q", arch, a, doc[arch])
		}
	}
}

func TestReadmeHelperImagesPinned(t *testing.T) {
	seen := map[string]bool{}
	for n, line := range strings.Split(readRoot(t, "README.md"), "\n") {
		img := dockerRunImage(line)
		if img == "" {
			continue
		}
		if why := checkPinned(img); why != "" {
			t.Errorf("README.md:%d: helper image %s: %s", n+1, img, why)
		}
		seen[img[:strings.IndexAny(img+":", ":@")]] = true
	}
	for _, want := range []string{"alpine", "keinos/sqlite3"} {
		if !seen[want] {
			t.Errorf("README.md: no docker run with %s found", want)
		}
	}
	if !strings.Contains(readRoot(t, "README.md"), "scripts/update-pins.sh") {
		t.Error("README.md does not tell how to update the pins (scripts/update-pins.sh)")
	}
}

// digestOrSum matches every pinned digest and 64-hex checksum.
var digestOrSum = regexp.MustCompile(`[0-9a-f]{64}`)

// TestPinsDocumentListsEveryPin: PINS.md records every digest and checksum in
// use, so a refresh that forgets the record (or the other way round) fails.
func TestPinsDocumentListsEveryPin(t *testing.T) {
	pins := readRoot(t, "PINS.md")
	for _, rel := range []string{"Dockerfile", "scripts/go.sh", "README.md", "internal/web/static/htmx.min.js.sha256", "internal/web/static/material-symbols-outlined-v383.woff2.sha256", "internal/web/testdata/material-symbols-outlined.codepoints.sha256"} {
		found := digestOrSum.FindAllString(readRoot(t, rel), -1)
		if len(found) == 0 {
			t.Errorf("%s: no pinned digest or checksum found", rel)
		}
		for _, d := range found {
			if !strings.Contains(pins, d) {
				t.Errorf("PINS.md does not record %s from %s", d, rel)
			}
		}
	}
	for _, d := range digestOrSum.FindAllString(pins, -1) {
		used := false
		for _, rel := range []string{"Dockerfile", "scripts/go.sh", "README.md", "internal/web/static/htmx.min.js.sha256", "internal/web/static/material-symbols-outlined-v383.woff2.sha256", "internal/web/testdata/material-symbols-outlined.codepoints.sha256"} {
			if strings.Contains(readRoot(t, rel), d) {
				used = true
			}
		}
		if !used {
			t.Errorf("PINS.md records %s, which nothing uses any more", d)
		}
	}
}
