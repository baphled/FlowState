package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Load scans each root one level deep (<dir>/<name>/SKILL.md) and validates
// every skill directory. A missing root is empty. A skill that fails is not
// loaded and is reported; it never blocks the others. A name found twice, in
// any roots, is an error naming every path, and none of the copies is loaded:
// a silent shadow of a skill that enforcement may name would be a hole.
func Load(roots []Root) Result {
	return LoadCached(roots, nil)
}

// LoadCached is Load through a Cache (ADR-013 D17): a SKILL.md whose path, mtime and size are remembered is
// neither re-read nor re-hashed, its cached conclusions returned. A nil cache reads everything, as Load does.
func LoadCached(roots []Root, cache *Cache) Result {
	res := Result{Skills: []Skill{}, Diagnostics: []Diagnostic{}}
	byName := map[string][]Skill{}
	for _, root := range roots {
		found, diags := loadRoot(root, cache)
		res.Diagnostics = append(res.Diagnostics, diags...)
		for _, s := range found {
			byName[s.Name] = append(byName[s.Name], s)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		copies := byName[name]
		if len(copies) == 1 {
			res.Skills = append(res.Skills, copies[0])
			continue
		}
		paths := make([]string, len(copies))
		for i, c := range copies {
			paths[i] = c.Path
		}
		res.Diagnostics = append(res.Diagnostics, diag(SeverityError, paths[0],
			"duplicate skill name %q in %s", name, strings.Join(paths, " and ")))
	}
	return res
}

// diag builds a Diagnostic.
func diag(sev Severity, path, format string, args ...any) Diagnostic {
	return Diagnostic{Severity: sev, Path: path, Message: fmt.Sprintf(format, args...)}
}

// loadRoot validates every skill directory directly inside one root.
func loadRoot(root Root, cache *Cache) ([]Skill, []Diagnostic) {
	// Stat first: opening a FIFO as the root would block.
	info, err := os.Stat(root.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil && !info.IsDir() {
		return nil, []Diagnostic{diag(SeverityError, root.Dir, "cannot open the skills directory: not a directory (%s)", info.Mode().Type())}
	}
	var r *os.Root
	if err == nil {
		r, err = os.OpenRoot(root.Dir)
	}
	if err != nil {
		return nil, []Diagnostic{diag(SeverityError, root.Dir, "cannot open the skills directory: %v", err)}
	}
	defer func() { _ = r.Close() }()
	entries, err := readEntries(r)
	if err != nil {
		return nil, []Diagnostic{diag(SeverityError, root.Dir, "cannot list the skills directory: %v", err)}
	}
	var skills []Skill
	var diags []Diagnostic
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(root.Dir, name)
		info, serr := r.Stat(name)
		if serr != nil {
			diags = append(diags, diag(SeverityError, path, "not readable inside the skills directory: %v", serr))
			continue
		}
		if !info.IsDir() {
			continue
		}
		s, found, derr := loadCached(r, root, name, cache)
		diags = append(diags, found...)
		if derr != nil {
			diags = append(diags, diag(SeverityError, filepath.Join(path, FileName), "%s", strings.TrimPrefix(derr.Error(), ErrInvalidSkill.Error()+": ")))
			continue
		}
		skills = append(skills, s)
	}
	return skills, diags
}

// readEntries lists the directory entries of an opened root, sorted by name.
func readEntries(r *os.Root) ([]fs.DirEntry, error) {
	d, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// loadCached loads the skill directory name below r through the cache: a SKILL.md whose path, mtime and size
// are remembered is not re-read, its skill, warnings or failure returned as concluded; anything else is loaded and
// remembered. A missing or non-regular SKILL.md is loadOne's error and is not remembered.
func loadCached(r *os.Root, root Root, name string, cache *Cache) (Skill, []Diagnostic, error) {
	path := filepath.Join(root.Dir, name, FileName)
	info, err := r.Stat(filepath.Join(name, FileName))
	if err != nil || !info.Mode().IsRegular() {
		return loadOne(r, root, name)
	}
	if cache != nil {
		if e, ok := cache.entries[path]; ok && e.size == info.Size() && e.mtime.Equal(info.ModTime()) {
			return e.skill, e.diags, e.failed
		}
	}
	s, diags, err := loadOne(r, root, name)
	if cache != nil {
		if cache.entries == nil {
			cache.entries = map[string]cacheEntry{}
		}
		cache.entries[path] = cacheEntry{mtime: info.ModTime(), size: info.Size(), skill: s, diags: diags, failed: err}
	}
	return s, diags, err
}

// Current is the skill as its SKILL.md is now, through the cache: the per-turn refresh of a required skill
// (ADR-013 D17); ok is false when it is gone, not regular, or no longer valid. An unchanged file costs one stat.
func (c *Cache) Current(s Skill) (Skill, bool) {
	if s.Path == "" {
		return Skill{}, false
	}
	root := Root{Dir: filepath.Dir(filepath.Dir(s.Path)), Source: s.Source}
	r, err := os.OpenRoot(root.Dir)
	if err != nil {
		return Skill{}, false
	}
	defer func() { _ = r.Close() }()
	now, _, err := loadCached(r, root, filepath.Base(filepath.Dir(s.Path)), c)
	return now, err == nil
}

// Fresh is Current read again whatever the cache remembers, and remembered in its place: what a session asks of a
// skill whose cached hash is apart from a load's, as after a same-size rewrite under the old mtime, so both converge.
func (c *Cache) Fresh(s Skill) (Skill, bool) {
	delete(c.entries, s.Path) // entries are keyed by the path, as the lookup above is
	return c.Current(s)
}

// loadOne validates the skill directory name below r; the warnings it found
// are returned even when the skill fails.
func loadOne(r *os.Root, root Root, name string) (Skill, []Diagnostic, error) {
	path := filepath.Join(root.Dir, name, FileName)
	data, err := readFile(r, filepath.Join(name, FileName))
	if err != nil {
		return Skill{}, nil, fmt.Errorf("%w: %v", ErrInvalidSkill, err)
	}
	s, ignored, err := Parse(data)
	if err != nil {
		return Skill{}, nil, err
	}
	if s.Name != name {
		return Skill{}, nil, fmt.Errorf("%w: name %q does not match its directory %q", ErrInvalidSkill, s.Name, name)
	}
	sum := sha256.Sum256(data)
	s.Source, s.Path, s.SHA256, s.Bytes = root.Source, path, hex.EncodeToString(sum[:]), len(data)
	var diags []Diagnostic
	if len(ignored) > 0 {
		diags = append(diags, diag(SeverityWarning, path, "ignored, not supported: %s", strings.Join(ignored, ", ")))
	}
	return s, append(diags, checkSupporting(r, root, name)...), nil
}

// readFile returns the bytes of a regular file inside r, read through a
// MaxFileBytes+1 limit so a file that grows is still seen as too large. A
// symlink is followed only while it stays inside r. The mode is checked before
// the open (a FIFO would block it) and again after, against a swap in between.
func readFile(r *os.Root, rel string) ([]byte, error) {
	pre, err := r.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("SKILL.md is missing")
		}
		return nil, err
	}
	if !pre.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (%s)", pre.Mode().Type())
	}
	f, err := openFile(r, rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (%s)", info.Mode().Type())
	}
	return io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
}

// checkSupporting warns about files below the skill directory that
// skill.load would refuse: a path outside the segment charset, or a file over
// the size cap. Only regular files are judged; at most MaxSupportingScan entries are visited.
func checkSupporting(r *os.Root, root Root, name string) []Diagnostic {
	sub, err := r.OpenRoot(name)
	if err != nil {
		return []Diagnostic{diag(SeverityWarning, filepath.Join(root.Dir, name), "supporting files not checked: %v", err)}
	}
	defer func() { _ = sub.Close() }()
	var diags []Diagnostic
	seen := 0
	_ = fs.WalkDir(sub.FS(), ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || p == "." || p == FileName {
			return nil
		}
		seen++
		if seen > MaxSupportingScan {
			diags = append(diags, diag(SeverityWarning, filepath.Join(root.Dir, name), "more than %d entries: the rest were not checked", MaxSupportingScan))
			return fs.SkipAll
		}
		if !d.Type().IsRegular() {
			return nil
		}
		full := filepath.Join(root.Dir, name, filepath.FromSlash(p))
		if perr := ValidSupportPath(p); perr != nil {
			diags = append(diags, diag(SeverityWarning, full, "skill.load will refuse this file: its path is outside the allowed characters"))
		} else if info, ierr := d.Info(); ierr == nil && info.Size() > MaxFileBytes {
			diags = append(diags, diag(SeverityWarning, full, "skill.load will refuse this file: larger than %d bytes", MaxFileBytes))
		}
		return nil
	})
	return diags
}
