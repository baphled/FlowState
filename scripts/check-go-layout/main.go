package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
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
	var findings []string
	count := 0
	for _, path := range paths {
		if path == "" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := exec.Command("git", "show", "HEAD:"+path).Output()
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
	if err := exec.Command("git", "merge-base", "--is-ancestor", base, "HEAD").Run(); err != nil {
		return nil, fmt.Errorf("base commit must exist and be an ancestor of HEAD: %w", err)
	}
	data, err := exec.Command("git", "diff", "--no-ext-diff", "--name-only", "-z", "--diff-filter=ACMRT", base, "HEAD", "--", "*.go").Output()
	if err != nil {
		return nil, fmt.Errorf("resolve changed Go files: %w", err)
	}
	return strings.Split(string(data), "\x00"), nil
}
