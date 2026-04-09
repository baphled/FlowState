package hook

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SkillContentCache pre-scans a skills directory on Init and caches each skill's
// content and byte size for efficient retrieval and budget enforcement.
type SkillContentCache struct {
	dir      string
	mu       sync.RWMutex
	contents map[string]string
	sizes    map[string]int
	total    int
}

// NewSkillContentCache returns a new SkillContentCache rooted at dir.
//
// Expected:
//   - dir is the path to the skills directory to scan.
//
// Returns:
//   - A cache initialised with empty content and size maps.
//
// Side effects:
//   - None.
func NewSkillContentCache(dir string) *SkillContentCache {
	return &SkillContentCache{
		dir:      dir,
		contents: map[string]string{},
		sizes:    map[string]int{},
	}
}

// Init scans all {name}/SKILL.md files in the cache directory, strips YAML frontmatter,
// and populates the content and byte-size maps.
//
// Expected:
//   - c has been created by NewSkillContentCache.
//
// Returns:
//   - nil when the cache is refreshed successfully or the directory does not exist.
//   - A non-nil error when the directory cannot be read.
//
// Side effects:
//   - Reads the filesystem and replaces any cached content.
func (c *SkillContentCache) Init() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.contents = map[string]string{}
	c.sizes = map[string]int{}
	c.total = 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(c.dir, entry.Name(), "SKILL.md")
		data, readErr := os.ReadFile(skillPath)
		if readErr != nil {
			continue
		}
		content := stripYAMLFrontmatter(string(data))
		size := len(content)
		c.contents[entry.Name()] = content
		c.sizes[entry.Name()] = size
		c.total += size
	}
	return nil
}

// GetContent returns the cached content for the named skill.
//
// Expected:
//   - name is the skill identifier to look up.
//
// Returns:
//   - The cached content and true when the skill is present.
//   - "" and false when the skill is not in the cache.
//
// Side effects:
//   - None.
func (c *SkillContentCache) GetContent(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.contents[name]
	return v, ok
}

// HasSkill reports whether the named skill is present in the cache.
//
// Expected:
//   - name is the skill identifier to look up.
//
// Returns:
//   - true if the skill is cached, false otherwise.
//
// Side effects:
//   - None.
func (c *SkillContentCache) HasSkill(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.contents[name]
	return ok
}

// ByteSize returns the byte size of the cached content for the named skill.
//
// Expected:
//   - name is the skill identifier to look up.
//
// Returns:
//   - The cached byte size for the skill, or 0 when the skill is not cached.
//
// Side effects:
//   - None.
func (c *SkillContentCache) ByteSize(name string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sizes[name]
}

// TotalBytes returns the aggregate byte size of all cached skill content.
//
// Expected:
//   - None.
//
// Returns:
//   - The sum of all cached skill content byte sizes.
//
// Side effects:
//   - None.
func (c *SkillContentCache) TotalBytes() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.total
}

// AllNames returns a slice containing every skill name currently in the cache.
//
// Expected:
//   - None.
//
// Returns:
//   - A slice of cached skill names in undefined order.
//
// Side effects:
//   - None.
func (c *SkillContentCache) AllNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.contents))
	for name := range c.contents {
		names = append(names, name)
	}
	return names
}

// stripYAMLFrontmatter removes the leading --- ... --- block from s, if present.
//
// Expected:
//   - s contains raw skill file content.
//
// Returns:
//   - The content body with leading YAML frontmatter removed when present, or s unchanged otherwise.
//
// Side effects:
//   - None.
func stripYAMLFrontmatter(s string) string {
	if !strings.HasPrefix(strings.TrimSpace(s), "---") {
		return s
	}
	scanner := bufio.NewScanner(strings.NewReader(s))
	var body []string
	closedFrontmatter := false
	openSeen := false
	for scanner.Scan() {
		line := scanner.Text()
		if !openSeen && strings.TrimSpace(line) == "---" {
			openSeen = true
			continue
		}
		if openSeen && !closedFrontmatter && strings.TrimSpace(line) == "---" {
			closedFrontmatter = true
			continue
		}
		if closedFrontmatter {
			body = append(body, line)
		}
	}
	if !closedFrontmatter {
		return s
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}
