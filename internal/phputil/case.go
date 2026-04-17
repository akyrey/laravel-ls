package phputil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Snake converts a camelCase or StudlyCase identifier to snake_case,
// mirroring Illuminate\Support\Str::snake exactly.
//
// Laravel's algorithm:
//  1. Fast path: if value is all lowercase letters it is already snake_case.
//     NOTE: ctype_lower in PHP returns false for underscores, digits, etc.,
//     so a value like "foo_bar" still enters the main branch — this is
//     intentional, it is idempotent for inputs that contain only lowercase
//     letters and underscores because the regex finds nothing to insert.
//  2. Uppercase the first character of each whitespace-separated word
//     (equivalent to PHP's ucwords with default whitespace delimiters).
//  3. Strip whitespace.
//  4. Insert "_" before every uppercase letter using the lookahead regex
//     /(.)(?=[A-Z])/u — this inserts a separator between EVERY adjacent pair
//     where the right character is uppercase, so "HTML" → "H_T_M_L".
//  5. Lowercase the whole string.
//
// Consequence for consecutive capitals: "HTMLParser" → "h_t_m_l_parser",
// NOT "html_parser". This must be mirrored exactly or attribute name
// lookups will silently miss.
func Snake(value string) string {
	if isAllLower(value) {
		return value
	}
	value = ucwordsWhitespace(value)
	value = stripWhitespace(value)
	value = insertUnderscoreBeforeUpper(value)
	return strings.ToLower(value)
}

// Studly converts snake_case (or any delimiter-separated form) to StudlyCase,
// mirroring Illuminate\Support\Str::studly.
//
// Algorithm: replace '-' and '_' with ' ', split on any whitespace run,
// ucfirst each word, concatenate.
func Studly(value string) string {
	value = strings.NewReplacer("-", " ", "_", " ").Replace(value)
	words := splitWhitespace(value)
	var b strings.Builder
	for _, w := range words {
		b.WriteString(ucfirst(w))
	}
	return b.String()
}

// Camel converts snake_case to camelCase, mirroring Illuminate\Support\Str::camel.
// It is equivalent to lcfirst(Studly(value)).
func Camel(value string) string {
	return lcfirst(Studly(value))
}

// CamelToSnake and StudlyToSnake are both just Snake — Laravel's snake()
// handles camelCase and StudlyCase inputs identically.
func CamelToSnake(value string) string { return Snake(value) }
func StudlyToSnake(value string) string { return Snake(value) }

// isAllLower mirrors PHP's ctype_lower: returns true only if every rune is
// a lowercase Unicode letter. Underscores, digits, hyphens return false.
func isAllLower(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLower(r) {
			return false
		}
	}
	return true
}

// ucwordsWhitespace uppercases the first letter of each whitespace-separated
// word, mirroring PHP ucwords with default delimiters (" \t\r\n\f\v").
// Underscore and hyphen are NOT word separators here (unlike studly).
func ucwordsWhitespace(s string) string {
	inWord := false
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
			b.WriteRune(r)
		} else {
			if !inWord {
				b.WriteRune(unicode.ToUpper(r))
				inWord = true
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// stripWhitespace removes all Unicode whitespace characters.
func stripWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// insertUnderscoreBeforeUpper inserts "_" before every uppercase letter,
// mirroring the PHP regex /(.)(?=[A-Z])/u → "$1_delimiter".
// It is a lookahead: the character before each uppercase gets a "_" appended
// after it, but the uppercase letter itself is NOT consumed/replaced.
//
// This produces one "_" per uppercase letter regardless of runs:
// "HTML" → "H_T_M_L", "emailAddress" → "email_Address".
func insertUnderscoreBeforeUpper(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i, r := range runes {
		b.WriteRune(r)
		if i+1 < len(runes) && unicode.IsUpper(runes[i+1]) {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// splitWhitespace splits on runs of whitespace, mirroring PHP mb_split('\s+', ...).
// Empty strings from leading/trailing whitespace are filtered out.
func splitWhitespace(s string) []string {
	return strings.FieldsFunc(s, unicode.IsSpace)
}

// ucfirst uppercases the first UTF-8 rune of s, leaving the rest unchanged.
func ucfirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// lcfirst lowercases the first UTF-8 rune of s.
func lcfirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}
