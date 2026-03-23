package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ReferenceValidator checks that all Go file references in plan text exist under the project root.
type ReferenceValidator struct{}

// Validate scans planText for backtick-wrapped .go file paths and checks their existence under projectRoot.
// Returns a ValidationResult and error if any reference is invalid or outside project root.
func (v *ReferenceValidator) Validate(planText string, projectRoot string) (*ValidationResult, error) {
	result := &ValidationResult{Valid: true, Score: 1.0}
	refRegex := regexp.MustCompile("`([^`]+\\.go[^`]*)`")
	matches := refRegex.FindAllStringSubmatch(planText, -1)
	if len(matches) == 0 {
		return result, nil // No refs: valid, score 1.0
	}

	totalRefs := len(matches)
	validRefs := v.validateReferences(matches, projectRoot, result)

	v.calculateScore(result, validRefs, totalRefs)

	if !result.Valid && len(result.Errors) > 0 {
		return result, fmt.Errorf("%s", strings.Join(result.Errors, "; "))
	}
	return result, nil
}

func (v *ReferenceValidator) validateReferences(matches [][]string, projectRoot string, result *ValidationResult) int {
	validRefs := 0
	projRootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "invalid project root: "+err.Error())
		return validRefs
	}

	for _, m := range matches {
		ref := extractRef(m[1])
		if v.validateReference(ref, projectRoot, projRootAbs, result) {
			validRefs++
		}
	}
	return validRefs
}

func extractRef(refWithLine string) string {
	colonIdx := strings.Index(refWithLine, ":")
	if colonIdx != -1 {
		return refWithLine[:colonIdx]
	}
	return refWithLine
}

func (v *ReferenceValidator) validateReference(ref, projectRoot, projRootAbs string, result *ValidationResult) bool {
	absPath := filepath.Clean(filepath.Join(projectRoot, ref))
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "invalid path: "+ref)
		return false
	}

	if !isUnderProjectRoot(absPath, projRootAbs) {
		result.Valid = false
		result.Errors = append(result.Errors, "reference outside project root: "+ref)
		return false
	}

	if _, err := os.Stat(absPath); err != nil {
		result.Valid = false
		if os.IsNotExist(err) {
			result.Errors = append(result.Errors, "file not found: "+ref)
		} else {
			result.Errors = append(result.Errors, "stat error: "+ref+": "+err.Error())
		}
		return false
	}

	return true
}

func isUnderProjectRoot(absPath, projRootAbs string) bool {
	return absPath == projRootAbs || strings.HasPrefix(absPath, projRootAbs+string(os.PathSeparator))
}

func (v *ReferenceValidator) calculateScore(result *ValidationResult, validRefs, totalRefs int) {
	if validRefs < totalRefs {
		result.Valid = false
		result.Score = float64(validRefs) / float64(totalRefs)
		if result.Score < 0.0 {
			result.Score = 0.0
		}
	} else {
		result.Score = 1.0
	}
}
