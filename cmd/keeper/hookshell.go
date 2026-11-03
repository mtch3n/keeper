package main

import (
	"net"
	"net/url"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// shellCommand is one simple command of a shell line: its words, and the
// NAME=value assignments that apply to it.
type shellCommand struct {
	words []string
	env   []string
}

// shellCommands parses a shell line and lists every simple command in it,
// including those inside $(...), pipelines and the script of `sh -c`. A heredoc
// body and a quoted argument are data, not commands. A line that does not parse
// lists nothing: the hook is guidance, and the shell would refuse it too.
func shellCommands(line string) []shellCommand {
	cmds, global := parseShell(line, 0)
	for i := range cmds {
		cmds[i].env = append(cmds[i].env, global...)
	}
	return cmds
}

func parseShell(line string, depth int) (cmds []shellCommand, global []string) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(line), "")
	if err != nil {
		return nil, nil
	}
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.DeclClause:
			for _, a := range n.Args {
				if a.Name != nil {
					global = append(global, assignment(a))
				}
			}
		case *syntax.CallExpr:
			var env []string
			for _, a := range n.Assigns {
				env = append(env, assignment(a))
			}
			if len(n.Args) == 0 {
				global = append(global, env...)
				return true
			}
			words := make([]string, len(n.Args))
			for i, w := range n.Args {
				words[i] = wordText(w)
			}
			cmds = append(cmds, shellCommand{words, env})
			if depth < 3 {
				if script, ok := shellScript(words); ok {
					inner, innerGlobal := parseShell(script, depth+1)
					for _, c := range inner {
						c.env = append(c.env, env...)
						cmds = append(cmds, c)
					}
					global = append(global, innerGlobal...)
				}
			}
		}
		return true
	})
	return cmds, global
}

func assignment(a *syntax.Assign) string {
	if a.Value == nil {
		return a.Name.Value + "="
	}
	return a.Name.Value + "=" + wordText(a.Value)
}

// wordText is a word with its quotes removed. An expansion stays as written, so
// it never equals a host name.
func wordText(w *syntax.Word) string {
	var b strings.Builder
	var add func(parts []syntax.WordPart)
	add = func(parts []syntax.WordPart) {
		for _, p := range parts {
			switch p := p.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
			case *syntax.DblQuoted:
				add(p.Parts)
			case *syntax.ParamExp:
				b.WriteString("$" + p.Param.Value)
			default:
				b.WriteString("$()")
			}
		}
	}
	add(w.Parts)
	return b.String()
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// shellScript returns the program text a shell command line runs: the argument
// of `sh -c`, or what `eval` joins together.
func shellScript(words []string) (string, bool) {
	i, _ := program(words)
	if i < 0 {
		return "", false
	}
	name := path.Base(words[i])
	args := words[i+1:]
	if name == "eval" {
		return strings.Join(args, " "), len(args) > 0
	}
	if !shells[name] {
		return "", false
	}
	for j, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
			if j+1 < len(args) {
				return args[j+1], true
			}
			return "", false
		}
	}
	return "", false
}

// launcher is a program that runs the rest of its command line. withArg are the
// options that take a value, positional the arguments before the command.
type launcher struct {
	withArg    []string
	positional int
	env        bool
}

var launchers = map[string]launcher{
	"sudo":    {withArg: []string{"-u", "-g", "-h", "-p", "-C", "-D", "-R", "-T", "-U"}},
	"env":     {withArg: []string{"-u", "-C", "-S"}, env: true},
	"time":    {withArg: []string{"-f", "-o"}},
	"nohup":   {},
	"exec":    {withArg: []string{"-a"}},
	"command": {},
	"nice":    {withArg: []string{"-n"}},
	"timeout": {withArg: []string{"-s", "-k"}, positional: 1},
	"xargs":   {withArg: []string{"-n", "-I", "-L", "-P", "-d", "-s", "-E", "-a"}},
	"setsid":  {},
	"stdbuf":  {withArg: []string{"-i", "-o", "-e"}},
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// program returns the index of the command a word list runs, past launchers and
// their options, or -1, and the assignments `env` gave it. `command -v` runs
// nothing.
func program(words []string) (int, []string) {
	var env []string
	i := 0
	for i < len(words) {
		l, ok := launchers[path.Base(words[i])]
		if !ok {
			return i, env
		}
		isCommand := path.Base(words[i]) == "command"
		skip := l.positional
		i++
	options:
		for ; i < len(words); i++ {
			w := words[i]
			switch {
			case l.env && isAssignment(w):
				env = append(env, w)
			case w == "--":
				i++
				break options
			case strings.HasPrefix(w, "-") && len(w) > 1:
				if isCommand && (w == "-v" || w == "-V") {
					return -1, env
				}
				if slices.Contains(l.withArg, w) {
					i++
				}
			case skip > 0:
				skip--
			default:
				break options
			}
		}
	}
	return -1, env
}

func keeperSubcommand(words []string) (string, []string) {
	i, _ := program(words)
	if i < 0 || path.Base(words[i]) != "keeper" {
		return "", nil
	}
	var rest []string
	for _, w := range words[i+1:] {
		if !strings.HasPrefix(w, "-") {
			rest = append(rest, w)
		}
	}
	if len(rest) == 0 {
		return "", nil
	}
	return rest[0], rest[1:]
}

var pgClients = map[string]bool{"psql": true, "pg_dump": true, "pg_dumpall": true, "pgcli": true}

// pgOptionsWithValue are psql options whose value is not a server address, so
// a "host=" inside it is not read as one.
var pgOptionsWithValue = []string{"-c", "--command", "-f", "--file", "-o", "--output"}

// pgServer is where a database client connects.
type pgServer struct{ host, port string }

// pgTargets lists the server every database client in the commands connects to.
// A client given no host connects to localhost on 5432, or to PGHOST and PGPORT.
func pgTargets(cmds []shellCommand) []pgServer {
	var out []pgServer
	for _, c := range cmds {
		i, launched := program(c.words)
		if i < 0 || !pgClients[path.Base(c.words[i])] {
			continue
		}
		env := append(slices.Clone(c.env), launched...)
		lookup := func(name string) string {
			for _, kv := range slices.Backward(env) {
				if v, ok := strings.CutPrefix(kv, name+"="); ok {
					return v
				}
			}
			return ""
		}
		var hosts, ports []string
		var port string
		add := func(host, p string) {
			hosts = append(hosts, splitNonEmpty(host)...)
			if port == "" {
				port = p
			}
		}
		args := c.words[i+1:]
		if slices.ContainsFunc(args, func(a string) bool {
			return a == "--version" || a == "-V" || a == "--help" || a == "-?"
		}) {
			continue
		}
		for j := 0; j < len(args); j++ {
			a := args[j]
			next := func() string {
				if j+1 < len(args) {
					j++
					return args[j]
				}
				return ""
			}
			switch {
			case slices.Contains(pgOptionsWithValue, a):
				j++
			case a == "-h" || a == "--host":
				add(next(), "")
			case strings.HasPrefix(a, "--host="):
				add(a[len("--host="):], "")
			case a == "-p" || a == "--port":
				ports = append(ports, next())
			case strings.HasPrefix(a, "--port="):
				ports = append(ports, a[len("--port="):])
			case a == "-d" || a == "--dbname":
				h, p := connectionTarget(next())
				add(h, p)
			case strings.HasPrefix(a, "--dbname="):
				h, p := connectionTarget(a[len("--dbname="):])
				add(h, p)
			case strings.HasPrefix(a, "-h") && !strings.HasPrefix(a, "--"):
				add(a[2:], "")
			case strings.HasPrefix(a, "-p") && !strings.HasPrefix(a, "--"):
				ports = append(ports, a[2:])
			case strings.HasPrefix(a, "-d") && !strings.HasPrefix(a, "--"):
				h, p := connectionTarget(a[2:])
				add(h, p)
			case !strings.HasPrefix(a, "-"):
				h, p := connectionTarget(a)
				add(h, p)
			}
		}
		if len(hosts) == 0 {
			hosts = splitNonEmpty(lookup("PGHOSTADDR"))
			hosts = append(hosts, splitNonEmpty(lookup("PGHOST"))...)
		}
		if len(hosts) == 0 {
			hosts = []string{"localhost"}
		}
		if len(ports) > 0 {
			port = ports[0]
		}
		if port == "" {
			port = lookup("PGPORT")
		}
		for _, h := range hosts {
			out = append(out, pgServer{h, port})
		}
	}
	return out
}

func splitNonEmpty(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// connectionTarget reads the host and port out of a connection URI or a
// conninfo string. Anything else, such as a database name, has none. A conninfo
// may name both host and hostaddr, and either is a way to reach the server.
func connectionTarget(arg string) (host, port string) {
	if strings.HasPrefix(arg, "postgres://") || strings.HasPrefix(arg, "postgresql://") {
		u, err := url.Parse(arg)
		if err != nil {
			return "", ""
		}
		hosts := []string{u.Hostname()}
		if q := u.Query().Get("host"); q != "" {
			hosts = append(hosts, q)
		}
		if q := u.Query().Get("hostaddr"); q != "" {
			hosts = append(hosts, q)
		}
		port = u.Port()
		if q := u.Query().Get("port"); q != "" {
			port = q
		}
		return strings.Join(slices.DeleteFunc(hosts, func(h string) bool { return h == "" }), ","), port
	}
	var hosts []string
	for kv := range strings.FieldsSeq(arg) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, "'")
		switch k {
		case "host", "hostaddr":
			hosts = append(hosts, v)
		case "port":
			port = v
		}
	}
	return strings.Join(hosts, ","), port
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || strings.HasPrefix(h, "/") {
		return "127.0.0.1"
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return "127.0.0.1"
	}
	return h
}

// sameServer compares a client's target with a registered "address:port".
func sameServer(t pgServer, registered string) bool {
	host, port, err := net.SplitHostPort(registered)
	if err != nil {
		host, port = registered, ""
	}
	return normalizeHost(t.host) == normalizeHost(host) && portOrDefault(t.port) == portOrDefault(port)
}

func portOrDefault(p string) string {
	if p == "" {
		return "5432"
	}
	return p
}
