package skill

import (
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
	res := Result{Skills: []Skill{}, Diagnostics: []Diagnostic{}}
	byName := map[string][]Skill{}
	for _, root := range roots {
		found, diags := loadRoot(root)
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
func loadRoot(root Root) ([]Skill, []Diagnostic) {
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
		s, found, derr := loadOne(r, root, name)
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
	s.Source, s.Path = root.Source, path
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
