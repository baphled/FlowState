package types

type UndocumentedType struct{} // want `exported type UndocumentedType missing doc comment`

// This describes something else.
type BadNameType struct{} // want `doc comment for BadNameType should start with "BadNameType"`

// DocumentedType is a properly documented type.
type DocumentedType struct{}

type UndocumentedInterface interface{} // want `exported type UndocumentedInterface missing doc comment`

// DocumentedInterface defines a contract.
type DocumentedInterface interface {
	Method()
}

type unexportedType struct{} // want `unexported type unexportedType missing doc comment`

// This describes something else.
type unexportedBadNameType struct{} // want `doc comment for unexportedBadNameType should start with "unexportedBadNameType"`

// unexportedDocumentedType is a properly documented unexported type.
type unexportedDocumentedType struct{}

type unexportedInterface interface{} // want `unexported type unexportedInterface missing doc comment`

// unexportedDocumentedInterface defines an unexported contract.
type unexportedDocumentedInterface interface {
	Method()
}
