package tool

// fsOptions carries the overridable size caps.
type fsOptions struct {
	maxRead, maxWrite int64
}

// FSOption customises an fs tool constructor.
type FSOption func(*fsOptions)

// fsReadTool reads bounded file contents under the workdir.
type fsReadTool struct {
	workdir string
	max     int64
}

// fsWriteTool writes bounded content under the workdir.
type fsWriteTool struct {
	workdir string
	max     int64
}

// fsEditTool replaces a unique plain-string match under the workdir.
type fsEditTool struct {
	workdir           string
	maxRead, maxWrite int64
}
