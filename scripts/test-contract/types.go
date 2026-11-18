package main

type sourceFile struct {
	// Name is the repository-relative path used in parser diagnostics.
	Name string
	// Source is the complete candidate Go source, never a path to mutable input.
	Source string
}
