package tool

// Protected trees (ADR-012 D20): fs.write and fs.edit refuse, by identity rather than by workdir prefix, a target in
// a hook-config directory. Three fail-closed rules: (a) a `.flowstate` path component (case-folded), (b) the target's
// physically resolved path lies in a protected directory, (c) the final component is a symlink, a multiply-linked
// file is the same file as one under a protected directory, or the target is what a symlink inside one points at, resolved by hand when it dangles. The walk and the write are two calls, so these rules are
// best-effort against a concurrent actor; os.Root stays the final authority on confinement.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// protectedName is the directory name rule (a) matches, case-folded.
const protectedName = ".flowstate"

// maxProtectedFiles caps the recursive listing of one protected directory; beyond it a multiply-linked target is
// refused.
const maxProtectedFiles = 1024

// maxProtectedEntries is a variable only so that a spec can lower it: the bound of the separate symlink pass over
// one protected directory. Beyond it every write is refused.
var maxProtectedEntries = 65536

// ErrPathProtected reports a write to a protected tree.
var ErrPathProtected = errors.New("tool: path is in a protected directory")

// WithProtectedDirs turns on the protected-tree refusal for fs.write and fs.edit. dirs are the absolute protected
// directories (the project root's .flowstate and the user flowstate config directory); they need not exist yet.
// A relative entry is made absolute against the process working directory (fail closed rather than protecting
// nothing); an empty entry is ignored, since it names no directory. Once the option is given the `.flowstate` name
// rule applies even when dirs is empty. Without it nothing is refused.
func WithProtectedDirs(dirs ...string) FSOption {
	return func(o *fsOptions) {
		kept := make([]string, 0, len(dirs))
		for _, d := range dirs {
			if d == "" {
				continue
			}
			if abs, err := filepath.Abs(d); err == nil {
				d = abs
			}
			kept = append(kept, d)
		}
		o.protect = &fsProtect{dirs: kept}
	}
}

// refuse builds the typed refusal for rel.
func refuse(rel, why string) error {
	return fmt.Errorf("%w: %s (%s)", ErrPathProtected, rel, why)
}

// checkProtected returns nil when p is nil or rel is writable, ErrPathProtected for a protected target, and any
// os.Root error (an escape maps to ErrPathOutsideWorkdir) the walk meets.
func checkProtected(root *os.Root, workdir, rel string, p *fsProtect) error {
	if p == nil {
		return nil
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return err
	}
	workdir = abs
	slashed := filepath.ToSlash(rel)
	if last := slashed[strings.LastIndex(slashed, "/")+1:]; last == "" || last == "." || last == ".." {
		// A write to a directory path never succeeds, but MkdirAll would first create what a dangling link names.
		return refuse(rel, "a directory path (trailing /, . or ..)")
	}
	comps := pathComponents(rel)
	for _, c := range comps {
		if strings.EqualFold(c, protectedName) {
			return refuse(rel, "a .flowstate component")
		}
	}
	if err := checkFinalLink(root, strings.Join(comps, "/")); err != nil {
		return err
	}
	return checkResolved(root, workdir, comps, rel, p)
}

// pathComponents splits rel into its non-empty, non-"." components without cleaning ".." away.
func pathComponents(rel string) []string {
	var out []string
	for _, c := range strings.Split(filepath.ToSlash(rel), "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

// checkFinalLink refuses a final component that is a symlink, whatever it points to and whether or not it dangles.
func checkFinalLink(root *os.Root, rel string) error {
	info, err := root.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return mapRootErr(err)
	case info.Mode()&fs.ModeSymlink != 0:
		return refuse(rel, "symlinked files are not writable by the file tools")
	}
	return nil
}

// checkResolved refuses a target whose resolved path equals or lies under a protected directory's resolved path, or
// whose existing ancestors are the same directory as a protected one.
func checkResolved(root *os.Root, workdir string, comps []string, rel string, p *fsProtect) error {
	existing, err := existingPrefix(root, comps)
	if err != nil {
		return err
	}
	for _, c := range comps[len(existing):] {
		if c == ".." {
			// The remainder is joined lexically, but after MkdirAll ".." would step back onto the existing tree and
			// a later symlink would resolve physically, past the key.
			return refuse(rel, "a .. component after a missing directory")
		}
	}
	key := workdir
	if len(existing) > 0 {
		key += string(filepath.Separator) + strings.Join(existing, string(filepath.Separator))
	}
	target := filepath.Join(resolvePath(key), filepath.Join(append([]string{"."}, comps[len(existing):]...)...))
	for _, dir := range p.dirs {
		if within(target, resolvePath(dir)) {
			return refuse(rel, "resolves into a protected directory")
		}
	}
	if err := sameAsProtected(root, existing, p); err != nil {
		return err
	}
	return checkContents(root, rel, target, p)
}

// existingPrefix returns the leading components that exist, resolving ".." and links physically through root. A
// dangling symlink in any position is refused: it is neither existing nor creatable.
func existingPrefix(root *os.Root, comps []string) ([]string, error) {
	for i := range comps {
		prefix := strings.Join(comps[:i+1], "/")
		_, err := root.Stat(prefix)
		if err == nil {
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, mapRootErr(err)
		}
		if li, lerr := root.Lstat(prefix); lerr == nil && li.Mode()&fs.ModeSymlink != 0 {
			return nil, refuse(strings.Join(comps, "/"), "dangling directory symlink")
		}
		return comps[:i], nil
	}
	return comps, nil
}

// sameAsProtected compares every existing prefix with os.SameFile against each protected directory that exists.
func sameAsProtected(root *os.Root, existing []string, p *fsProtect) error {
	for _, dir := range p.dirs {
		want, err := os.Stat(dir)
		if err != nil {
			continue
		}
		for i := range existing {
			got, serr := root.Stat(strings.Join(existing[:i+1], "/"))
			if serr == nil && os.SameFile(got, want) {
				return refuse(strings.Join(existing, "/"), "same directory as a protected one")
			}
		}
	}
	return nil
}

// within reports whether path equals or lies under dir, comparing each component case-folded on every platform: a
// case-insensitive filesystem may name an absent directory with another case, and over-refusal is harmless.
func within(path, dir string) bool {
	p, d := splitPath(path), splitPath(dir)
	if len(p) < len(d) {
		return false
	}
	for i := range d {
		if !strings.EqualFold(p[i], d[i]) {
			return false
		}
	}
	return true
}

// splitPath cleans p and returns its non-empty components.
func splitPath(p string) []string {
	var out []string
	for _, c := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// checkContents scans each protected directory and refuses a target that (1) lies under, or is, the resolved
// target of a symlink inside the directory (a dangling link resolves by hand to the path it would create), (2) is
// the same file as a symlinked file's target or as a file under a symlinked directory's target (hard links
// included), or (3) is a multiply-linked regular file sharing an inode with a file in the directory. Past the
// hard-link listing cap (maxProtectedFiles) a multiply-linked target is refused; the symlink pass has its own
// bound (maxProtectedEntries) beyond which every write is refused. An unreadable entry refuses (fail closed). A
// directory that does not exist holds nothing and costs one failed lstat.
func checkContents(root *os.Root, rel, target string, p *fsProtect) error {
	info, err := root.Stat(rel)
	if err != nil {
		info = nil
	}
	multi := info != nil && info.Mode().IsRegular() && linkCount(info) >= 2
	for _, dir := range p.dirs {
		if why := scanProtected(resolvePath(dir), target, info, multi); why != "" {
			return refuse(rel, why)
		}
	}
	return nil
}

// scanProtected returns the refusal reason for the target in dir (already resolved: WalkDir does not follow a root
// symlink), or "" when none applies. Symlinks are collected by their own pass, which the file cap does not limit;
// the hard-link listing runs only for a multiply-linked target.
func scanProtected(dir, target string, info fs.FileInfo, multi bool) string {
	if why := scanSymlinks(dir, target, info, multi); why != "" {
		return why
	}
	if multi {
		return scanHardLinks(dir, info)
	}
	return ""
}

// scanSymlinks walks dir, resolving every symlink against the target; a link to a directory has that directory
// walked too (transitive links), each resolved directory once. Past maxProtectedEntries entries in all, the pass
// stops and every write is refused (fail closed): a link that was not seen could name the target.
func scanSymlinks(dir, target string, info fs.FileInfo, multi bool) string {
	s := &linkScan{target: target, info: info, multi: multi, seen: map[string]bool{dirKey(dir): true}}
	s.walk(dir)
	return s.why
}

// dirKey is the visited-set key of a resolved directory: the exact cleaned path. It never folds case, since two
// directories differing only in case are distinct on a case-sensitive filesystem (within folds, only to compare).
func dirKey(dir string) string { return filepath.Clean(dir) }

// walk visits dir without following links, resolving each symlink it holds.
func (s *linkScan) walk(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				s.why = "unreadable entry in a protected directory"
			}
			return fs.SkipAll
		}
		s.n++
		if s.n > maxProtectedEntries {
			s.why = "too many entries in a protected directory to collect its symlinks"
			return fs.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if s.why = s.verdict(path); s.why != "" {
				return fs.SkipAll
			}
		}
		return nil
	})
}

// scanHardLinks lists dir (at most maxProtectedFiles files and as many directories) and refuses a file sharing
// info's inode; past a cap the multiply-linked target is refused (fail closed).
func scanHardLinks(dir string, info fs.FileInfo) string {
	why, files, dirs := "", 0, 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				why = "unreadable entry in a protected directory"
			}
			return fs.SkipAll
		}
		n := &files
		if d.IsDir() {
			n = &dirs
		}
		*n++
		if *n > maxProtectedFiles {
			why = "hard link to a protected file (listing cap exceeded)"
			return fs.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 || d.IsDir() {
			return nil
		}
		if fi, ierr := d.Info(); ierr == nil && os.SameFile(fi, info) {
			why = "hard link to a protected file"
			return fs.SkipAll
		}
		return nil
	})
	return why
}

// verdict resolves a symlink found in a protected directory and reports whether the target is what it points at:
// under it for a directory or a path that does not exist yet, the same file for a file (also one under a
// directory's target when the target is multiply linked). A directory's own symlinks are scanned in turn.
func (s *linkScan) verdict(link string) string {
	target, info, multi := s.target, s.info, s.multi
	res, state := resolveLink(link)
	switch state {
	case linkLoop:
		return "" // nothing can be read or created through a loop
	case linkUnknown:
		if within(target, res) {
			return "resolves into an unresolvable symlink target in a protected directory"
		}
		return ""
	}
	st, err := os.Stat(res)
	switch {
	case err != nil:
		if within(target, res) {
			return "resolves into the target of a dangling symlink in a protected directory"
		}
	case st.IsDir():
		if within(target, res) {
			return "resolves into the target of a symlink in a protected directory"
		}
		if multi && sharesInode(res, info) {
			return "hard link to a file under the target of a symlink in a protected directory"
		}
		if key := dirKey(res); !s.seen[key] {
			s.seen[key] = true
			s.walk(res)
			return s.why
		}
	case within(target, res) || (info != nil && os.SameFile(info, st)):
		return "is the target of a symlinked protected file"
	}
	return ""
}

// sharesInode reports whether a file under dir (walked without following links, capped) is info's file; a walk
// error or the cap answers true (fail closed).
func sharesInode(dir string, info fs.FileInfo) bool {
	hit, n := false, 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		n++
		if err != nil || n > 2*maxProtectedFiles {
			hit = true
			return fs.SkipAll
		}
		if fi, ierr := d.Info(); ierr == nil && !d.IsDir() && os.SameFile(fi, info) {
			hit = true
			return fs.SkipAll
		}
		return nil
	})
	return hit
}
