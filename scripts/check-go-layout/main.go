package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	base := flag.String("base", "", "exact PR base commit SHA")
	flag.Parse()
	if err := checkRevision(*base, os.Stdout); err != nil {
		log.SetFlags(0)
		log.Print(err)
		os.Exit(1)
	}
}

func checkRevision(base string, output io.Writer) error {
	paths, err := changedFiles(base)
	if err != nil {
		return err
	}
	regular, err := regularFiles()
	if err != nil {
		return err
	}
	var findings []string
	count := 0
	for _, path := range paths {
		if path == "" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		if !regular[path] {
			return fmt.Errorf("%q: changed Go source must be a regular committed file", path)
		}
		source, err := committedSource(path)
		if err != nil {
			return fmt.Errorf("read committed source %q: %w", path, err)
		}
		findings = append(findings, inspectSource(path, source)...)
		count++
	}
	if len(findings) != 0 {
		return errors.New(strings.Join(findings, "\n"))
	}
	_, err = fmt.Fprintf(output, "CHECKED: changed-production-go-files=%d; named types separated from implementation\n", count)
	return err
}

func changedFiles(base string) ([]string, error) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(base) {
		return nil, errors.New("an exact 40-character base commit SHA is required")
	}
	baseTree, headTree, err := ancestorTrees(base)
	if err != nil {
		return nil, err
	}
	command := exec.Command("git", "diff-tree", "--stdin", "--no-commit-id", "--no-ext-diff", "--name-only", "-z", "-r", "--find-renames", "--diff-filter=ACMRT", "--", "*.go")
	command.Stdin = strings.NewReader(baseTree + " " + headTree + "\n")
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("resolve changed Go files: %w", err)
	}
	header, paths, ok := bytes.Cut(data, []byte("\n"))
	if !ok || string(header) != baseTree+" "+headTree {
		return nil, errors.New("unexpected Git tree-diff framing")
	}
	return strings.Split(string(paths), "\x00"), nil
}

func ancestorTrees(base string) (string, string, error) {
	data, err := exec.Command("git", "log", "--format=%H:%T", "--no-show-signature", "--no-notes", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("read commit ancestry: %w", err)
	}
	headTree := ""
	for _, row := range strings.Fields(string(data)) {
		commit, tree, ok := strings.Cut(row, ":")
		if !ok {
			return "", "", errors.New("unexpected Git ancestry framing")
		}
		if headTree == "" {
			headTree = tree
		}
		if commit == base {
			return tree, headTree, nil
		}
	}
	return "", "", errors.New("base commit must exist and be an ancestor of HEAD")
}

func regularFiles() (map[string]bool, error) {
	data, err := exec.Command("git", "ls-tree", "-r", "-z", "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("read committed file modes: %w", err)
	}
	result := make(map[string]bool)
	for _, row := range strings.Split(string(data), "\x00") {
		if row == "" {
			continue
		}
		metadata, path, ok := strings.Cut(row, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 {
			return nil, errors.New("unexpected Git tree framing")
		}
		result[path] = (fields[0] == "100644" || fields[0] == "100755") && fields[1] == "blob"
	}
	return result, nil
}

func committedSource(path string) ([]byte, error) {
	command := exec.Command("git", "cat-file", "--batch", "-z")
	command.Stdin = strings.NewReader("HEAD:" + path + "\x00")
	data, err := command.Output()
	if err != nil {
		return nil, err
	}
	header, body, ok := bytes.Cut(data, []byte("\n"))
	fields := strings.Fields(string(header))
	if !ok || len(fields) != 3 || fields[1] != "blob" {
		return nil, errors.New("expected one committed Git blob")
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil || size < 0 || size >= len(body) || size+1 != len(body) || body[size] != '\n' {
		return nil, errors.New("unexpected Git blob size/framing")
	}
	return body[:size], nil
}
