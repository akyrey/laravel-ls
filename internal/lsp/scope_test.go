package lsp

import (
	"path/filepath"
	"testing"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// propFetchCollector records every property-fetch and the single method body
// it lives in, so tests can grab the exact *ts.Node resolveExprType expects.
type propFetchCollector struct {
	phpwalk.NullVisitor
	fc      *phputil.FileContext
	method  *phpwalk.MethodInfo
	fetches []phpwalk.PropertyFetchInfo
}

func (v *propFetchCollector) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *propFetchCollector) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}
func (v *propFetchCollector) VisitClassMethod(n phpwalk.MethodInfo) { v.method = &n }
func (v *propFetchCollector) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
	v.fetches = append(v.fetches, n)
}

// findPropertyFetch parses src and returns the PropertyFetchInfo whose
// PropName equals want, along with the enclosing method and file context.
// The returned *ts.Node values are only valid while tree stays open —
// callers must defer tree.Close() themselves, AFTER they're done using
// the returned nodes.
func findPropertyFetch(t *testing.T, src []byte, want string) (phpwalk.PropertyFetchInfo, *phpwalk.MethodInfo, *phputil.FileContext, *ts.Tree) {
	t.Helper()
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	pv := &propFetchCollector{fc: &phputil.FileContext{Uses: make(phputil.UseMap)}}
	phpwalk.Walk("", src, tree, pv)

	for _, pf := range pv.fetches {
		if pf.PropName == want {
			return pf, pv.method, pv.fc, tree
		}
	}
	t.Fatalf("property fetch %q not found in fixture", want)
	return phpwalk.PropertyFetchInfo{}, nil, nil, nil
}

func testModelIndex(t *testing.T) *eloquent.ModelIndex {
	t.Helper()
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	idx, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	return idx
}

func TestResolveExprType_This(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function test() {
        return $this->email_address;
    }
}
`)
	pf, method, fc, tree := findPropertyFetch(t, src, "email_address")
	defer tree.Close()

	got := resolveExprType(pf.VarRaw, pf.Src, "App\\Models\\Scratch", method.Params, nil, fc, testModelIndex(t))
	if got != "App\\Models\\Scratch" {
		t.Errorf("resolveExprType($this) = %q, want %q", got, "App\\Models\\Scratch")
	}
}

func TestResolveExprType_TypedParam(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function test(User $u) {
        return $u->email_address;
    }
}
`)
	pf, method, fc, tree := findPropertyFetch(t, src, "email_address")
	defer tree.Close()

	got := resolveExprType(pf.VarRaw, pf.Src, "App\\Models\\Scratch", method.Params, nil, fc, testModelIndex(t))
	if got != "App\\Models\\User" {
		t.Errorf("resolveExprType($u typed param) = %q, want %q", got, "App\\Models\\User")
	}
}

func TestResolveExprType_AssignedFromNew(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function test() {
        $u = new User();
        return $u->email_address;
    }
}
`)
	pf, method, fc, tree := findPropertyFetch(t, src, "email_address")
	defer tree.Close()
	assignedVars := collectAssignments(*method, fc)

	got := resolveExprType(pf.VarRaw, pf.Src, "App\\Models\\Scratch", method.Params, assignedVars, fc, testModelIndex(t))
	if got != "App\\Models\\User" {
		t.Errorf("resolveExprType($u = new User()) = %q, want %q", got, "App\\Models\\User")
	}
}

func TestResolveExprType_AssignedFromStaticFind(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function test() {
        $u = User::find(1);
        return $u->email_address;
    }
}
`)
	pf, method, fc, tree := findPropertyFetch(t, src, "email_address")
	defer tree.Close()
	assignedVars := collectAssignments(*method, fc)

	got := resolveExprType(pf.VarRaw, pf.Src, "App\\Models\\Scratch", method.Params, assignedVars, fc, testModelIndex(t))
	if got != "App\\Models\\User" {
		t.Errorf("resolveExprType($u = User::find(1)) = %q, want %q", got, "App\\Models\\User")
	}
}

// TestResolveExprType_MultiHopRelationshipChain exercises $u->posts->author on
// a typed $u User param: posts() is a HasMany<Post> relationship, author() is
// an untyped BelongsTo<User> relationship declared on Post. resolveExprType
// recurses on member_access_expression, so it should follow both hops even
// though CLAUDE.md documents chained access as "one Relationship hop only".
func TestResolveExprType_MultiHopRelationshipChain(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function test(User $u) {
        return $u->posts->author->email_address;
    }
}
`)
	pf, method, fc, tree := findPropertyFetch(t, src, "email_address")
	defer tree.Close()

	got := resolveExprType(pf.VarRaw, pf.Src, "App\\Models\\Scratch", method.Params, nil, fc, testModelIndex(t))
	if got != "App\\Models\\User" {
		t.Errorf("resolveExprType($u->posts->author) = %q, want %q (two relationship hops)", got, "App\\Models\\User")
	}
}

func TestResolveExprType_UnknownVariable(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
class Scratch {
    public function test() {
        return $mystery->email_address;
    }
}
`)
	pf, method, fc, tree := findPropertyFetch(t, src, "email_address")
	defer tree.Close()

	got := resolveExprType(pf.VarRaw, pf.Src, "App\\Models\\Scratch", method.Params, nil, fc, testModelIndex(t))
	if got != "" {
		t.Errorf("resolveExprType($mystery) = %q, want empty", got)
	}
}

func TestResolveExprType_NilExpr(t *testing.T) {
	got := resolveExprType(nil, nil, "", nil, nil, &phputil.FileContext{Uses: make(phputil.UseMap)}, testModelIndex(t))
	if got != "" {
		t.Errorf("resolveExprType(nil) = %q, want empty", got)
	}
}
