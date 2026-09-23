package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/build"
	"github.com/mtchen/keeper/internal/client"
)

// `keeper update` replaces this binary with the published release and, when
// nothing is in flight, restarts the daemon onto it.
//
// It is a CLI command and is deliberately absent from the MCP surface (SPEC
// §6.3). The rest of that surface withholds registering a connection,
// accepting a finding, approving and granting for one reason — an agent that
// could do them would be removing its own supervision — and replacing
// keeper's own executable is the widest version of exactly that.
//
// The daemon is checked before anything is downloaded, not after. A restart
// permanently invalidates every token every session holds and cancels every
// pending ticket (R3.4d), so when there is something to lose this command
// stops while the system is still consistent. Replacing the binary first and
// refusing to restart afterwards would leave a new client that its own daemon
// refuses to talk to, which is a worse place to stop than where we started.

const defaultReleaseBase = "https://github.com/mtch3n/keeper/releases/latest/download"

// maxBinary bounds what is read out of the archive. A keeper build is tens of
// megabytes; anything past this is not one, and an unbounded read from a
// remote archive is how a download becomes a memory problem.
const maxBinary = 256 << 20

// releaseBase is where the release assets are fetched from. The environment
// override exists so the integration test can serve a fixture rather than
// reach GitHub, and for anyone mirroring releases internally.
func releaseBase() string {
	if v := os.Getenv("KEEPER_RELEASE_BASE"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultReleaseBase
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "report what is published and change nothing")
	force := fs.Bool("force", false, "restart even though live sessions or pending work would be lost")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sums, published, err := fetchChecksums(ctx)
	if err != nil {
		return err
	}
	if published == build.Version {
		fmt.Printf("keeper %s is the published version\n", published)
		return nil
	}
	fmt.Printf("published %s · running %s\n", published, build.Version)
	if *check {
		return nil
	}

	// What the restart would cost, decided before anything is touched.
	loss, err := inFlight(ctx)
	if err != nil {
		return err
	}
	if loss != "" && !*force {
		fmt.Println()
		fmt.Println(loss)
		fmt.Println("Updating means restarting the daemon, and a restart cancels every pending")
		fmt.Println("ticket and permanently invalidates every token those sessions hold: the")
		fmt.Println("reverse map is memory and the queries that minted them have to be re-run.")
		fmt.Println("Nothing has been changed. Re-run with --force when that is acceptable.")
		return nil
	}

	target, err := replaceableBinary()
	if err != nil {
		return err
	}

	asset := fmt.Sprintf("keeper_%s_%s_%s.tar.gz", published, runtime.GOOS, runtime.GOARCH)
	want, ok := sums[asset]
	if !ok {
		return fmt.Errorf("release %s publishes no %s/%s build", published, runtime.GOOS, runtime.GOARCH)
	}

	blob, err := fetch(ctx, releaseBase()+"/"+asset)
	if err != nil {
		return err
	}
	// Verified before anything is written. This binary is about to hold a
	// database credential, and the checksum is the one step that says what was
	// downloaded is what was published.
	if got := hex.EncodeToString(sha256Sum(blob)); got != want {
		return fmt.Errorf("%s does not match its published checksum (want %s, got %s)", asset, want, got)
	}
	fmt.Printf("verified %s against SHA256SUMS\n", asset)

	binary, err := binaryFromArchive(blob)
	if err != nil {
		return err
	}
	if err := replaceBinary(target, binary); err != nil {
		return err
	}
	fmt.Printf("installed %s at %s\n", published, target)

	return restartOntoNewBinary(ctx, loss)
}

// fetchChecksums reads SHA256SUMS and returns it keyed by asset name, along
// with the version those assets carry. The published version is taken from the
// asset names rather than from a separate call, so the number and the bytes it
// names come from one file.
func fetchChecksums(ctx context.Context) (map[string]string, string, error) {
	body, err := fetch(ctx, releaseBase()+"/SHA256SUMS")
	if err != nil {
		return nil, "", err
	}
	sums := map[string]string{}
	version := ""
	for line := range strings.Lines(string(body)) {
		digest, name, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if !ok {
			continue
		}
		name = filepath.Base(strings.TrimSpace(name))
		sums[name] = digest
		// keeper_0.0.4_linux_amd64.tar.gz
		if rest, ok := strings.CutPrefix(name, "keeper_"); ok {
			if v, _, ok := strings.Cut(rest, "_"); ok {
				version = v
			}
		}
	}
	if version == "" || len(sums) == 0 {
		return nil, "", errors.New("SHA256SUMS names no release asset; the release may still be uploading")
	}
	return sums, version, nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBinary))
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// inFlight describes what a restart would destroy, or "" when it would destroy
// nothing. A daemon that is not running has nothing in flight by definition.
func inFlight(ctx context.Context) (string, error) {
	cli, err := dialOnly(ctx, client.DefaultSocketPath())
	if err != nil {
		// A daemon at a different version refuses the handshake, and that is
		// the state an interrupted update leaves behind — so it is the one
		// case where "cannot read it" must not be read as "nothing is there".
		// Every other dial failure means no daemon, which genuinely has
		// nothing to lose.
		if _, mismatch := errors.AsType[*client.VersionMismatchError](err); mismatch {
			return "a daemon is running at a different version, so what a restart would cancel cannot be read", nil
		}
		return "", nil
	}
	defer cli.Close()

	rep, err := cli.GetDoctor(ctx)
	if err != nil {
		// The daemon answered the handshake and then would not report. Treat
		// that as something to lose rather than nothing: the failure mode of
		// guessing wrong here is silently cancelling another window's work.
		return "the running daemon did not report its state, so what a restart would cancel is unknown", nil
	}
	var parts []string
	if n := len(rep.Sessions); n > 0 {
		parts = append(parts, fmt.Sprintf("%d agent session(s) connected", n))
	}
	if rep.PendingApprovals > 0 {
		parts = append(parts, fmt.Sprintf("%d approval(s) waiting on a human", rep.PendingApprovals))
	}
	if rep.OpenTickets > 0 {
		parts = append(parts, fmt.Sprintf("%d open ticket(s)", rep.OpenTickets))
	}
	if rep.OpenRequests > 0 {
		parts = append(parts, fmt.Sprintf("%d open local request(s)", rep.OpenRequests))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, ", ") + ".", nil
}

// managedPrefixes are directories a package manager owns. Writing into one
// works when the command is run as root and is still wrong: the next upgrade
// of that package overwrites it, and the package database then describes a
// file that is not there.
//
// `/usr/local` is deliberately absent. It is the one place under /usr that
// exists precisely so a local admin can install outside the package manager,
// and the README names it as the second install location — refusing it would
// mean this command declines to update installs done the way the project
// documents. The writability probe below is what stops an unprivileged run
// there, which is the correct reason to stop.
var managedPrefixes = []string{
	"/usr/bin/",
	"/usr/sbin/",
	"/usr/lib/",
	"/usr/local/Cellar/",
	"/opt/homebrew/",
	"/home/linuxbrew/",
	"/nix/store/",
	"/snap/",
	"/var/lib/flatpak/",
}

// replaceableBinary returns the path this process runs from, or an error
// naming why it must not be replaced here.
func replaceableBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("this process has no executable path to replace: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return self, replaceable(self)
}

// replaceable reports whether this command may write over the binary at path.
func replaceable(self string) error {
	for _, prefix := range managedPrefixes {
		if strings.HasPrefix(self, prefix) {
			return fmt.Errorf(
				"%s is package-manager territory, so this update would be undone by the next upgrade of that package.\n"+
					"Update it the way it was installed, or install a copy under ~/.local/bin and let that one take over", self)
		}
	}
	// Writability is established by doing it, not by reading a mode: the mode
	// does not account for the filesystem being read-only or for who we are.
	probe, err := os.CreateTemp(filepath.Dir(self), ".keeper-update-probe-*")
	if err != nil {
		return fmt.Errorf(
			"cannot write next to %s, so the binary cannot be replaced in place: %w.\n"+
				"Download the release and install it yourself, rather than running this under sudo", self, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}

// binaryFromArchive pulls the keeper executable out of the release tarball.
func binaryFromArchive(blob []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("release archive is not gzip: %w", err)
	}
	defer zr.Close()

	want := "keeper"
	if runtime.GOOS == "windows" {
		want = "keeper.exe"
	}
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read release archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != want {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxBinary))
	}
	return nil, fmt.Errorf("release archive contains no %s", want)
}

// replaceBinary writes the new executable beside the old one and renames it
// over the top, so a download that dies half-way leaves the working binary
// where it was rather than a truncated one.
func replaceBinary(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".keeper-update-*")
	if err != nil {
		return fmt.Errorf("stage the new binary in %s: %w", dir, err)
	}
	staged := tmp.Name()
	defer os.Remove(staged)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", staged, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", staged, err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", staged, err)
	}

	if runtime.GOOS == "windows" {
		// Windows will not rename over a running image. Moving the running one
		// aside is allowed, so the new binary takes the name and the old one is
		// left for the next run to clear.
		old := path + ".old"
		os.Remove(old)
		if err := os.Rename(path, old); err != nil {
			return fmt.Errorf("move the running binary aside: %w", err)
		}
		if err := os.Rename(staged, path); err != nil {
			os.Rename(old, path)
			return fmt.Errorf("install the new binary: %w", err)
		}
		return nil
	}
	if err := os.Rename(staged, path); err != nil {
		return fmt.Errorf("install the new binary at %s: %w", path, err)
	}
	return nil
}

// restartOntoNewBinary brings the daemon onto what was just installed. The
// daemon spawns from this process's own executable path, which now holds the
// new build.
func restartOntoNewBinary(ctx context.Context, loss string) error {
	sock := client.DefaultSocketPath()
	cli, err := dialOnly(ctx, sock)
	if err != nil {
		fmt.Println("no daemon was running; the next thing that needs one starts it on the new binary")
		return nil
	}
	cli.Close()

	if err := client.RestartDaemon(ctx, sock); err != nil {
		return fmt.Errorf("the binary was replaced but the daemon did not restart: %w.\nRun `keeper daemon restart`", err)
	}
	if loss == "" {
		fmt.Println("daemon restarted; nothing was in flight")
		return nil
	}
	fmt.Printf("daemon restarted, cancelling: %s\n", loss)
	return nil
}
