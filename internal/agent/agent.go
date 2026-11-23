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
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrInvalidProfile marks a profile file or directory that fails validation.
var ErrInvalidProfile = errors.New("agent: invalid profile")

// ErrInvalidScope marks a scope that is not user, project or both.
var ErrInvalidScope = errors.New("agent: invalid scope")

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

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
// next line that is exactly "---". An unknown field, a tool not in known, a
// duplicate tool, an out-of-range max_turns, a bad name, a missing description
// and an empty body each fail. An empty body is a failure on purpose: a profile
// with no instructions is a mistake. Surrounding whitespace is trimmed from the
// body. Path, Source and SHA256 are set by the loader, not here.
func Parse(data []byte, known []string) (Profile, error) {
	header, body, err := split(data)
	if err != nil {
		return Profile{}, err
	}
	var fm frontmatter
	dec := yaml.NewDecoder(bytes.NewReader(header))
	dec.KnownFields(true)
	if derr := dec.Decode(&fm); derr != nil && !errors.Is(derr, io.EOF) {
		return Profile{}, fmt.Errorf("%w: frontmatter: %v", ErrInvalidProfile, derr)
	}
	p, err := build(fm, known)
	if err != nil {
		return Profile{}, err
	}
	p.Body = strings.TrimSpace(string(body))
	if p.Body == "" {
		return Profile{}, fmt.Errorf("%w: the body (system prompt) is empty", ErrInvalidProfile)
	}
	return p, nil
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
		if string(bytes.TrimRight(line, "\r")) == "---" {
			return rest[:offset], rest[next:], nil
		}
		offset = next
	}
	return nil, nil, fmt.Errorf("%w: frontmatter is not closed by a --- line", ErrInvalidProfile)
}

// build applies the field rules to a decoded header.
func build(fm frontmatter, known []string) (Profile, error) {
	p := Profile{Model: InheritModel, MaxTurns: DefaultMaxTurns}
	if fm.Name == nil || !nameRE.MatchString(*fm.Name) {
		return Profile{}, fmt.Errorf("%w: name must match %s", ErrInvalidProfile, nameRE)
	}
	p.Name = *fm.Name
	if fm.Description == nil || strings.TrimSpace(*fm.Description) == "" {
		return Profile{}, fmt.Errorf("%w: description is required", ErrInvalidProfile)
	}
	p.Description = *fm.Description
	tools, err := checkTools(fm.Tools, known)
	if err != nil {
		return Profile{}, err
	}
	p.Tools = tools
	if fm.Model != nil && *fm.Model != "" {
		p.Model = *fm.Model
	}
	if fm.MaxTurns != nil {
		if *fm.MaxTurns < 1 || *fm.MaxTurns > MaxTurnsLimit {
			return Profile{}, fmt.Errorf("%w: max_turns %d is outside 1..%d", ErrInvalidProfile, *fm.MaxTurns, MaxTurnsLimit)
		}
		p.MaxTurns = *fm.MaxTurns
	}
	return p, nil
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
// file (a symlink, directory or fifo is refused) of at most MaxFileBytes. The
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
	if linfo.Size() > MaxFileBytes {
		return nil, fmt.Errorf("larger than %d bytes", MaxFileBytes)
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

// Merge combines user and project profiles: a project profile replaces a user
// profile of the same name and records Overrides: SourceUser. The result is
// sorted by name.
func Merge(user, project []Profile) []Profile {
	byName := make(map[string]Profile, len(user)+len(project))
	for _, p := range user {
		byName[p.Name] = p
	}
	for _, p := range project {
		if _, ok := byName[p.Name]; ok {
			p.Overrides = SourceUser
		}
		byName[p.Name] = p
	}
	return sorted(byName)
}

func sorted(byName map[string]Profile) []Profile {
	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load reads the directories the scope selects and merges them. Directories
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
	return Merge(user, project), nil
}
