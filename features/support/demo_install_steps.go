//go:build e2e

package support

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

type demoInstallSteps struct {
	repoRoot string
}

func registerDemoInstallSteps(ctx *godog.ScenarioContext) {
	steps := &demoInstallSteps{}
	ctx.Step(`^the FlowState repository root is available$`, steps.theFlowStateRepositoryRootIsAvailable)
	ctx.Step(`^the demo quickstart document should mention:$`, steps.theDemoQuickstartDocumentShouldMention)
	ctx.Step(`^the demo environment example should mention:$`, steps.theDemoEnvironmentExampleShouldMention)
	ctx.Step(`^the demo bootstrap script should be executable$`, steps.theDemoBootstrapScriptShouldBeExecutable)
	ctx.Step(`^the demo bootstrap script should mention:$`, steps.theDemoBootstrapScriptShouldMention)
	ctx.Step(`^the demo Makefile shortcuts should be available:$`, steps.theDemoMakefileShortcutsShouldBeAvailable)
}

func (s *demoInstallSteps) theFlowStateRepositoryRootIsAvailable() error {
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	s.repoRoot = root
	return nil
}

func (s *demoInstallSteps) theDemoQuickstartDocumentShouldMention(table *godog.Table) error {
	return s.fileShouldMention("docs/install/demo-quickstart.md", table)
}

func (s *demoInstallSteps) theDemoEnvironmentExampleShouldMention(table *godog.Table) error {
	return s.fileShouldMention(".env.demo.example", table)
}

func (s *demoInstallSteps) theDemoBootstrapScriptShouldBeExecutable() error {
	path := filepath.Join(s.mustRepoRoot(), "scripts", "bootstrap-demo.sh")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat demo bootstrap script: %w", err)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("expected %s to be executable", path)
	}
	return nil
}

func (s *demoInstallSteps) theDemoBootstrapScriptShouldMention(table *godog.Table) error {
	return s.fileShouldMention("scripts/bootstrap-demo.sh", table)
}

func (s *demoInstallSteps) theDemoMakefileShortcutsShouldBeAvailable(table *godog.Table) error {
	content, err := s.readRepoFile("Makefile")
	if err != nil {
		return err
	}
	for _, target := range tableValues(table) {
		needle := "\n" + target + ":"
		if !strings.Contains("\n"+content, needle) {
			return fmt.Errorf("expected Makefile target %q", target)
		}
	}
	return nil
}

func (s *demoInstallSteps) fileShouldMention(path string, table *godog.Table) error {
	content, err := s.readRepoFile(path)
	if err != nil {
		return err
	}
	for _, text := range tableValues(table) {
		if !strings.Contains(content, text) {
			return fmt.Errorf("expected %s to contain %q", path, text)
		}
	}
	return nil
}

func (s *demoInstallSteps) readRepoFile(path string) (string, error) {
	content, err := os.ReadFile(filepath.Join(s.mustRepoRoot(), filepath.FromSlash(path)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(content), nil
}

func (s *demoInstallSteps) mustRepoRoot() string {
	if s.repoRoot != "" {
		return s.repoRoot
	}
	root, err := findRepositoryRoot()
	if err != nil {
		panic(err)
	}
	s.repoRoot = root
	return root
}

func tableValues(table *godog.Table) []string {
	values := make([]string, 0, len(table.Rows))
	for i, row := range table.Rows {
		if i == 0 || len(row.Cells) == 0 {
			continue
		}
		values = append(values, row.Cells[0].Value)
	}
	return values
}

func findRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}
