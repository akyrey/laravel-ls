package eloquent

import (
	"sync"

	"github.com/akyrey/laravel-lsp/internal/phputil"
)

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
// The same type also holds a trait's attributes (stored in ModelIndex.traits
// rather than byFQN); for traits, Extends is always empty.
type ModelCatalog struct {
	Class      phputil.FQN
	Path       string        // absolute path of the file that defines this class
	Extends    phputil.FQN   // direct parent FQN (for inheritance-chain walking)
	UsesTraits []phputil.FQN // resolved FQNs of traits used in the class/trait body

	// ByExposed maps the snake_case attribute name to all known entries.
	// Multiple entries exist when the same name appears in both an accessor
	// method AND a $fillable or $casts declaration, or in the ide-helper stub.
	// Query-time ranking: ModernAccessor > LegacyAccessor/Mutator > array entries > ide-helper.
	ByExposed map[string][]ModelAttribute
}

// ModelIndex is the full Eloquent symbol table for a project.
type ModelIndex struct {
	byFQN  map[phputil.FQN]*ModelCatalog
	traits map[phputil.FQN]*ModelCatalog // trait FQN → its attribute catalog
	syms   *symbolTable                  // retained for incremental per-file reindex

	// merged memoizes inheritance/trait-merged catalog views per index
	// generation (indexes are immutable once published, so views never go
	// stale within one generation). Guarded by mu because LSP handlers
	// call Lookup concurrently.
	mu     sync.Mutex
	merged map[phputil.FQN]*ModelCatalog
}

// NewModelIndex returns an empty, ready-to-use index.
func NewModelIndex() *ModelIndex {
	return &ModelIndex{
		byFQN:  make(map[phputil.FQN]*ModelCatalog),
		traits: make(map[phputil.FQN]*ModelCatalog),
	}
}

// Add stores or replaces a catalog entry. Typically called once per class.
func (idx *ModelIndex) Add(c *ModelCatalog) {
	idx.byFQN[c.Class] = c
	idx.invalidateMerged()
}

// AddTrait stores or replaces a trait's attribute catalog.
func (idx *ModelIndex) AddTrait(c *ModelCatalog) {
	idx.traits[c.Class] = c
	idx.invalidateMerged()
}

func (idx *ModelIndex) invalidateMerged() {
	idx.mu.Lock()
	idx.merged = nil // catalogs changed; drop memoized merged views
	idx.mu.Unlock()
}

// Lookup returns the catalog for the given model FQN, or nil if not indexed.
// When the model extends another indexed class or uses indexed traits, the
// returned catalog is a merged view that also exposes every inherited and
// trait-provided attribute; otherwise the declared catalog is returned as-is.
func (idx *ModelIndex) Lookup(fqn phputil.FQN) *ModelCatalog {
	cat := idx.byFQN[fqn]
	if cat == nil {
		return nil
	}
	if idx.byFQN[cat.Extends] == nil && !idx.anyIndexedTrait(cat.UsesTraits) {
		return cat // no indexed ancestor or trait — nothing to merge
	}
	return idx.mergedView(fqn, cat)
}

// anyIndexedTrait reports whether at least one of the given trait FQNs has an
// attribute catalog in the index.
func (idx *ModelIndex) anyIndexedTrait(uses []phputil.FQN) bool {
	for _, t := range uses {
		if idx.traits[t] != nil {
			return true
		}
	}
	return false
}

// LookupDeclared returns the catalog holding only the attributes declared in
// the class body itself, with no inherited entries. Callers that mutate the
// catalog (the ide-helper merge) must use this so writes land on the real
// catalog rather than on a merged copy.
func (idx *ModelIndex) LookupDeclared(fqn phputil.FQN) *ModelCatalog {
	return idx.byFQN[fqn]
}

// mergedView builds (and memoizes) a catalog view whose ByExposed also
// contains the attributes of every indexed ancestor and used trait. Entries
// follow PHP's precedence order — the class itself, its traits (including
// traits-of-traits, depth-first), then each ancestor with its traits — so
// child declarations shadow parents in ranked results. Source catalogs are
// never mutated — slices are copied before combining — so catalogs shared
// with previous index generations stay untouched.
func (idx *ModelIndex) mergedView(fqn phputil.FQN, cat *ModelCatalog) *ModelCatalog {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if v, ok := idx.merged[fqn]; ok {
		return v
	}

	view := &ModelCatalog{
		Class:      cat.Class,
		Path:       cat.Path,
		Extends:    cat.Extends,
		UsesTraits: cat.UsesTraits,
		ByExposed:  make(map[string][]ModelAttribute, len(cat.ByExposed)),
	}
	appendAttrs := func(c *ModelCatalog) {
		for name, attrs := range c.ByExposed {
			existing := view.ByExposed[name]
			combined := make([]ModelAttribute, 0, len(existing)+len(attrs))
			combined = append(combined, existing...)
			combined = append(combined, attrs...)
			view.ByExposed[name] = combined
		}
	}

	visitedClasses := make(map[phputil.FQN]bool)
	visitedTraits := make(map[phputil.FQN]bool)
	var addTraits func(uses []phputil.FQN)
	addTraits = func(uses []phputil.FQN) {
		for _, tf := range uses {
			if visitedTraits[tf] {
				continue
			}
			visitedTraits[tf] = true
			tcat := idx.traits[tf]
			if tcat == nil {
				continue
			}
			appendAttrs(tcat)
			addTraits(tcat.UsesTraits)
		}
	}
	for c := cat; c != nil && !visitedClasses[c.Class]; c = idx.byFQN[c.Extends] {
		visitedClasses[c.Class] = true
		appendAttrs(c)
		addTraits(c.UsesTraits)
	}

	if idx.merged == nil {
		idx.merged = make(map[phputil.FQN]*ModelCatalog)
	}
	idx.merged[fqn] = view
	return view
}

// Syms returns the retained symbol table, or nil if this index was not built
// via Walk (e.g. constructed manually in tests).
func (idx *ModelIndex) Syms() *symbolTable { return idx.syms }

// RemoveByFile removes all catalogs whose source file is path.
func (idx *ModelIndex) RemoveByFile(path string) {
	for fqn, cat := range idx.byFQN {
		if cat.Path == path {
			delete(idx.byFQN, fqn)
		}
	}
}

// All returns every catalog in the index.
func (idx *ModelIndex) All() []*ModelCatalog {
	out := make([]*ModelCatalog, 0, len(idx.byFQN))
	for _, c := range idx.byFQN {
		out = append(out, c)
	}
	return out
}
