package container

import "github.com/akyrey/laravel-ls/internal/phputil"

// BindingKind classifies how a binding was registered.
type BindingKind int

const (
	// BindCall is a direct method call: ->bind(), ->singleton(), ->scoped(), ->instance().
	BindCall BindingKind = iota
	// BindClosure is a closure binding where the body contains a single `new X(...)`.
	BindClosure
	// BindAttribute is a PHP 8 attribute: #[Bind], #[Singleton], #[ScopedBind].
	BindAttribute
)

// Binding represents one resolved service-container binding.
type Binding struct {
	// Abstract is the interface or abstract class that is resolved from the container.
	Abstract phputil.FQN
	// Concrete is the class that is instantiated. Empty when a closure body
	// could not be reduced to a single `new X(...)` call.
	Concrete phputil.FQN
	Kind     BindingKind
	// Lifetime is one of "transient", "singleton", "scoped", "instance".
	Lifetime string
	// Location is the jump target: the declaration of Concrete, or the closure site.
	Location phputil.Location
	// Source is the binding call or attribute, useful for diagnostics.
	Source phputil.Location
}

// BindingIndex is the service-container symbol table built during indexing.
// It maps each abstract FQN to one or more bindings (multiple providers can
// bind the same interface).
type BindingIndex struct {
	byAbstract map[phputil.FQN][]Binding
}

// NewBindingIndex returns an empty, ready-to-use index.
func NewBindingIndex() *BindingIndex {
	return &BindingIndex{byAbstract: make(map[phputil.FQN][]Binding)}
}

// Add inserts a binding into the index.
func (idx *BindingIndex) Add(b Binding) {
	idx.byAbstract[b.Abstract] = append(idx.byAbstract[b.Abstract], b)
}

// Lookup returns all bindings for the given abstract FQN.
// Returns nil (not an error) when no binding is found.
func (idx *BindingIndex) Lookup(abstract phputil.FQN) []Binding {
	return idx.byAbstract[abstract]
}

// All returns every binding in the index, in unspecified order.
func (idx *BindingIndex) All() []Binding {
	var out []Binding
	for _, bindings := range idx.byAbstract {
		out = append(out, bindings...)
	}
	return out
}
