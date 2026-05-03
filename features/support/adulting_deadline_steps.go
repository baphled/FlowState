//go:build e2e

package support

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cucumber/godog"
)

type adultingDataset struct {
	Meta struct {
		Today       string `json:"today"`
		Description string `json:"description"`
	} `json:"meta"`
	Tasks []adultingTask `json:"tasks"`
	ExpectedDeadlineClasses map[string]string   `json:"expected_deadline_classes"`
	ExpectedBillItems       []string            `json:"expected_bill_items"`
	ExpectedBillStatuses    map[string]string   `json:"expected_bill_statuses"`
	ExpectedCriticalPath    []string            `json:"expected_critical_path"`
}

type adultingTask struct {
	Title     string `json:"title"`
	Deadline  string `json:"deadline"`
	Priority  int    `json:"priority"`
	Urgency   string `json:"urgency"`
	Impact    string `json:"impact"`
	Rationale string `json:"rationale"`
}

type deadlineState struct {
	dataset *adultingDataset
	today   time.Time
}

var dlState *deadlineState

func init() {
	dlState = &deadlineState{}
}

func RegisterAdultingDeadlineSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the adulting test dataset is loaded with anchor date (.+)$`, dlState.loadDataset)
	ctx.Step(`^the task "([^"]*)" should have deadline class "([^"]*)"$`, dlState.taskShouldHaveDeadlineClass)
	ctx.Step(`^the tasks should include the following bill items:$`, dlState.tasksShouldIncludeBillItems)
	ctx.Step(`^the bill "([^"]*)" should have status "([^"]*)"$`, dlState.billShouldHaveStatus)
	ctx.Step(`^the following tasks should be on the critical path:$`, dlState.tasksShouldBeOnCriticalPath)
}

func (s *deadlineState) loadDataset(anchorDate string) error {
	paths := []string{
		filepath.Join("examples", "swarms", "adulting", "testdata", "tasks_fixture.json"),
		filepath.Join("..", "..", "examples", "swarms", "adulting", "testdata", "tasks_fixture.json"),
	}

	var data []byte
	var err error
	for _, p := range paths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if data == nil {
		return fmt.Errorf("cannot find tasks_fixture.json: last error: %w", err)
	}

	var dataset adultingDataset
	if err := json.Unmarshal(data, &dataset); err != nil {
		return fmt.Errorf("cannot parse tasks_fixture.json: %w", err)
	}

	today, err := time.Parse("2006-01-02T15:04:05Z", dataset.Meta.Today)
	if err != nil {
		return fmt.Errorf("cannot parse anchor date: %w", err)
	}

	s.dataset = &dataset
	s.today = today

	if dataset.Meta.Today != anchorDate+"T00:00:00Z" {
		return fmt.Errorf("anchor date mismatch: fixture has %s, step expects %s", dataset.Meta.Today, anchorDate)
	}

	return nil
}

func (s *deadlineState) taskShouldHaveDeadlineClass(taskTitle, expectedClass string) error {
	if s.dataset == nil {
		return fmt.Errorf("dataset not loaded")
	}

	actual, ok := s.dataset.ExpectedDeadlineClasses[taskTitle]
	if !ok {
		return fmt.Errorf("task %q not found in expected_deadline_classes", taskTitle)
	}

	if actual != expectedClass {
		return fmt.Errorf("task %q: expected class %q, got %q", taskTitle, expectedClass, actual)
	}

	var task *adultingTask
	for i := range s.dataset.Tasks {
		if s.dataset.Tasks[i].Title == taskTitle {
			task = &s.dataset.Tasks[i]
			break
		}
	}
	if task == nil {
		return fmt.Errorf("task %q not found in tasks array", taskTitle)
	}

	verified, err := s.verifyDeadlineClass(task)
	if err != nil {
		return err
	}
	if verified != expectedClass {
		return fmt.Errorf("task %q: fixture says %q but classification logic says %q", taskTitle, expectedClass, verified)
	}

	return nil
}

func (s *deadlineState) verifyDeadlineClass(task *adultingTask) (string, error) {
	if task.Deadline == "" || task.Deadline == "null" {
		return "unspecified", nil
	}

	dl, err := time.Parse("2006-01-02", task.Deadline)
	if err != nil {
		return "", fmt.Errorf("cannot parse deadline %q: %w", task.Deadline, err)
	}

	days := dl.Sub(s.today).Hours() / 24

	switch {
	case days < 0:
		return "overdue", nil
	case days <= 7:
		return "critical", nil
	case days <= 14:
		return "imminent", nil
	case days <= 28:
		return "approaching", nil
	default:
		return "scheduled", nil
	}
}

func (s *deadlineState) tasksShouldIncludeBillItems(table *godog.Table) error {
	if s.dataset == nil {
		return fmt.Errorf("dataset not loaded")
	}

	expected := make(map[string]bool)
	for _, row := range table.Rows[1:] {
		expected[row.Cells[0].Value] = false
	}

	for _, item := range s.dataset.ExpectedBillItems {
		if _, ok := expected[item]; ok {
			expected[item] = true
		}
	}

	for item, found := range expected {
		if !found {
			return fmt.Errorf("expected bill item %q not found in dataset", item)
		}
	}

	return nil
}

func (s *deadlineState) billShouldHaveStatus(billTitle, expectedStatus string) error {
	if s.dataset == nil {
		return fmt.Errorf("dataset not loaded")
	}

	actual, ok := s.dataset.ExpectedBillStatuses[billTitle]
	if !ok {
		return fmt.Errorf("bill %q not found in expected_bill_statuses", billTitle)
	}

	if actual != expectedStatus {
		return fmt.Errorf("bill %q: expected status %q, got %q", billTitle, expectedStatus, actual)
	}

	return nil
}

func (s *deadlineState) tasksShouldBeOnCriticalPath(table *godog.Table) error {
	if s.dataset == nil {
		return fmt.Errorf("dataset not loaded")
	}

	expected := make(map[string]bool)
	for _, row := range table.Rows[1:] {
		expected[row.Cells[0].Value] = false
	}

	for _, item := range s.dataset.ExpectedCriticalPath {
		if _, ok := expected[item]; ok {
			expected[item] = true
		}
	}

	for item, found := range expected {
		if !found {
			return fmt.Errorf("expected critical path item %q not found in dataset", item)
		}
	}

	return nil
}
