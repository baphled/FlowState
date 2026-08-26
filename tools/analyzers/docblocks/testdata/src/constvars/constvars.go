package constvars

const UndocumentedConst = 1 // want `exported const UndocumentedConst missing doc comment`

// DocumentedConst is a properly documented constant.
const DocumentedConst = 2

var UndocumentedVar string // want `exported var UndocumentedVar missing doc comment`

// DocumentedVar is a properly documented variable.
var DocumentedVar string

// GroupDocCoversAll defines grouped constants.
const (
	GroupedA = "a"
	GroupedB = "b"
)

const (
	// IndividuallyDocumented is a constant with its own doc.
	IndividuallyDocumented = "x"

	UndocumentedInGroup = "y" // want `exported const UndocumentedInGroup missing doc comment`
)

const unexportedConst = 99

var unexportedVar = "hidden"

// This constant is not named correctly.
const MisnamedConst = 3 // want `doc comment for MisnamedConst should start with "MisnamedConst"`

// This variable is not named correctly.
var MisnamedVar string // want `doc comment for MisnamedVar should start with "MisnamedVar"`
