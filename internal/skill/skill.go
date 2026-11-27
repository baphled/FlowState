package skill

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ErrInvalidSkill marks a skill file or directory that fails validation.
var ErrInvalidSkill = errors.New("skill: invalid skill")

var nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName reports whether name follows the loader's rule: lowercase words joined by single hyphens, at most
// MaxNameChars. A Windows device word (nul, aux...) is a valid name: a skill directory so named loads on Unix.
func ValidName(name string) bool { return nameRE.MatchString(name) && len(name) <= MaxNameChars }

// reservedRE matches a Windows device name, with or without an extension.
var reservedRE = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com\d|lpt\d)(\..*)?$`)

var segmentRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// acceptedKey lists the keys the standard defines that are accepted and ignored
// without a diagnostic; every other key is ignored with a warning.
var acceptedKey = map[string]bool{"license": true, "compatibility": true, "metadata": true}

// Parse validates the bytes of one SKILL.md. The file must start with "---" at
// byte 0 and the frontmatter ends at the next line that is exactly "---" (CRLF
// tolerated). The frontmatter is one YAML document with no explicit tag and no
// duplicate key; name and description are required single-line strings with no
// control, format or separator character. Any other key is dropped and returned
// in ignored (sorted); it is never an error. The file is at most MaxFileBytes
// and MaxFileLines, and its JSON-marshalled body fits MaxResultBytes. The body
// is UTF-8 without control characters but tab, newline and carriage return, without bidi controls or Unicode tag
// characters, and does not contain CloseFence. Path and Source are set by the
// loader; the name is checked against the directory there.
func Parse(data []byte) (s Skill, ignored []string, err error) {
	if len(data) > MaxFileBytes || lineCount(data) > MaxFileLines {
		return Skill{}, nil, fmt.Errorf("%w: larger than %d bytes or %d lines", ErrInvalidSkill, MaxFileBytes, MaxFileLines)
	}
	header, body, err := split(data)
	if err != nil {
		return Skill{}, nil, err
	}
	if cerr := ValidText(body, "the body"); cerr != nil {
		return Skill{}, nil, cerr
	}
	if bytes.Contains(body, []byte(CloseFence)) {
		return Skill{}, nil, fmt.Errorf("%w: the body contains the closing fence %s", ErrInvalidSkill, CloseFence)
	}
	if marshalled, merr := json.Marshal(string(body)); merr != nil || len(marshalled)+frameReserve > MaxResultBytes {
		return Skill{}, nil, fmt.Errorf("%w: the framed result would exceed %d bytes once marshalled", ErrInvalidSkill, MaxResultBytes)
	}
	fields, ignored, err := decodeHeader(header)
	if err != nil {
		return Skill{}, nil, err
	}
	if s.Name, err = required(fields, "name"); err != nil {
		return Skill{}, nil, err
	}
	if !ValidName(s.Name) {
		return Skill{}, nil, fmt.Errorf("%w: name must match %s and be at most %d characters", ErrInvalidSkill, nameRE, MaxNameChars)
	}
	if s.Description, err = required(fields, "description"); err != nil {
		return Skill{}, nil, err
	}
	if utf8.RuneCountInString(s.Description) > MaxDescriptionChars {
		return Skill{}, nil, fmt.Errorf("%w: description is longer than %d characters", ErrInvalidSkill, MaxDescriptionChars)
	}
	s.Body = strings.TrimSpace(string(body))
	return s, ignored, nil
}

// Body validates a freshly read SKILL.md exactly as Parse does, refusing what Parse refuses, and returns the
// name (for skill.load to hold against the skill it was asked for) and the body without the frontmatter.
func Body(data []byte) (name, body string, err error) {
	s, _, err := Parse(data)
	return s.Name, s.Body, err
}

// lineCount counts lines; a final line without a newline still counts.
func lineCount(data []byte) int {
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// required reads a mandatory single-line string field.
func required(fields map[string]*yaml.Node, key string) (string, error) {
	n := fields[key]
	if n == nil || n.Tag == "!!null" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidSkill, key)
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return "", fmt.Errorf("%w: %s must be a string", ErrInvalidSkill, key)
	}
	if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return "", fmt.Errorf("%w: %s must be a single line: block scalars are not allowed", ErrInvalidSkill, key)
	}
	if hidden(strings.ReplaceAll(n.Value, "\t", "")) {
		return "", fmt.Errorf("%w: %s holds a control, format or separator character", ErrInvalidSkill, key)
	}
	if strings.TrimSpace(n.Value) == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidSkill, key)
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

// ValidText refuses text that is not UTF-8, or holds a control character other than tab, newline and carriage return,
// a bidirectional control or a Unicode tag character: the rule Parse holds a body to and skill.load a supporting file.
// Other format characters (ZWJ, ZWNJ) stay allowed: emoji text uses them. what names the text in the error.
func ValidText(text []byte, what string) error {
	if !utf8.Valid(text) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidSkill, what)
	}
	for _, r := range string(text) {
		if refused(r) {
			return fmt.Errorf("%w: %s holds a control, bidirectional or tag character (U+%04X)", ErrInvalidSkill, what, r)
		}
	}
	return nil
}

// refused reports whether r is a control other than tab, newline and carriage return, a bidi control or a tag character.
func refused(r rune) bool {
	return (unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r') || r == 0x61c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || (r >= 0xe0000 && r <= 0xe007f)
}

// decodeHeader parses the frontmatter into its top-level value nodes and the
// sorted names of the keys that are ignored with a warning.
func decodeHeader(header []byte) (map[string]*yaml.Node, []string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(header))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, fmt.Errorf("%w: the frontmatter is empty", ErrInvalidSkill)
		}
		return nil, nil, fmt.Errorf("%w: frontmatter: %v", ErrInvalidSkill, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%w: frontmatter must be exactly one YAML document", ErrInvalidSkill)
	}
	if err := rejectTags(&doc); err != nil {
		return nil, nil, err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%w: frontmatter must be a mapping", ErrInvalidSkill)
	}
	fields := make(map[string]*yaml.Node, len(root.Content)/2)
	var ignored []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind != yaml.ScalarNode {
			return nil, nil, fmt.Errorf("%w: a frontmatter key must be a string", ErrInvalidSkill)
		}
		if fields[key.Value] != nil {
			return nil, nil, fmt.Errorf("%w: field %q is set twice", ErrInvalidSkill, key.Value)
		}
		fields[key.Value] = root.Content[i+1]
		if key.Value != "name" && key.Value != "description" && !acceptedKey[key.Value] {
			ignored = append(ignored, key.Value)
		}
	}
	sort.Strings(ignored)
	return fields, ignored, nil
}

// rejectTags refuses an explicit YAML tag anywhere in the tree.
func rejectTags(n *yaml.Node) error {
	if n.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("%w: explicit YAML tag %q is not allowed", ErrInvalidSkill, n.Tag)
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
		return nil, nil, fmt.Errorf("%w: frontmatter must start with --- at byte 0", ErrInvalidSkill)
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
	return nil, nil, fmt.Errorf("%w: frontmatter is not closed by a --- line", ErrInvalidSkill)
}

// ValidSupportPath checks the relative path of a supporting file: "/"
// separated, every segment from [A-Za-z0-9._-], none empty, none starting with
// a dot (so no "." or ".."), none ending in a dot or being a Windows device
// name (CON, NUL, COM1... with any extension), because the value is written into a fence header.
// The message echoes at most 128 runes of rel.
func ValidSupportPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("%w: the file path is empty", ErrInvalidSkill)
	}
	for _, seg := range strings.Split(rel, "/") {
		if !segmentRE.MatchString(seg) {
			return fmt.Errorf("%w: %.128q: a path segment must match %s", ErrInvalidSkill, rel, segmentRE)
		}
		if strings.HasSuffix(seg, ".") || reservedRE.MatchString(seg) {
			return fmt.Errorf("%w: %.128q: a path segment is a Windows reserved name or ends with a dot", ErrInvalidSkill, rel)
		}
	}
	return nil
}
