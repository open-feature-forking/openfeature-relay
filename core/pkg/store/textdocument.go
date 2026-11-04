package store

// TextDocument is implemented by the Value of a document whose configuration format lets the flag sync endpoint send
// it to a client: the text is the flag configuration as the source delivered it.
type TextDocument interface {
	DocumentText() string
}

// DocumentText returns the text of the document's value, and whether the value has one.
func DocumentText(d Document) (string, bool) {
	t, ok := d.Value.(TextDocument)
	if !ok {
		return "", false
	}
	return t.DocumentText(), true
}
