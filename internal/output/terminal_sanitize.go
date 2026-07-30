package output

import (
	"strings"
	"unicode/utf8"
)

// sanitizeHumanCell neutralizes characters that terminals, pagers, CI logs,
// and browser-backed Markdown viewers interpret instead of displaying. JSON
// rendering deliberately does not call this function.
//
// Source line breaks use lineBreak so each renderer can choose an inert
// representation. Other spacing controls become ordinary spaces. Remaining
// C0/C1 controls, DEL, and bidirectional formatting runes are removed.
// Invalid UTF-8 bytes become U+FFFD so raw C1 bytes cannot reach a terminal.
func sanitizeHumanCell(input string, lineBreak string) string {
	if input == "" {
		return input
	}

	var output strings.Builder
	output.Grow(len(input))
	for index := 0; index < len(input); {
		r, size := utf8.DecodeRuneInString(input[index:])
		if r == utf8.RuneError && size == 1 {
			output.WriteRune(utf8.RuneError)
			index++
			continue
		}

		switch {
		case r == '\r':
			output.WriteString(lineBreak)
			index += size
			if index < len(input) && input[index] == '\n' {
				index++
			}
			continue
		case r == '\n':
			output.WriteString(lineBreak)
			index += size
			continue
		case isHumanCellSeparator(r):
			output.WriteByte(' ')
			index += size
			continue
		case isInterpretedHumanRune(r):
			index += size
			continue
		default:
			output.WriteString(input[index : index+size])
			index += size
		}
	}
	return output.String()
}

func isHumanCellSeparator(r rune) bool {
	switch r {
	case '\t', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return true
	default:
		return false
	}
}

func isInterpretedHumanRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c:
		return true
	case r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}
