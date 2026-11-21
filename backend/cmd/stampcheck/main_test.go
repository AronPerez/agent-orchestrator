package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const fixtureMain = `package main

var stamped = "default"
var unset string

func main() { println(stamped, unset) }
`

// fixtureTargets are cross-compiled on every host, so both readers are tested
// everywhere, including Windows, whose own PE format stampcheck does not read.
var fixtureTargets = []struct{ goos, goarch string }{
	{"linux", "amd64"},  // ELF
	{"darwin", "arm64"}, // Mach-O
}

// forEachTarget runs fn once per fixture target.
func forEachTarget(t *testing.T, fn func(t *testing.T, goos, goarch string)) {
	for _, target := range fixtureTargets {
		t.Run(target.goos+"_"+target.goarch, func(t *testing.T) { fn(t, target.goos, target.goarch) })
	}
}

// buildFixture cross-compiles a tiny program with the given -ldflags and
// returns the binary's path.
func buildFixture(t *testing.T, goos, goarch, ldflags string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.22\n", "main.go": fixtureMain} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "fixture")
	cmd := exec.Command(goBin, "build", "-ldflags", ldflags, "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, b)
	}
	return out
}

func readVars(t *testing.T, path string, names ...string) map[string]string {
	t.Helper()
	img, err := openImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img.close()
	got := map[string]string{}
	for _, name := range names {
		v, err := img.readString(name)
		if err != nil {
			t.Fatal(err)
		}
		got[name] = v
	}
	return got
}

func TestReadsStampedValues(t *testing.T) {
	forEachTarget(t, func(t *testing.T, goos, goarch string) {
		bin := buildFixture(t, goos, goarch, "-X main.stamped=client_PROBE -X main.unset=set_PROBE")
		got := readVars(t, bin, "main.stamped", "main.unset")
		if got["main.stamped"] != "client_PROBE" || got["main.unset"] != "set_PROBE" {
			t.Fatalf("got %v, want stamped=client_PROBE unset=set_PROBE", got)
		}
	})
}

// A misspelled -X target is silently ignored by the linker. The probe text is
// still in the binary (build info records -ldflags), which is why a byte search
// cannot catch this, but the variable keeps its default.
func TestMisspelledTargetReadsDefault(t *testing.T) {
	forEachTarget(t, func(t *testing.T, goos, goarch string) {
		bin := buildFixture(t, goos, goarch, "-X main.stampedTypo=client_PROBE")
		raw, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte("client_PROBE")) {
			t.Fatal("expected the probe in build info; this test's premise no longer holds")
		}
		got := readVars(t, bin, "main.stamped", "main.unset")
		if got["main.stamped"] != "default" || got["main.unset"] != "" {
			t.Fatalf("got %v, want stamped=default unset=\"\"", got)
		}
	})
}

func TestMissingSymbolIsAnError(t *testing.T) {
	forEachTarget(t, func(t *testing.T, goos, goarch string) {
		bin := buildFixture(t, goos, goarch, "")
		img, err := openImage(bin)
		if err != nil {
			t.Fatal(err)
		}
		defer img.close()
		if _, err := img.readString("main.renamed"); err == nil {
			t.Fatal("want an error for a symbol that does not exist")
		}
	})
}
