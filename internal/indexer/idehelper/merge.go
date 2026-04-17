package idehelper

import (
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

// Merge parses _ide_helper_models.php (if present at path) and adds
// SourceIdeHelper entries into catalog for any attribute name not already
// present from SourceAST.
//
// Conflict-resolution rule: SourceAST wins. IdeHelper entries for a name
// that already has at least one SourceAST entry are silently dropped.
// This means a pure DB column (e.g. $id) gets an IdeHelperProperty entry,
// while an accessor method that is also listed in the stub uses the AST entry.
//
// Jump-target policy: IdeHelper Location fields are set to the stub line,
// but the query layer filters them out when policy is "return nothing for
// ide-helper-only names" (plan §7, option b — the approved choice).
//
// Not yet implemented — stub for the next iteration.
//
// Implementation plan:
//  1. Check whether path exists; return nil if absent (not an error).
//  2. Parse with VKCOM php-parser (it is valid PHP).
//  3. For each namespaced class in the stub, compute FQN and look up or
//     create a ModelCatalog.
//  4. Extract the leading doc-comment string from the class node.
//  5. Parse @property, @property-read, @property-write, @method lines
//     with a small regex (PHPDoc grammar is simple for these tags).
//  6. For each @property $name: if catalog.ByExposed[name] has no SourceAST
//     entry, append an IdeHelperProperty ModelAttribute.
func Merge(path string, idx *eloquent.ModelIndex) error {
	// TODO: implement
	_ = path
	_ = idx
	return nil
}
