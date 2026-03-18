package components

// Expose tokenColor for testing.
var TokenColor = tokenColor

// We need to implement tokenColor here or in status.go, but for export it must exist.
// Since status.go has the function tokenColor (implied), I'll add the export here.
// But wait, I haven't implemented tokenColor in status.go yet (I added a placeholder `TokenColor` helper there, but I should remove that public one and use the private one + export_test.go pattern).

// Actually, in status.go I wrote:
// func TokenColor(used, budget int) lipgloss.Color { return lipgloss.Color("") }
// which is Public.
// The task asked for `func tokenColor` (private).
// I will change status.go to use private `tokenColor` and use `export_test.go` to expose it.

// But wait, I already wrote status.go with Public `TokenColor`.
// I will Edit status.go to make it private `tokenColor`.
