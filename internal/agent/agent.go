package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ErrInvalidProfile marks a profile file or directory that fails validation.
var ErrInvalidProfile = errors.New("agent: invalid profile")

// ErrInvalidScope marks a scope that is not user, project or both.
var ErrInvalidScope = errors.New("agent: invalid scope")

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

var decimalRE = regexp.MustCompile(`^(0|[1-9]\d*)$`)

var knownField = map[string]bool{"name": true, "description": true, "tools": true, "model": true, "max_turns": true}

// ParseScope reads a --agents value; the empty string is ScopeUser.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case "", ScopeUser:
		return ScopeUser, nil
	case ScopeProject, ScopeBoth:
		return Scope(s), nil
	}
	return "", fmt.Errorf("%w: %q (want user, project or both)", ErrInvalidScope, s)
}

// Parse validates one profile's bytes against the registered tool names in
// known. The file must start with "---" at byte 0; the frontmatter ends at the
// next line that is exactly "---" (CRLF tolerated). The frontmatter is exactly
// one YAML document with no explicit tags and no duplicate or unknown keys;
// max_turns is a plain decimal integer, every tools entry a string, and no
// single-line string carries a control, format or separator character other
// than a tab (a name carries none), and none is a block scalar. A
// tool not in known, a duplicate tool, an out-of-range max_turns, a bad name, a
// missing description and an empty body each fail, and so does a body that is
// not valid UTF-8 or holds a NUL, ESC, DEL or C1 character (newline, tab and CR
// are allowed; format characters such as ZWJ stay allowed in prose). An empty body is a failure on
// purpose: a profile with no instructions is a mistake. Surrounding whitespace
// is trimmed from the body. Path, Source and SHA256 are set by the loader.
func Parse(data []byte, known []string) (Profile, error) {
	header, body, err := split(data)
	if err != nil {
		return Profile{}, err
	}
	if !validBody(body) {
		return Profile{}, fmt.Errorf("%w: the body must be valid UTF-8 without NUL, ESC, DEL or C1 control characters",
			ErrInvalidProfile)
	}
	fields, err := decodeHeader(header)
	if err != nil {
		return Profile{}, err
	}
	p, err := build(fields, known)
	if err != nil {
		return Profile{}, err
	}
	p.Body = strings.TrimSpace(string(body))
	if p.Body == "" {
		return Profile{}, fmt.Errorf("%w: the body (system prompt) is empty", ErrInvalidProfile)
	}
	return p, nil
}

// decodeHeader parses the frontmatter into its top-level field nodes.
func decodeHeader(header []byte) (map[string]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(header))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]*yaml.Node{}, nil
		}
		return nil, fmt.Errorf("%w: frontmatter: %v", ErrInvalidProfile, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: frontmatter must be exactly one YAML document", ErrInvalidProfile)
	}
	if err := rejectTags(&doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: frontmatter must be a mapping", ErrInvalidProfile)
	}
	fields := make(map[string]*yaml.Node, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind != yaml.ScalarNode || !knownField[key.Value] {
			return nil, fmt.Errorf("%w: unknown field %q", ErrInvalidProfile, key.Value)
		}
		if fields[key.Value] != nil {
			return nil, fmt.Errorf("%w: field %q is set twice", ErrInvalidProfile, key.Value)
		}
		fields[key.Value] = root.Content[i+1]
	}
	return fields, nil
}

// rejectTags refuses an explicit YAML tag anywhere in the tree.
func rejectTags(n *yaml.Node) error {
	if n.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("%w: explicit YAML tag %q is not allowed", ErrInvalidProfile, n.Tag)
	}
	for _, c := range n.Content {
		if err := rejectTags(c); err != nil {
			return err
		}
	}
	return nil
}

// split separates the frontmatter from the body.
func split(data []byte) (header, body []byte, err error) {
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		rest, ok = bytes.CutPrefix(data, []byte("---\r\n"))
	}
	if !ok {
		return nil, nil, fmt.Errorf("%w: frontmatter must start with --- at byte 0", ErrInvalidProfile)
	}
	for offset := 0; offset < len(rest); {
		line, next := rest[offset:], len(rest)
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, next = line[:i], offset+i+1
		}
		if string(bytes.TrimSuffix(line, []byte("\r"))) == "---" {
			return rest[:offset], rest[next:], nil
		}
		offset = next
	}
	return nil, nil, fmt.Errorf("%w: frontmatter is not closed by a --- line", ErrInvalidProfile)
}

// validBody reports whether the body is UTF-8 free of NUL, ESC, DEL and the C1
// range. Format characters stay allowed: emoji text legitimately uses ZWJ.
func validBody(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	for _, r := range string(body) {
		if r == 0 || r == 0x1b || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	return true
}

// text reads a single-line string field: absent or null yields "", anything
// but a string scalar fails, and so does a block scalar, a control character
// other than a tab, a format character (Cf) or a line/paragraph separator.
func text(n *yaml.Node, field string) (string, error) {
	if n == nil || n.Tag == "!!null" {
		return "", nil
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return "", fmt.Errorf("%w: %s must be a string", ErrInvalidProfile, field)
	}
	if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return "", fmt.Errorf("%w: %s must be a single line: block scalars are not allowed", ErrInvalidProfile, field)
	}
	if hidden(strings.ReplaceAll(n.Value, "\t", "")) {
		return "", fmt.Errorf("%w: %s holds a control, format or separator character", ErrInvalidProfile, field)
	}
	return n.Value, nil
}

// hidden reports whether s holds a control, format (Cf) or line/paragraph
// separator character: none may reach a terminal unescaped.
func hidden(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return true
		}
	}
	return false
}

// build applies the field rules to the decoded header fields.
func build(fields map[string]*yaml.Node, known []string) (Profile, error) {
	p := Profile{Model: InheritModel, MaxTurns: DefaultMaxTurns}
	name, err := text(fields["name"], "name")
	if err != nil {
		return Profile{}, err
	}
	if !nameRE.MatchString(name) {
		return Profile{}, fmt.Errorf("%w: name must match %s", ErrInvalidProfile, nameRE)
	}
	p.Name = name
	if p.Description, err = text(fields["description"], "description"); err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(p.Description) == "" {
		return Profile{}, fmt.Errorf("%w: description is required", ErrInvalidProfile)
	}
	names, err := toolNames(fields["tools"])
	if err != nil {
		return Profile{}, err
	}
	if p.Tools, err = checkTools(names, known); err != nil {
		return Profile{}, err
	}
	model, err := text(fields["model"], "model")
	if err != nil {
		return Profile{}, err
	}
	if model = strings.TrimSpace(model); utf8.RuneCountInString(model) > MaxModelChars {
		return Profile{}, fmt.Errorf("%w: model is longer than %d characters", ErrInvalidProfile, MaxModelChars)
	}
	if model != "" {
		p.Model = model
	}
	return withMaxTurns(p, fields["max_turns"])
}

// withMaxTurns applies max_turns, which must be a plain decimal integer.
func withMaxTurns(p Profile, n *yaml.Node) (Profile, error) {
	if n == nil || n.Tag == "!!null" {
		return p, nil
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!int" || !decimalRE.MatchString(n.Value) {
		return Profile{}, fmt.Errorf("%w: max_turns must be a plain decimal integer", ErrInvalidProfile)
	}
	turns, err := strconv.Atoi(n.Value)
	if err != nil || turns < 1 || turns > MaxTurnsLimit {
		return Profile{}, fmt.Errorf("%w: max_turns %s is outside 1..%d", ErrInvalidProfile, n.Value, MaxTurnsLimit)
	}
	p.MaxTurns = turns
	return p, nil
}

// toolNames reads the tools sequence; every entry must be a string scalar.
func toolNames(n *yaml.Node) ([]string, error) {
	if n == nil || n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: tools must be a list", ErrInvalidProfile)
	}
	names := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		name, err := text(item, "a tools entry")
		if err != nil {
			return nil, err
		}
		if item.Tag == "!!null" {
			return nil, fmt.Errorf("%w: a tools entry must be a string", ErrInvalidProfile)
		}
		names = append(names, name)
	}
	return names, nil
}

// checkTools rejects unregistered and duplicate tool names.
func checkTools(tools, known []string) ([]string, error) {
	registered := make(map[string]bool, len(known))
	for _, k := range known {
		registered[k] = true
	}
	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		if !registered[t] {
			return nil, fmt.Errorf("%w: tool %q is not registered", ErrInvalidProfile, t)
		}
		if seen[t] {
			return nil, fmt.Errorf("%w: tool %q is listed twice", ErrInvalidProfile, t)
		}
		seen[t] = true
	}
	return append([]string{}, tools...), nil
}

// LoadFile reads and validates one profile file. The file must be a regular
// file (a symlink, directory or fifo is refused) of at most MaxFileBytes, read
// through a MaxFileBytes+1 limit so a file that grows is still refused. The
// file is opened through os.OpenRoot on its directory and checked to be the
// same file that was inspected, so a swap between check and read is refused.
func LoadFile(path string, source Source, known []string) (Profile, error) {
	data, err := readRegular(path)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %s: %v", ErrInvalidProfile, path, err)
	}
	p, err := Parse(data, known)
	if err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	p.Path, p.Source, p.SHA256 = path, source, hex.EncodeToString(sum[:])
	return p, nil
}

// readRegular returns the bytes of a regular, size-capped file.
func readRegular(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	base := filepath.Base(path)
	linfo, err := root.Lstat(base)
	if err != nil {
		return nil, err
	}
	if !linfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (%s)", linfo.Mode().Type())
	}
	f, err := root.Open(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	finfo, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(linfo, finfo) {
		return nil, errors.New("changed while being read")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("larger than %d bytes", MaxFileBytes)
	}
	return data, nil
}

// LoadDir loads every visible *.md entry directly in dir (no recursion, no
// hidden files, other names ignored), sorted by name. A missing directory is
// empty, not an error; an unreadable one is. An entry named *.md that is not a
// regular file fails, and so do two files declaring the same profile name.
func LoadDir(dir string, source Source, known []string) ([]Profile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidProfile, dir, err)
	}
	byName := map[string]Profile{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if hidden(e.Name()) {
			return nil, fmt.Errorf("%w: %q: file name holds a control, format or separator character", ErrInvalidProfile, e.Name())
		}
		p, lerr := LoadFile(filepath.Join(dir, e.Name()), source, known)
		if lerr != nil {
			return nil, lerr
		}
		if first, dup := byName[p.Name]; dup {
			return nil, fmt.Errorf("%w: %s and %s both declare %q", ErrInvalidProfile, first.Path, p.Path, p.Name)
		}
		byName[p.Name] = p
	}
	return sorted(byName), nil
}

// Merge combines user and project profiles sorted by name. A name present in
// both is refused, naming both files: a repository profile must never replace a
// trusted user profile, so there is no override.
func Merge(user, project []Profile) ([]Profile, error) {
	byName := make(map[string]Profile, len(user)+len(project))
	for _, p := range user {
		byName[p.Name] = p
	}
	for _, p := range project {
		if u, ok := byName[p.Name]; ok {
			return nil, fmt.Errorf("%w: %s and %s both declare %q; --agents both refuses a conflict",
				ErrInvalidProfile, u.Path, p.Path, p.Name)
		}
		byName[p.Name] = p
	}
	return sorted(byName), nil
}

func sorted(byName map[string]Profile) []Profile {
	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load reads the directories the scope selects and merges them; a name found in
// both is an error. Directories
// that the scope does not select are not touched, so a default (user) load
// never reads repository-controlled profiles.
func Load(scope Scope, userDir, projectDir string, known []string) ([]Profile, error) {
	var user, project []Profile
	var err error
	if scope == ScopeUser || scope == ScopeBoth {
		if user, err = LoadDir(userDir, SourceUser, known); err != nil {
			return nil, err
		}
	}
	if scope == ScopeProject || scope == ScopeBoth {
		if project, err = LoadDir(projectDir, SourceProject, known); err != nil {
			return nil, err
		}
	}
	return Merge(user, project)
}
