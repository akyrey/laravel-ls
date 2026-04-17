package eloquent

import "github.com/akyrey/laravel-ls/internal/phputil"

// AttributeKind classifies the source of a model attribute entry.
type AttributeKind int

const (
	// ModernAccessor: a method returning Illuminate\Database\Eloquent\Casts\Attribute.
	// Method name is camelCase; ExposedName is its snake_case equivalent.
	ModernAccessor AttributeKind = iota
	// LegacyAccessor: a method named getXxxAttribute().
	LegacyAccessor
	// LegacyMutator: a method named setXxxAttribute().
	LegacyMutator
	// Relationship: method whose return type is a subclass of Relation.
	// ExposedName equals MethodName (no case translation).
	Relationship
	// FillableArray: a key in the $fillable array declaration.
	FillableArray
	// CastArray: a key in the $casts array declaration.
	CastArray
	// AppendsArray: a key in the $appends array declaration.
	AppendsArray
	// HiddenArray: a key in the $hidden array declaration.
	HiddenArray
	// IdeHelperProperty: a @property / @property-read / @property-write
	// entry parsed from _ide_helper_models.php.
	IdeHelperProperty
	// IdeHelperMethod: a @method entry from _ide_helper_models.php.
	IdeHelperMethod
)

// AttributeSource indicates whether an entry came from real source code or
// a generated stub, enabling the conflict-resolution rule at query time:
// SourceAST wins over SourceIdeHelper.
type AttributeSource int

const (
	SourceAST       AttributeSource = iota // parsed from app source code
	SourceIdeHelper                        // parsed from _ide_helper_models.php stub
)

// ModelAttribute is a single entry in a model's attribute catalog.
type ModelAttribute struct {
	// ExposedName is the snake_case name as used at the call site, e.g. "email_address".
	ExposedName string
	// MethodName is the camelCase PHP method name, e.g. "emailAddress".
	// Empty for array entries (FillableArray, CastArray, etc.) and ide-helper entries.
	MethodName string
	Kind       AttributeKind
	Source     AttributeSource
	// Location is the jump target. May be zero for SourceIdeHelper entries
	// when policy is to return nothing (see plan §7).
	Location phputil.Location
	// RelatedFQN is the fully-qualified class name of the related model for
	// Relationship attributes (e.g. "App\Models\Post" for a hasMany(Post::class)
	// relation). Empty for non-relationship attributes.
	RelatedFQN phputil.FQN
}

// ModelCatalog is the per-model symbol table produced by the Eloquent indexer.
type ModelCatalog struct {
	Class   phputil.FQN
	Extends phputil.FQN // direct parent FQN (for inheritance-chain walking)

	// ByExposed maps the snake_case attribute name to all known entries.
	// Multiple entries exist when the same name appears in both an accessor
	// method AND a $fillable or $casts declaration, or in the ide-helper stub.
	// Query-time ranking: ModernAccessor > LegacyAccessor/Mutator > array entries > ide-helper.
	ByExposed map[string][]ModelAttribute
}

// ModelIndex is the full Eloquent symbol table for a project.
type ModelIndex struct {
	byFQN map[phputil.FQN]*ModelCatalog
}

// NewModelIndex returns an empty, ready-to-use index.
func NewModelIndex() *ModelIndex {
	return &ModelIndex{byFQN: make(map[phputil.FQN]*ModelCatalog)}
}

// Add stores or replaces a catalog entry. Typically called once per class.
func (idx *ModelIndex) Add(c *ModelCatalog) {
	idx.byFQN[c.Class] = c
}

// Lookup returns the catalog for the given model FQN, or nil if not indexed.
func (idx *ModelIndex) Lookup(fqn phputil.FQN) *ModelCatalog {
	return idx.byFQN[fqn]
}

// All returns every catalog in the index.
func (idx *ModelIndex) All() []*ModelCatalog {
	out := make([]*ModelCatalog, 0, len(idx.byFQN))
	for _, c := range idx.byFQN {
		out = append(out, c)
	}
	return out
}
