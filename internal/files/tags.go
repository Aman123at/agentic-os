package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// OpTags lists the symbols defined in some files of a tree, with
// universal-ctags, for Code's navigation (PLAN.md M8.8). Like every file
// operation it runs as the Realm's user.
const OpTags = "tags"

// ErrNoCtags is returned on a Machine without universal-ctags (Exuberant
// Ctags cannot write JSON).
var ErrNoCtags = errors.New("universal-ctags is not installed")

type TagsArgs struct {
	Root string `json:"root"`
	// Files are relative to Root, slash-separated.
	Files []string `json:"files"`
}

// Tag is one symbol ctags found.
type Tag struct {
	Name     string `json:"name"`
	Path     string `json:"path"` // relative to the root, as given
	Line     int    `json:"line"`
	End      int    `json:"end,omitempty"`
	Kind     string `json:"kind"`
	Language string `json:"language,omitempty"`
	// Scope is what the symbol is inside, such as "main.Server" for a method.
	Scope     string `json:"scope,omitempty"`
	ScopeKind string `json:"scopeKind,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// maxSignature bounds a symbol's signature: a generated file's can be huge.
const maxSignature = 200

// ctagsNames are universal-ctags' names across distributions: Debian and
// Ubuntu install it as ctags-universal behind the ctags alternative.
var ctagsNames = []string{"ctags-universal", "universal-ctags", "ctags"}

// findCtags returns universal-ctags, or ErrNoCtags.
func findCtags(ctx context.Context) (string, error) {
	for _, name := range ctagsNames {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, p, "--version").Output()
		if err == nil && bytes.Contains(out, []byte("Universal Ctags")) {
			return p, nil
		}
	}
	return "", ErrNoCtags
}

// Tags runs ctags over a.Files. Files it cannot read or has no parser for
// give no tags.
func (o Ops) Tags(ctx context.Context, a TagsArgs) ([]Tag, error) {
	ctags, err := findCtags(ctx)
	if err != nil {
		return nil, err
	}
	var list strings.Builder
	for _, f := range a.Files {
		if f != "" && !strings.ContainsAny(f, "\n\r") {
			list.WriteString(f)
			list.WriteByte('\n')
		}
	}
	// --options=NONE: the user's ~/.ctags.d must not change what Code sees.
	cmd := exec.CommandContext(ctx, ctags, "--options=NONE", "--output-format=json", "--fields=+nKlSe-P", "--sort=no", "-f", "-", "-L", "-")
	cmd.Dir = a.Root
	cmd.Stdin = strings.NewReader(list.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var tags []Tag
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var t struct {
			Type string `json:"_type"`
			Tag
		}
		if json.Unmarshal(sc.Bytes(), &t) != nil || t.Type != "tag" || t.Name == "" {
			continue
		}
		if len(t.Signature) > maxSignature {
			t.Signature = t.Signature[:maxSignature] + "…"
		}
		tags = append(tags, t.Tag)
	}
	scanErr := sc.Err()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ctags: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return tags, scanErr
}
