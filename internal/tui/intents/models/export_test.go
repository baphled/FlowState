package models

// OnSelectForTest returns the OnSelect callback for testing purposes.
func (i *Intent) OnSelectForTest() func(provider, model string) {
	return i.onSelect
}
