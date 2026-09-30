package installerbody

import "strings"

// Fixture is one body and the verdict the rule gives it.
type Fixture struct {
	Name string
	Body string
	Want Kind
}

// Fixtures is the rule's table, one cell per clause or edge. It is not test-only code by
// accident: the jail's launcher checks bodies in shell (the package comment), and its parity
// test in internal/entrypoint runs these same bodies through that check, so the two
// implementations are held to one table rather than to two that could drift.
func Fixtures() []Fixture {
	script := "#!/bin/bash\necho hi\n"
	pad := func(off int) string {
		// A shebang script whose first NUL sits at exactly offset off.
		body := script + "#"
		body += strings.Repeat("x", off-len(body)-1) + "\n"
		return body + "\x00\x00\x01payload"
	}
	return []Fixture{
		{"a shebang script", script, Script},
		{"an empty body", "", Script},
		{"an ELF executable", "\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 56) + "binary", Binary},
		{"a script with a NUL at offset 512", pad(512), Binary},
		{"a script with a NUL at offset 1023", pad(1023), Binary},
		{"a script with a NUL at offset 1024", pad(1024), Script},
		{"a web page", "<!doctype html><html><body>moved</body></html>\n", Markup},
		{"a web page in capitals after blanks", " \t<!DOCTYPE HTML>\n<html>\n", Markup},
		{"an html page with no doctype", "<html lang=\"en\">\n", Markup},
		{"an xml document", "<?xml version=\"1.0\"?>\n<a/>\n", Markup},
		{"a tag that is not a page", "<div>\necho hi\n", Script},
		{"control bytes and no shebang", "\x01\x02\x03\x1f\x7f\necho hi\n", NonText},
		{"a shebang script carrying control bytes", "#!/bin/sh\n# \x01\x02\x7f\n", Script},
		{"UTF-8 and text control bytes, no shebang", "# installer ✓ \t\x1b[32m \x1a end\r\n", Script},
	}
}
