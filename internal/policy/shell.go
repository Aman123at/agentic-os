package policy

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ShellEnv is what shell analysis needs to know about the Session.
type ShellEnv struct {
	Cwd  string
	Home string
	// Stat reports whether a path exists and is a directory. Nil means nothing exists.
	Stat func(path string) (exists, dir bool)
}

// CommandAnalysis is the early warning for run_command (PLAN.md §7.4): Landlock
// is the enforcement, analysis lets the Agent ask first.
type CommandAnalysis struct {
	Risky   bool
	Reasons []string
	// Effects are the paths the command visibly changes. Paths computed at run
	// time ($(…), unknown variables) are not included.
	Effects []Effect
}

// AnalyzeCommand parses a bash command and classifies what it does.
func AnalyzeCommand(command string, env ShellEnv) CommandAnalysis {
	a := &analyzer{env: env, cwd: env.Cwd}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return CommandAnalysis{Reasons: []string{"could not parse the command: " + err.Error()}}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Stmt:
			for _, r := range n.Redirs {
				a.redirect(r)
			}
		case *syntax.CallExpr:
			a.call(n)
		}
		return true
	})
	return a.result
}

// Program returns the program a command is about: the first one it runs other
// than cd, export and the like, looking through wrappers such as sudo, env and
// timeout. The Retry guard counts failures per program (PLAN.md §8.3).
func Program(command string) string {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		if f := strings.Fields(command); len(f) > 0 {
			return filepath.Base(f[0])
		}
		return ""
	}
	a := &analyzer{}
	name := ""
	syntax.Walk(file, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok && name == "" {
			args := make([]arg, len(call.Args))
			for i, w := range call.Args {
				args[i] = a.word(w)
			}
			if p := program(args); !setup[p] {
				name = p
			}
		}
		return name == ""
	})
	return name
}

// setup lists commands that prepare for the real one.
var setup = map[string]bool{"": true, "cd": true, "pushd": true, "popd": true, "export": true, "set": true, "unset": true, "source": true, ".": true, "alias": true}

// program returns the base name of the program args run, through wrappers.
func program(args []arg) string {
	for len(args) > 0 {
		if !args[0].static {
			return ""
		}
		name := filepath.Base(args[0].value)
		values, ok := wrappers[name]
		if !ok {
			return name
		}
		rest := args[1:]
		i := 0
	options:
		for ; i < len(rest); i++ {
			v := rest[i].value
			switch {
			case name == "env" && strings.Contains(v, "=") && !strings.HasPrefix(v, "-"):
			case strings.HasPrefix(v, "-") && v != "-":
				if slices.Contains(values, v) {
					i++
				}
			case name == "timeout":
				name = "" // the duration; the command follows
			default:
				break options
			}
		}
		args = rest[i:]
	}
	return ""
}

type analyzer struct {
	env    ShellEnv
	cwd    string
	result CommandAnalysis
}

func (a *analyzer) risky(format string, args ...any) {
	a.result.Risky = true
	reason := fmt.Sprintf(format, args...)
	for _, r := range a.result.Reasons {
		if r == reason {
			return
		}
	}
	a.result.Reasons = append(a.result.Reasons, reason)
}

func (a *analyzer) effect(path string, op Op) {
	a.result.Effects = append(a.result.Effects, Effect{Path: path, Op: op})
}

func (a *analyzer) stat(path string) (exists, dir bool) {
	if a.env.Stat == nil {
		return false, false
	}
	return a.env.Stat(path)
}

// arg is one evaluated word: its value, and whether it is known before running.
type arg struct {
	value  string
	static bool
}

func (a *analyzer) call(call *syntax.CallExpr) {
	args := make([]arg, len(call.Args))
	for i, w := range call.Args {
		args[i] = a.word(w)
	}
	a.command(args)
}

// wrappers run the command that follows their options; values lists the options
// that take a separate value.
var wrappers = map[string][]string{
	"sudo":    {"-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U", "-T"},
	"env":     {"-u", "-C", "-S", "--unset", "--chdir"},
	"nice":    {"-n", "--adjustment"},
	"ionice":  {"-c", "-n", "-p", "-P", "-u"},
	"nohup":   nil,
	"time":    nil,
	"command": nil,
	"builtin": nil,
	"exec":    {"-a"},
	"stdbuf":  {"-i", "-o", "-e"},
	"timeout": {"-s", "-k", "--signal", "--kill-after"},
	"xargs":   {"-I", "-n", "-P", "-L", "-d", "-s", "-E", "-a", "--max-args", "--max-procs", "--delimiter", "--arg-file"},
}

// command analyses one simple command given as evaluated words.
func (a *analyzer) command(args []arg) {
	if len(args) == 0 || !args[0].static {
		return
	}
	name := filepath.Base(args[0].value)
	rest := args[1:]
	if values, ok := wrappers[name]; ok {
		i := 0
		for ; i < len(rest); i++ {
			v := rest[i].value
			switch {
			case name == "env" && strings.Contains(v, "=") && !strings.HasPrefix(v, "-"):
			case strings.HasPrefix(v, "-") && v != "-":
				for _, f := range values {
					if v == f {
						i++
					}
				}
			case name == "timeout":
				name = "" // the duration; the command follows
			default:
				inner := rest[i:]
				if filepath.Base(args[0].value) == "xargs" {
					// xargs appends arguments that are only known at run time.
					inner = append(append([]arg{}, inner...), arg{})
				}
				a.command(inner)
				return
			}
		}
		if filepath.Base(args[0].value) == "xargs" && i == len(rest) {
			return // xargs without a command runs echo
		}
		return
	}
	if strings.HasPrefix(name, "mkfs.") {
		name = "mkfs"
	}
	switch name {
	case "mkfs", "mke2fs":
		a.risky("%s formats a disk", name)
	case "cd":
		if len(rest) == 1 && rest[0].static {
			a.cwd = a.abs(rest[0].value)
		}
	case "rm", "rmdir", "unlink":
		a.remove(name, operands(rest))
	case "shred":
		a.remove(name, operands(rest))
	case "mv":
		a.transfer("mv", rest, true)
	case "cp":
		a.transfer("cp", rest, false)
	case "find":
		a.find(rest)
	case "tee":
		appends := hasFlag(rest, "-a", "--append")
		for _, op := range operands(rest) {
			a.write("tee", op, !appends)
		}
	case "dd":
		for _, x := range rest {
			if v, ok := strings.CutPrefix(x.value, "of="); ok {
				a.write("dd", arg{value: v, static: x.static}, false)
				a.risky("dd writes raw data to %s", a.abs(v))
			}
		}
	case "truncate":
		for _, op := range operandsWithValues(rest, "-s", "--size", "-r", "--reference") {
			a.write("truncate", op, false)
			if op.static {
				a.risky("truncate cuts %s", a.abs(op.value))
			}
		}
	case "sed":
		a.sed(rest)
	case "bash", "sh", "dash", "zsh":
		for i, x := range rest {
			if strings.HasPrefix(x.value, "-") && !strings.HasPrefix(x.value, "--") && strings.Contains(x.value, "c") {
				if i+1 < len(rest) {
					a.nested(rest[i+1])
				}
				return
			}
		}
	case "eval":
		var words []string
		for _, x := range rest {
			if !x.static {
				a.risky("eval runs a command built at run time")
				return
			}
			words = append(words, x.value)
		}
		a.nested(arg{value: strings.Join(words, " "), static: true})
	case "git":
		a.git(rest)
	case "curl":
		a.curl(rest)
	case "wget":
		for i, x := range rest {
			v := x.value
			if (v == "-O" || v == "--output-document") && i+1 < len(rest) {
				a.write("wget", rest[i+1], false)
			} else if out, ok := strings.CutPrefix(v, "--output-document="); ok {
				a.write("wget", arg{value: out, static: x.static}, false)
			}
			for _, f := range []string{"--post-data", "--post-file", "--body-data", "--body-file"} {
				if v == f || strings.HasPrefix(v, f+"=") {
					a.risky("wget uploads data")
				}
			}
		}
	case "scp", "rsync", "sftp", "ftp":
		a.copyRemote(name, rest)
	case "pipx", "npm", "pnpm", "yarn", "pip", "pip3", "apt", "apt-get", "snap", "dpkg":
		a.software(name, rest)
	case "wipefs", "fdisk", "parted", "sgdisk":
		a.risky("%s changes disk partitions", name)
	case "chmod", "chown", "chgrp":
		ops := operandsWithValues(rest)
		if !hasPrefixFlag(rest, "--reference") && len(ops) > 0 {
			ops = ops[1:]
		}
		for _, op := range ops {
			a.write(name, op, false)
		}
	case "touch":
		for _, op := range operandsWithValues(rest, "-d", "-t", "-r", "--date", "--reference") {
			a.write(name, op, false)
		}
	case "mkdir":
		for _, op := range operandsWithValues(rest, "-m", "--mode") {
			a.write(name, op, false)
		}
	case "ln":
		ops := operandsWithValues(rest, "-t", "--target-directory", "-S", "--suffix")
		switch {
		case len(ops) == 1 && ops[0].static:
			a.write("ln", arg{value: filepath.Base(ops[0].value), static: true}, false)
		case len(ops) > 1:
			a.write("ln", ops[len(ops)-1], false)
		}
	}
}

func (a *analyzer) nested(script arg) {
	if !script.static {
		a.risky("runs a shell command built at run time")
		return
	}
	sub := AnalyzeCommand(script.value, ShellEnv{Cwd: a.cwd, Home: a.env.Home, Stat: a.env.Stat})
	for _, r := range sub.Reasons {
		if sub.Risky {
			a.risky("%s", r)
		}
	}
	a.result.Effects = append(a.result.Effects, sub.Effects...)
}

func (a *analyzer) git(args []arg) {
	dir := a.cwd
	i := 0
	for ; i < len(args); i++ {
		v := args[i].value
		if v == "-C" && i+1 < len(args) {
			i++
			if !args[i].static {
				return
			}
			dir = a.abs(args[i].value)
			continue
		}
		if v == "-c" || v == "--git-dir" || v == "--work-tree" || v == "--namespace" {
			i++
			continue
		}
		if !strings.HasPrefix(v, "-") {
			break
		}
	}
	if i >= len(args) {
		return
	}
	sub, rest := args[i].value, args[i+1:]
	discard := func(what string) {
		a.effect(dir, Discard)
		a.risky("git %s discards uncommitted changes in %s", what, dir)
	}
	switch sub {
	case "reset":
		if hasFlag(rest, "--hard", "--merge", "--keep") {
			discard("reset")
		}
	case "clean":
		for _, x := range rest {
			if x.value == "--force" || (strings.HasPrefix(x.value, "-") && !strings.HasPrefix(x.value, "--") && strings.Contains(x.value, "f")) {
				discard("clean")
				return
			}
		}
	case "checkout", "switch":
		for _, x := range rest {
			if x.value == "--" || x.value == "." || x.value == "-f" || x.value == "--force" || x.value == "--discard-changes" {
				discard(sub)
				return
			}
		}
	case "restore":
		if !hasFlag(rest, "--staged", "-S") || hasFlag(rest, "--worktree", "-W") {
			discard("restore")
		}
	case "stash":
		if len(rest) > 0 && (rest[0].value == "drop" || rest[0].value == "clear") {
			discard("stash " + rest[0].value)
		}
	case "push":
		for _, x := range rest {
			if x.value == "-f" || strings.HasPrefix(x.value, "--force") || strings.HasPrefix(x.value, "+") || x.value == "--delete" || x.value == "-d" {
				a.risky("git push rewrites or deletes remote history")
				return
			}
		}
	case "branch":
		if hasFlag(rest, "-D", "--delete", "-d") {
			a.risky("git branch deletes a branch")
		}
	}
}

func (a *analyzer) curl(args []arg) {
	for i, x := range args {
		v := x.value
		switch {
		case v == "-o" || v == "--output":
			if i+1 < len(args) {
				a.write("curl", args[i+1], false)
			}
		case strings.HasPrefix(v, "--output="):
			a.write("curl", arg{value: strings.TrimPrefix(v, "--output="), static: x.static}, false)
		case v == "-d" || v == "-F" || v == "-T" || strings.HasPrefix(v, "--data") || strings.HasPrefix(v, "--form") ||
			strings.HasPrefix(v, "--upload-file") || strings.HasPrefix(v, "--json"):
			a.risky("curl uploads data")
		case strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--") && len(v) > 2:
			// Combined short options such as -fsSLo FILE: o takes the next argument.
			flags := v[1:]
			if strings.ContainsAny(flags, "dFT") {
				a.risky("curl uploads data")
			}
			if strings.HasSuffix(flags, "o") && i+1 < len(args) {
				a.write("curl", args[i+1], false)
			}
		}
	}
}

// copyRemote handles scp, rsync, sftp and ftp: copying to another host is an upload.
func (a *analyzer) copyRemote(name string, args []arg) {
	if name == "sftp" || name == "ftp" {
		a.risky("%s can upload files to another host", name)
		return
	}
	ops := operandsWithValues(args, "-e", "-P", "-i", "-o", "-F", "-l", "-S", "--rsh", "--exclude", "--include", "--filter")
	if len(ops) < 2 {
		return
	}
	dest := ops[len(ops)-1]
	if isRemote(dest.value) {
		a.risky("%s uploads files to %s", name, dest.value)
		return
	}
	a.write(name, dest, false)
	if name == "rsync" && hasPrefixFlag(args, "--delete") {
		a.risky("rsync --delete deletes files in %s", a.abs(dest.value))
	}
}

func (a *analyzer) software(name string, args []arg) {
	ops := operands(args)
	if len(ops) == 0 {
		if name == "dpkg" && (hasFlag(args, "-r", "-P", "--remove", "--purge")) {
			a.risky("dpkg removes software")
		}
		return
	}
	switch ops[0].value {
	case "uninstall", "uninstall-all", "remove", "rm", "un", "r", "purge", "autoremove":
		a.risky("%s %s removes software", name, ops[0].value)
	}
}

func isRemote(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	colon := strings.Index(s, ":")
	return colon > 0 && !strings.Contains(s[:colon], "/")
}

// write records a write to x; overwriting an existing file is risky when clobber is set.
func (a *analyzer) write(name string, x arg, clobber bool) {
	if !x.static {
		return
	}
	path := a.abs(x.value)
	if within(path, "/dev") {
		return
	}
	a.effect(path, Write)
	if exists, dir := a.stat(path); clobber && exists && !dir && !scratch(path) {
		a.risky("%s overwrites %s", name, path)
	}
}

func (a *analyzer) redirect(r *syntax.Redirect) {
	if r.Word == nil {
		return
	}
	switch r.Op {
	case syntax.RdrOut, syntax.RdrAll, syntax.RdrClob:
		a.write("redirection", a.word(r.Word), true)
	case syntax.AppOut, syntax.AppAll:
		a.write("redirection", a.word(r.Word), false)
	case syntax.DplOut:
		if x := a.word(r.Word); x.static && strings.Trim(x.value, "0123456789-") != "" {
			a.write("redirection", x, true)
		}
	}
}

func (a *analyzer) sed(args []arg) {
	inPlace, scripted := false, false
	var ops []arg
	for i := 0; i < len(args); i++ {
		v := args[i].value
		switch {
		case v == "-e" || v == "-f" || v == "--expression" || v == "--file":
			scripted = true
			i++
		case strings.HasPrefix(v, "--expression=") || strings.HasPrefix(v, "--file="):
			scripted = true
		case v == "-i" || strings.HasPrefix(v, "-i") && !strings.HasPrefix(v, "-i-") || strings.HasPrefix(v, "--in-place"):
			inPlace = true
		case strings.HasPrefix(v, "-") && v != "-":
			if strings.Contains(strings.TrimLeft(v, "-"), "i") && !strings.HasPrefix(v, "--") {
				inPlace = true
			}
		default:
			ops = append(ops, args[i])
		}
	}
	if !inPlace {
		return
	}
	if !scripted && len(ops) > 0 {
		ops = ops[1:]
	}
	for _, op := range ops {
		a.write("sed", op, false)
	}
}

// remove records deletions; deleting only scratch space is not risky.
func (a *analyzer) remove(name string, ops []arg) {
	for _, op := range ops {
		if !op.static {
			a.risky("%s deletes files chosen at run time", name)
			continue
		}
		path := a.abs(op.value)
		a.effect(path, Delete)
		if !scratch(path) {
			a.risky("%s deletes %s", name, path)
		}
	}
}

// transfer handles mv and cp: sources (moved away by mv), a destination, and
// overwriting an existing file, which is risky.
func (a *analyzer) transfer(name string, args []arg, moves bool) {
	ops := operandsWithValues(args, "-t", "--target-directory", "-S", "--suffix")
	noClobber := hasFlag(args, "-n", "--no-clobber")
	var dest arg
	if t, ok := flagValue(args, "-t", "--target-directory"); ok {
		dest = t
	} else if len(ops) >= 2 {
		dest, ops = ops[len(ops)-1], ops[:len(ops)-1]
	} else {
		return
	}
	if moves {
		for _, src := range ops {
			if src.static {
				a.effect(a.abs(src.value), Delete)
			}
		}
	}
	if !dest.static {
		a.risky("%s writes to a destination chosen at run time", name)
		return
	}
	destPath := a.abs(dest.value)
	a.effect(destPath, Write)
	_, destIsDir := a.stat(destPath)
	for _, src := range ops {
		target := destPath
		if destIsDir {
			target = filepath.Join(destPath, filepath.Base(src.value))
		}
		if exists, dir := a.stat(target); exists && !dir && !noClobber && !scratch(target) {
			a.risky("%s overwrites %s", name, target)
		}
	}
}

func (a *analyzer) find(args []arg) {
	var starts []arg
	i := 0
	for ; i < len(args); i++ {
		if v := args[i].value; strings.HasPrefix(v, "-") || v == "(" || v == "!" {
			break
		}
		starts = append(starts, args[i])
	}
	deletes := false
	for j := i; j < len(args); j++ {
		switch args[j].value {
		case "-delete":
			deletes = true
		case "-exec", "-execdir", "-ok", "-okdir":
			if j+1 < len(args) {
				switch filepath.Base(args[j+1].value) {
				case "rm", "rmdir", "unlink", "shred":
					deletes = true
				}
			}
		}
	}
	if !deletes {
		return
	}
	if len(starts) == 0 {
		starts = []arg{{value: ".", static: true}}
	}
	a.remove("find", starts)
}

// word evaluates w as far as possible without running anything.
func (a *analyzer) word(w *syntax.Word) arg {
	var b strings.Builder
	static := true
	for i, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			v := unescape(p.Value)
			if i == 0 && (v == "~" || strings.HasPrefix(v, "~/")) {
				v = a.env.Home + v[1:]
			}
			b.WriteString(v)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				switch ip := inner.(type) {
				case *syntax.Lit:
					b.WriteString(ip.Value)
				case *syntax.ParamExp:
					v, ok := a.param(ip)
					static = static && ok
					b.WriteString(v)
				default:
					static = false
				}
			}
		case *syntax.ParamExp:
			v, ok := a.param(p)
			static = static && ok
			b.WriteString(v)
		default:
			static = false
		}
	}
	return arg{value: b.String(), static: static}
}

func (a *analyzer) param(p *syntax.ParamExp) (string, bool) {
	if p.Param == nil || p.Exp != nil || p.Repl != nil || p.Slice != nil || p.Index != nil || p.Length || p.Excl {
		return "", false
	}
	switch p.Param.Value {
	case "HOME":
		return a.env.Home, true
	case "PWD":
		return a.cwd, true
	}
	return "", false
}

func (a *analyzer) abs(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.cwd, path)
	}
	return filepath.Clean(path)
}

// operands returns the non-option arguments; everything after "--" is an operand.
func operands(args []arg) []arg {
	var ops []arg
	for i, x := range args {
		if x.value == "--" && x.static {
			return append(ops, args[i+1:]...)
		}
		if x.static && strings.HasPrefix(x.value, "-") && x.value != "-" {
			continue
		}
		ops = append(ops, x)
	}
	return ops
}

// operandsWithValues is like operands, but skips the value after each option in valueFlags.
func operandsWithValues(args []arg, valueFlags ...string) []arg {
	var ops []arg
	for i := 0; i < len(args); i++ {
		x := args[i]
		if x.value == "--" && x.static {
			return append(ops, args[i+1:]...)
		}
		if x.static && strings.HasPrefix(x.value, "-") && x.value != "-" {
			for _, f := range valueFlags {
				if x.value == f {
					i++
				}
			}
			continue
		}
		ops = append(ops, x)
	}
	return ops
}

// flagValue returns the value of an option given as "-t v", "--opt v" or "--opt=v".
func flagValue(args []arg, flags ...string) (arg, bool) {
	for i, x := range args {
		for _, f := range flags {
			if x.value == f && i+1 < len(args) {
				return args[i+1], true
			}
			if v, ok := strings.CutPrefix(x.value, f+"="); ok && strings.HasPrefix(f, "--") {
				return arg{value: v, static: x.static}, true
			}
		}
	}
	return arg{}, false
}

func hasPrefixFlag(args []arg, prefix string) bool {
	for _, x := range args {
		if strings.HasPrefix(x.value, prefix) {
			return true
		}
	}
	return false
}

func hasFlag(args []arg, flags ...string) bool {
	for _, x := range args {
		if x.value == "--" {
			return false
		}
		for _, f := range flags {
			if x.value == f {
				return true
			}
		}
	}
	return false
}

// scratch reports whether path is in a folder whose contents are disposable.
func scratch(path string) bool {
	return within(path, "/tmp") || within(path, "/var/tmp")
}

// unescape removes shell backslash escapes from an unquoted literal.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
