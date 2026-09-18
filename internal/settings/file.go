package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// configFile is /etc/aos/config.yml as a mutable YAML document. It keeps the
// yaml.v3 Node tree so comments and key order survive a write-back (ADR-0010):
// a value the user changes at runtime is set on its scalar node in place, and
// the whole tree is re-marshalled, so a hand-added comment is still there
// afterwards.
type configFile struct {
	path string
	doc  yaml.Node // a DocumentNode whose Content[0] is the top-level mapping
}

// readConfigFile parses path. A missing file yields an empty document
// (exists=false); a file that is not valid YAML, or whose top level is not a
// mapping, is an error — a typo refuses the start (ADR-0010) rather than
// silently reverting to defaults.
func readConfigFile(path string) (f *configFile, exists bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &configFile{path: path}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	f = &configFile{path: path}
	if err := yaml.Unmarshal(data, &f.doc); err != nil {
		return nil, false, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if len(f.doc.Content) > 0 && f.doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("%s must be a mapping of key: value pairs", path)
	}
	return f, true, nil
}

// mapping returns the top-level mapping node, creating the document and mapping
// if the file was empty.
func (f *configFile) mapping() *yaml.Node {
	if len(f.doc.Content) == 0 {
		f.doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	return f.doc.Content[0]
}

// pair returns the value node for key, or nil.
func (f *configFile) pair(key string) *yaml.Node {
	m := f.mapping()
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// keys returns the top-level keys in file order.
func (f *configFile) keys() []string {
	m := f.mapping()
	out := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

// get returns the scalar value for key.
func (f *configFile) get(key string) (string, bool) {
	if n := f.pair(key); n != nil {
		return n.Value, true
	}
	return "", false
}

// set writes value for key. An existing key keeps its tag, style and comments
// (values are type-checked per key, so the tag stays valid); a new key is
// appended with a tag inferred from the value.
func (f *configFile) set(key, value string) {
	if n := f.pair(key); n != nil {
		n.Value = value
		return
	}
	m := f.mapping()
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: scalarTag(value), Value: value})
}

// remove deletes key if present, so the environment or the built-in default
// applies again.
func (f *configFile) remove(key string) {
	m := f.mapping()
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// writeAtomic marshals the document and replaces the file in one rename, so a
// reader never sees a half-written config. The file is root-only 0600.
func (f *configFile) writeAtomic() error {
	out, err := yaml.Marshal(&f.doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.yml-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, f.path)
}

// scalarTag picks a YAML tag so a generated value reads naturally (numbers and
// booleans unquoted), for a newly appended key.
func scalarTag(v string) string {
	switch v {
	case "true", "false":
		return "!!bool"
	case "":
		return "!!str"
	}
	if _, err := strconv.Atoi(v); err == nil {
		return "!!int"
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return "!!float"
	}
	return "!!str"
}
