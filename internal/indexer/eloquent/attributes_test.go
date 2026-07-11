package eloquent

import (
	"testing"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// firstClassNode parses src, walks it, and returns the raw node of the first
// class declaration plus its FileContext. The returned *ts.Tree must be
// closed by the caller after use.
func firstClassNode(t *testing.T, src []byte) (*ts.Node, *phputil.FileContext, *ts.Tree) {
	t.Helper()
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	fc := &phputil.FileContext{Uses: make(phputil.UseMap)}
	var raw *ts.Node
	cv := &classNodeCollector{fc: fc, raw: &raw}
	phpwalk.Walk("", src, tree, cv)
	if raw == nil {
		t.Fatal("no class declaration found in fixture")
	}
	return raw, fc, tree
}

type classNodeCollector struct {
	phpwalk.NullVisitor
	fc  *phputil.FileContext
	raw **ts.Node
}

func (v *classNodeCollector) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *classNodeCollector) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}
func (v *classNodeCollector) VisitClass(n phpwalk.ClassInfo) {
	if *v.raw == nil {
		*v.raw = n.Raw
	}
}

func attrByMethod(attrs []ModelAttribute, methodName string) (ModelAttribute, bool) {
	for _, a := range attrs {
		if a.MethodName == methodName {
			return a, true
		}
	}
	return ModelAttribute{}, false
}

func TestExtractMethods_PlainMethodNoMatch(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function totalPrice(): int
    {
        return 100;
    }
}
`)
	raw, fc, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractMethods("test.php", raw, src, fc)
	if _, ok := attrByMethod(attrs, "totalPrice"); ok {
		t.Errorf("plain non-matching method should not produce a ModelAttribute, got %+v", attrs)
	}
}

func TestExtractMethods_NonRelationBuilderCallIgnored(t *testing.T) {
	// with(Foo::class) is not in relationBuilderMethods, so this must not be
	// classified as a Relationship even though it's a $this-> call with a
	// Class::class argument.
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function scopeEager()
    {
        return $this->with(Foo::class);
    }
}
`)
	raw, fc, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractMethods("test.php", raw, src, fc)
	if a, ok := attrByMethod(attrs, "scopeEager"); ok {
		t.Errorf("with(Foo::class) should not be classified as a Relationship, got %+v", a)
	}
}

func TestExtractRelatedFQN_NotOnThis(t *testing.T) {
	// The relation-builder call is on $other, not $this — must not resolve.
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function posts()
    {
        $other = $this;
        return $other->hasMany(Post::class);
    }
}
`)
	raw, fc, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractMethods("test.php", raw, src, fc)
	if a, ok := attrByMethod(attrs, "posts"); ok {
		t.Errorf("relation call on non-$this variable should not resolve, got %+v", a)
	}
}

func TestExtractRelatedFQN_ArgNotClassConst(t *testing.T) {
	// hasMany($related) — the argument is a variable, not a Class::class
	// expression, so no RelatedFQN can be extracted.
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function posts()
    {
        $related = 'Post';
        return $this->hasMany($related);
    }
}
`)
	raw, fc, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractMethods("test.php", raw, src, fc)
	if a, ok := attrByMethod(attrs, "posts"); ok {
		t.Errorf("relation call with non-class-const argument should not resolve, got %+v", a)
	}
}

func TestExtractMethods_LegacyAccessorAndMutatorBothPresent(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function getNameAttribute(string $value): string
    {
        return ucfirst($value);
    }

    public function setNameAttribute(string $value): void
    {
        $this->attributes['name'] = strtolower($value);
    }
}
`)
	raw, fc, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractMethods("test.php", raw, src, fc)
	getter, ok := attrByMethod(attrs, "getNameAttribute")
	if !ok || getter.Kind != LegacyAccessor || getter.ExposedName != "name" {
		t.Errorf("expected LegacyAccessor exposed as 'name', got %+v (ok=%v)", getter, ok)
	}
	setter, ok := attrByMethod(attrs, "setNameAttribute")
	if !ok || setter.Kind != LegacyMutator || setter.ExposedName != "name" {
		t.Errorf("expected LegacyMutator exposed as 'name', got %+v (ok=%v)", setter, ok)
	}
}
