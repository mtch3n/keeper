package mcpapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mtchen/keeper/internal/build"
	"github.com/mtchen/keeper/internal/client"
)

// replayEnv names a file of client messages a replaced keeper mcp reads before
// its stdin: the client's opening request, its initialized notification and
// every request the replaced process had not answered (UPG-D1).
const replayEnv = "KEEPER_MCP_REPLAY"

// session watches the newline-delimited JSON-RPC on stdio closely enough to
// hand the client's MCP session to a replacement process: which line
// initialized it, and which requests have no answer yet.
type session struct {
	mu      sync.Mutex
	init    []byte
	inited  []byte
	pending []request
	partial []byte
	out     []byte
	// drop is the id of a replayed opening request. The client had its answer
	// from the replaced process, so the replacement's answer is not sent again.
	drop string
}

// handshake is a client's opening request: initialize in the stateful
// protocol, server/discover in the one that has no session to open.
func handshake(method string) bool {
	return method == "initialize" || method == "server/discover"
}

type request struct {
	id   string
	line []byte
}

type envelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

// read records what the client sent, line by line.
func (s *session) read(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			return
		}
		line := bytes.Clone(s.partial[:i])
		s.partial = s.partial[i+1:]
		var e envelope
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Method {
		case "initialize", "server/discover":
			s.init = line
		case "notifications/initialized":
			s.inited = line
		}
		if e.Method != "" && len(e.ID) > 0 && !handshake(e.Method) {
			s.pending = append(s.pending, request{id: string(e.ID), line: line})
		}
	}
}

// write passes what the server sends to w, line by line, forgetting each
// request it answers and holding back the answer to a replayed initialize.
func (s *session) write(w io.Writer, p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, p...)
	for {
		i := bytes.IndexByte(s.out, '\n')
		if i < 0 {
			return nil
		}
		line := s.out[:i+1]
		var e envelope
		if json.Unmarshal(line, &e) == nil && e.Method == "" && len(e.ID) > 0 {
			id := string(e.ID)
			if id == s.drop {
				s.drop = ""
				s.out = s.out[i+1:]
				continue
			}
			for k, r := range s.pending {
				if r.id == id {
					s.pending = append(s.pending[:k], s.pending[k+1:]...)
					break
				}
			}
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		s.out = s.out[i+1:]
	}
}

// replace executes bin as keeper mcp in this process, handing it the session.
// It returns only when that fails. The lock is held through the exec, so no
// answer is half-written and no message is recorded after the hand-over is
// built.
func (s *session) replace(bin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b bytes.Buffer
	for _, line := range [][]byte{s.init, s.inited} {
		if line != nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	for _, r := range s.pending {
		b.Write(r.line)
		b.WriteByte('\n')
	}
	b.Write(s.partial)
	f, err := os.CreateTemp("", "keeper-mcp-replay-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(b.Bytes())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return werr
	}
	env := append(os.Environ(), replayEnv+"="+f.Name())
	err = syscall.Exec(bin, []string{bin, "mcp"}, env)
	os.Remove(f.Name())
	return err
}

// resume reads the hand-over a replaced process left, if any, and returns what
// the client sent before it, to be read ahead of stdin.
func (s *session) resume() []byte {
	path := os.Getenv(replayEnv)
	if path == "" {
		return nil
	}
	os.Unsetenv(replayEnv)
	data, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil {
		return nil
	}
	if line, _, ok := bytes.Cut(data, []byte("\n")); ok {
		var e envelope
		if json.Unmarshal(line, &e) == nil && handshake(e.Method) {
			s.drop = string(e.ID)
		}
	}
	return data
}

type sessionReader struct {
	r io.Reader
	s *session
}

func (r sessionReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.s.read(p[:n])
	}
	return n, err
}

func (sessionReader) Close() error { return os.Stdin.Close() }

type sessionWriter struct {
	w io.Writer
	s *session
}

func (w sessionWriter) Write(p []byte) (int, error) {
	if err := w.s.write(w.w, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (sessionWriter) Close() error { return os.Stdout.Close() }

// settle resolves a version mismatch without a person when nothing is lost
// that a person would choose to keep. A daemon on a newer build: this process
// becomes that build (UPG-D1). A daemon on an older build with nothing
// waiting: it is restarted on this build (UPG-D2). It returns nil only when
// the daemon was restarted and a new dial may succeed.
func settle(ctx context.Context, s *session, sock string, vm *client.VersionMismatchError) error {
	switch build.Compare(vm.DaemonVersion, vm.ClientVersion) {
	case 1:
		if s == nil {
			return vm
		}
		if bin := binaryAt(ctx, vm.DaemonVersion); bin != "" {
			s.replace(bin)
		}
		return vm
	case -1:
		_, waiting, err := client.DaemonStatus(ctx, sock)
		if err != nil || waiting == nil {
			return vm
		}
		if *waiting > 0 {
			vm.Waiting = *waiting
			return vm
		}
		if err := client.RestartDaemon(ctx, sock); err != nil {
			return vm
		}
		return nil
	}
	return vm
}

// binaryAt finds a keeper on disk whose build is version: this process's own
// path first, which an upgrade replaces in place, then keeper on PATH, then
// where the plugin's launcher installs it.
func binaryAt(ctx context.Context, version string) string {
	var candidates []string
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, self)
	}
	if p, err := exec.LookPath("keeper"); err == nil {
		candidates = append(candidates, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "keeper"))
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if resolved, err := filepath.EvalSymlinks(c); err == nil {
			c = resolved
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		if clientVersion(ctx, c) == version {
			return c
		}
	}
	return ""
}

// clientVersion is the build a keeper binary reports for itself.
func clientVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[0] == "client" {
			return f[1]
		}
	}
	return ""
}
