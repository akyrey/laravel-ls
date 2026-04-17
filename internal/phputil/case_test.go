package phputil_test

import (
	"testing"

	"github.com/akyrey/laravel-ls/internal/phputil"
)

// caseRow mirrors the test table from the plan. Each row is a triple of
// (input, expectedSnake, expectedStudly, expectedCamel).
// Starred rows call out counterintuitive Laravel behavior; see plan §4.
var caseTable = []struct {
	input   string
	snake   string
	studly  string
	camel   string
	comment string
}{
	{"emailAddress", "email_address", "EmailAddress", "emailAddress", ""},
	{"EmailAddress", "email_address", "EmailAddress", "emailAddress", ""},
	{"email_address", "email_address", "EmailAddress", "emailAddress", ""},

	// Double underscore: snake is idempotent (no uppercase → no insertion),
	// studly/camel collapse consecutive underscores as word separators.
	{"email__address", "email__address", "EmailAddress", "emailAddress", "double underscore"},

	// Consecutive capitals: each uppercase gets its own underscore.
	// "HTMLParser" → "H_T_M_L_Parser" → "h_t_m_l_parser", NOT "html_parser".
	{"HTMLParser", "h_t_m_l_parser", "HTMLParser", "hTMLParser", "consecutive caps"},

	// userID: r→I is one insertion, I→D is another → "user_i_d".
	{"userID", "user_i_d", "UserID", "userID", "trailing ID"},

	// iOS: ucwords('iOS') = 'IOS' (only first char uppercased, 'i'→'I').
	// Then regex on 'IOS': I→O and O→S each get an underscore → 'I_O_S' → 'i_o_s'.
	// studly('iOS'): replace no delimiters → single word ["iOS"] → ucfirst → "IOS".
	{"iOS", "i_o_s", "IOS", "iOS", "iOS starred"},

	{"first_name_2", "first_name_2", "FirstName2", "firstName2", "trailing digit"},

	// user2FAToken: snake inserts _ after '2' before 'F', after 'F' before 'A',
	// after 'A' before 'T' → "user2_f_a_token".
	{"user2FAToken", "user2_f_a_token", "User2FAToken", "user2FAToken", "digit+consecutive caps"},

	// ID: two-letter all-caps → "i_d".
	{"ID", "i_d", "ID", "iD", "two-letter acronym"},

	{"a", "a", "A", "a", "single letter"},

	// foo-bar: snake doesn't treat '-' as a separator, passes through unchanged.
	// studly replaces '-' with space so it splits correctly.
	{"foo-bar", "foo-bar", "FooBar", "fooBar", "hyphen separator"},

	{"foo_bar_baz", "foo_bar_baz", "FooBarBaz", "fooBarBaz", ""},
	{"FooBarBaz", "foo_bar_baz", "FooBarBaz", "fooBarBaz", ""},

	// getHTMLElement: consecutive caps in the middle → get_h_t_m_l_element.
	{"getHTMLElement", "get_h_t_m_l_element", "GetHTMLElement", "getHTMLElement", "method name with acronym"},
}

func TestSnake(t *testing.T) {
	t.Parallel()
	for _, tt := range caseTable {
		t.Run(tt.input, func(t *testing.T) {
			got := phputil.Snake(tt.input)
			if got != tt.snake {
				t.Errorf("Snake(%q) = %q, want %q (note: %s)", tt.input, got, tt.snake, tt.comment)
			}
		})
	}
}

func TestStudly(t *testing.T) {
	t.Parallel()
	for _, tt := range caseTable {
		t.Run(tt.input, func(t *testing.T) {
			got := phputil.Studly(tt.input)
			if got != tt.studly {
				t.Errorf("Studly(%q) = %q, want %q (note: %s)", tt.input, got, tt.studly, tt.comment)
			}
		})
	}
}

func TestCamel(t *testing.T) {
	t.Parallel()
	for _, tt := range caseTable {
		t.Run(tt.input, func(t *testing.T) {
			got := phputil.Camel(tt.input)
			if got != tt.camel {
				t.Errorf("Camel(%q) = %q, want %q (note: %s)", tt.input, got, tt.camel, tt.comment)
			}
		})
	}
}

// TestSnakeIdempotent verifies that Snake(Snake(x)) == Snake(x) for all inputs.
func TestSnakeIdempotent(t *testing.T) {
	t.Parallel()
	for _, tt := range caseTable {
		t.Run(tt.input, func(t *testing.T) {
			once := phputil.Snake(tt.input)
			twice := phputil.Snake(once)
			if once != twice {
				t.Errorf("Snake not idempotent on %q: first=%q second=%q", tt.input, once, twice)
			}
		})
	}
}

// TestCamelToSnakeRoundtrip verifies Snake(Camel(snake)) == snake for pure snake inputs
// that contain no trailing digits at word boundaries. Names like "first_name_2" intentionally
// do NOT round-trip: Camel("first_name_2") → "firstName2" → Snake → "first_name2" (digit
// absorbs into the final word). This matches Laravel's Str::camel / Str::snake behavior.
func TestCamelToSnakeRoundtrip(t *testing.T) {
	t.Parallel()
	pureSnake := []string{
		"email_address",
		"first_name",
		"foo_bar_baz",
		"foo_bar",
	}
	for _, s := range pureSnake {
		t.Run(s, func(t *testing.T) {
			if got := phputil.CamelToSnake(phputil.Camel(s)); got != s {
				t.Errorf("CamelToSnake(Camel(%q)) = %q, want %q", s, got, s)
			}
		})
	}
}
