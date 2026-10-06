package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildMatchesVolumesSupport checks that a spec equivalent to
// contemper's volumes-support images, built from copies of its files,
// produces exactly the digests contemper's own builder produced for them
// (testdata/volumes-support.digests says how those were generated).
func TestBuildMatchesVolumesSupport(t *testing.T) {
	opts := useRegistry(t)
	want := readDigests(t, "testdata/volumes-support.digests")

	for _, tc := range []struct{ block, revision string }{
		{"no revision", ""},
		{"revision", "0123456789abcdef0123456789abcdef01234567"},
	} {
		t.Run(tc.block, func(t *testing.T) {
			args := []string{"-prefix", "localhost:5555/contemper-e2e", "-image-dir", "testdata/volumes-support", "-tag", "v1"}
			if tc.revision != "" {
				args = append(args, "-revision", tc.revision)
			}
			digestsFile := filepath.Join(t.TempDir(), "digests.txt")
			args = append(args, "-digests-file", digestsFile)
			if err := run(args, io.Discard); err != nil {
				t.Fatal(err)
			}
			got := readDigests(t, digestsFile)[""]
			if len(got) != 3 {
				t.Fatalf("built %d images, want 3: %v", len(got), got)
			}
			for name, digest := range want[tc.block] {
				if got[name] != digest {
					t.Errorf("%s: digest = %s, want %s", name, got[name], digest)
				}
				// And what a registry serves under the tag is that digest.
				idx := pulledIndex(t, name+":v1", opts)
				d, err := idx.Digest()
				if err != nil {
					t.Fatal(err)
				}
				if d.String() != digest {
					t.Errorf("%s: pulled digest = %s, want %s", name, d, digest)
				}
			}
		})
	}
}

// readDigests parses a digests file: "name digest" lines, grouped under
// optional "[block]" headings (keyed by block name, "" before the first),
// with "#" comment lines ignored.
func readDigests(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // read-only test file
	out := map[string]map[string]string{"": {}}
	block := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			block = strings.Trim(line, "[]")
			out[block] = map[string]string{}
		default:
			fields := strings.Fields(line)
			if len(fields) != 2 {
				t.Fatalf("%s: malformed line %q", path, line)
			}
			out[block][fields[0]] = fields[1]
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
