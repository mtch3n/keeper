package plugintest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const fixtureBinary = "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"client   9.9.9\"; exit 0; fi\necho \"fixture keeper $*\"\n"

type launcherEnv struct {
	home, bin, release string
	env                []string
}

// newLauncherEnv builds a world with no keeper on PATH: a fake curl that serves
// files from a release folder, a fake uname, and an empty HOME.
func newLauncherEnv(t *testing.T) *launcherEnv {
	t.Helper()
	root := t.TempDir()
	e := &launcherEnv{
		home:    filepath.Join(root, "home"),
		bin:     filepath.Join(root, "bin"),
		release: filepath.Join(root, "release"),
	}
	for _, d := range []string{e.home, e.bin, e.release} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	curl := `#!/bin/sh
out=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
[ -n "$FAKE_CURL_FAIL" ] && { echo "curl: (6) Could not resolve host" >&2; exit 6; }
sleep "${FAKE_CURL_SLEEP:-0}"
case "$url" in
  https://github.com/mtch3n/keeper/releases/latest/download/*) ;;
  *) echo "unexpected url $url" >&2; exit 22 ;;
esac
cp "$FAKE_RELEASE/${url##*/}" "$out"
`
	uname := `#!/bin/sh
case "$1" in -s) echo "${FAKE_OS:-Linux}" ;; -m) echo "${FAKE_ARCH:-x86_64}" ;; esac
`
	for name, body := range map[string]string{"curl": curl, "uname": uname} {
		if err := os.WriteFile(filepath.Join(e.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.env = []string{
		"HOME=" + e.home,
		"PATH=" + e.bin + ":/usr/bin:/bin",
		"TMPDIR=" + root,
		"FAKE_RELEASE=" + e.release,
	}
	return e
}

// publish writes a release the way the workflow does: archives named
// keeper_<version>_<os>_<arch>.tar.gz holding one folder, and SHA256SUMS over
// "./name" paths. corrupt lists a digest to write instead of the real one.
func (e *launcherEnv) publish(t *testing.T, corrupt bool) {
	t.Helper()
	var sums strings.Builder
	for _, target := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		dir := "keeper_9.9.9_" + target
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		must(t, tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755}))
		must(t, tw.WriteHeader(&tar.Header{Name: dir + "/keeper", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(fixtureBinary))}))
		_, err := tw.Write([]byte(fixtureBinary))
		must(t, err)
		must(t, tw.Close())
		must(t, zw.Close())
		sum := sha256.Sum256(buf.Bytes())
		digest := hex.EncodeToString(sum[:])
		if corrupt {
			digest = strings.Repeat("0", 64)
		}
		fmt.Fprintf(&sums, "%s  ./%s.tar.gz\n", digest, dir)
		must(t, os.WriteFile(filepath.Join(e.release, dir+".tar.gz"), buf.Bytes(), 0o644))
	}
	must(t, os.WriteFile(filepath.Join(e.release, "SHA256SUMS"), []byte(sums.String()), 0o644))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *launcherEnv) run(extra ...string) (stdout, stderr string, err error) {
	cmd := exec.Command("sh", launcherPath(), "version")
	cmd.Env = append(append([]string{}, e.env...), extra...)
	var o, s bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &s
	err = cmd.Run()
	return o.String(), s.String(), err
}

func (e *launcherEnv) installed() string {
	return filepath.Join(e.home, ".local", "bin", "keeper")
}

func launcherPath() string {
	p, _ := filepath.Abs("../../plugin/bin/keeper-mcp-launch")
	return p
}

func TestPlg_PLG_C4_LauncherInstallsAndRunsTheRelease(t *testing.T) {
	e := newLauncherEnv(t)
	e.publish(t, false)
	out, errOut, err := e.run()
	if err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, errOut)
	}
	if out != "fixture keeper mcp version\n" {
		t.Errorf("stdout %q: the installed keeper did not run as `keeper mcp`", out)
	}
	got, readErr := os.ReadFile(e.installed())
	if readErr != nil || string(got) != fixtureBinary {
		t.Errorf("installed binary: %q, %v", got, readErr)
	}
	if info, _ := os.Stat(e.installed()); info == nil || info.Mode().Perm()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}

	e.env = append(e.env, "FAKE_CURL_FAIL=1")
	if out, _, err = e.run(); err != nil || out != "fixture keeper mcp version\n" {
		t.Errorf("second start must use the installed binary without the network: %q, %v", out, err)
	}
}

func TestPlg_PLG_C5_LauncherStopsWithAReasonAndRunsNothing(t *testing.T) {
	cases := []struct {
		name    string
		corrupt bool
		env     []string
		reason  string
	}{
		{"checksum mismatch", true, nil, "checksum mismatch"},
		{"unsupported os", false, []string{"FAKE_OS=FreeBSD"}, "unsupported operating system"},
		{"unsupported arch", false, []string{"FAKE_ARCH=riscv64"}, "unsupported architecture"},
		{"no network", false, []string{"FAKE_CURL_FAIL=1"}, "cannot download"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newLauncherEnv(t)
			e.publish(t, c.corrupt)
			out, errOut, err := e.run(c.env...)
			if err == nil {
				t.Fatal("want a non-zero exit")
			}
			if !strings.Contains(errOut, c.reason) {
				t.Errorf("stderr %q does not give the reason %q", errOut, c.reason)
			}
			if out != "" {
				t.Errorf("stdout %q: something ran", out)
			}
			if _, statErr := os.Stat(e.installed()); statErr == nil {
				t.Error("a binary was installed")
			}
		})
	}
}

func TestPlg_PLG_C6_TwoLaunchersAtOnceLeaveOneGoodBinary(t *testing.T) {
	e := newLauncherEnv(t)
	e.publish(t, false)
	var wg sync.WaitGroup
	results := make([]error, 4)
	outs := make([]string, 4)
	for i := range results {
		wg.Go(func() {
			outs[i], _, results[i] = e.run("FAKE_CURL_SLEEP=0.3")
		})
	}
	wg.Wait()
	for i, err := range results {
		if err != nil || outs[i] != "fixture keeper mcp version\n" {
			t.Errorf("launcher %d: %q, %v", i, outs[i], err)
		}
	}
	got, err := os.ReadFile(e.installed())
	if err != nil || string(got) != fixtureBinary {
		t.Errorf("installed binary: %q, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(e.installed()))
	if len(entries) != 1 {
		t.Errorf("install folder holds %d entries, want only keeper", len(entries))
	}
}

func TestPlg_LauncherRefusesAKeeperOlderThanTheManifestNeeds(t *testing.T) {
	const oldKeeper = "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"client   0.0.1\"; exit 0; fi\necho \"keeper: unknown command $1\" >&2\nexit 2\n"
	cases := []struct {
		name string
		path func(e *launcherEnv) string
	}{
		{"on PATH", func(e *launcherEnv) string { return filepath.Join(e.bin, "keeper") }},
		{"in ~/.local/bin", func(e *launcherEnv) string { return e.installed() }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newLauncherEnv(t)
			e.publish(t, false)
			must(t, os.MkdirAll(filepath.Dir(c.path(e)), 0o755))
			must(t, os.WriteFile(c.path(e), []byte(oldKeeper), 0o755))
			out, errOut, err := e.run()
			if err == nil {
				t.Fatal("want a non-zero exit")
			}
			if out != "" || strings.Contains(errOut, "unknown command") {
				t.Errorf("the old keeper ran: stdout %q, stderr %q", out, errOut)
			}
			for _, want := range []string{"0.0.1", "keeper update"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr %q does not mention %q", errOut, want)
				}
			}
		})
	}

	e := newLauncherEnv(t)
	e.publish(t, false)
	must(t, os.WriteFile(filepath.Join(e.bin, "keeper"), []byte(fixtureBinary), 0o755))
	if out, errOut, err := e.run(); err != nil || out != "fixture keeper mcp version\n" {
		t.Errorf("a new enough keeper must run: %q, %q, %v", out, errOut, err)
	}
}
