package sheet

// NextRowCursorForTest exposes the production cursor derivation to the external driver walk tests, so no test hand-rolls it.
func NextRowCursorForTest(digest string, sort *RowSort, last ProjectedRow, pagesServed int) RowCursor {
	return nextRowCursor(digest, sort, last, pagesServed)
}
