package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/ossyrian/tau/internal/theme"
)

// styleFor maps a token to the terminal's own 16-colour palette, so syntax
// colours follow the user's theme and never paint a background.
func styleFor(t chroma.TokenType) lipgloss.Style {
	switch {
	case t == chroma.GenericHeading, t == chroma.GenericSubheading, t == chroma.GenericStrong:
		return theme.Title
	case t == chroma.GenericEmph:
		return lipgloss.NewStyle().Italic(true)
	case t.InCategory(chroma.Comment):
		return theme.Dim
	case t.InCategory(chroma.Keyword), t == chroma.NameTag, t == chroma.NameAttribute:
		return theme.Info
	case t.InSubCategory(chroma.LiteralString), t == chroma.LiteralStringBacktick:
		return theme.OK
	case t.InSubCategory(chroma.LiteralNumber), t.InCategory(chroma.Name) && t == chroma.NameConstant:
		return theme.Warn
	}
	return lipgloss.NewStyle()
}

// highlight colours text written in the named language ("markdown", "yaml").
// Text it cannot lex comes back unchanged.
func highlight(language, text string) string {
	lexer := lexers.Get(language)
	if lexer == nil {
		return text
	}
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return text
	}
	var b strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		// Style each line apart: a style that spans a newline would be
		// cut off when the viewport slices the text into lines.
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				b.WriteByte('\n')
			}
			if part != "" {
				b.WriteString(styleFor(tok.Type).Render(part))
			}
		}
	}
	return b.String()
}
