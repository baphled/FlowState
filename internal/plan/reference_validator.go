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
//
// Expected:
//   - planText contains backtick-wrapped file references.
//   - projectRoot is a valid directory path.
//
// Returns:
//   - A ValidationResult with score and errors.
//   - An error if any reference is invalid or outside project root.
//
// Side effects:
//   - Accesses the filesystem to check file existence.
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

// validateReferences processes all matched file references and returns the count of valid references.
//
// Expected:
//   - matches contains regex match groups with file references.
//   - projectRoot is a valid directory path.
//
// Returns:
//   - The count of valid file references.
//
// Side effects:
//   - Modifies result by appending errors for invalid references.
//   - Accesses the filesystem to check file existence.
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

// extractRef removes line number suffix (e.g., ":42") from a file reference.
//
// Expected:
//   - refWithLine is a file reference string, optionally with ":linenum" suffix.
//
// Returns:
//   - The file reference without line number suffix.
//
// Side effects:
//   - None.
func extractRef(refWithLine string) string {
	colonIdx := strings.Index(refWithLine, ":")
	if colonIdx != -1 {
		return refWithLine[:colonIdx]
	}
	return refWithLine
}

// validateReference checks a single file reference for validity, security, and existence.
//
// Expected:
//   - ref is a relative file path.
//   - projectRoot and projRootAbs are valid directory paths.
//
// Returns:
//   - true if the reference is valid and the file exists, false otherwise.
//
// Side effects:
//   - Modifies result by appending errors for invalid references.
//   - Accesses the filesystem to check file existence.
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

// isUnderProjectRoot checks whether an absolute path is within the project root directory.
//
// Expected:
//   - absPath and projRootAbs are absolute file paths.
//
// Returns:
//   - true if absPath is within projRootAbs, false otherwise.
//
// Side effects:
//   - None.
func isUnderProjectRoot(absPath, projRootAbs string) bool {
	return absPath == projRootAbs || strings.HasPrefix(absPath, projRootAbs+string(os.PathSeparator))
}

// calculateScore computes the validation score based on the ratio of valid to total references.
//
// Expected:
//   - validRefs is between 0 and totalRefs.
//
// Side effects:
//   - Modifies result by setting the Valid field and Score.
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
