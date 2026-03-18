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

type unexportedType struct{}
