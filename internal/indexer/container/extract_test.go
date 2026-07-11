package container

import (
	"testing"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// declCollector records the raw node of the first class/interface declaration
// it sees, along with the FileContext built up from namespace/use events.
type declCollector struct {
	phpwalk.NullVisitor
	fc  *phputil.FileContext
	raw *ts.Node
}

func (v *declCollector) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *declCollector) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}
func (v *declCollector) VisitClass(n phpwalk.ClassInfo) {
	if v.raw == nil {
		v.raw = n.Raw
	}
}
func (v *declCollector) VisitInterface(n phpwalk.InterfaceInfo) {
	if v.raw == nil {
		v.raw = n.Raw
	}
}

// firstDecl parses src and returns the raw node of the first class/interface
// declaration plus its FileContext. The returned *ts.Tree must be closed by
// the caller after it's done using raw.
func firstDecl(t *testing.T, src []byte) (*ts.Node, *phputil.FileContext, *ts.Tree) {
	t.Helper()
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dc := &declCollector{fc: &phputil.FileContext{Uses: make(phputil.UseMap)}}
	phpwalk.Walk("", src, tree, dc)
	if dc.raw == nil {
		t.Fatal("no class/interface declaration found in fixture")
	}
	return dc.raw, dc.fc, tree
}

func TestExtractAttrBindings_Singleton(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
#[\Illuminate\Container\Attributes\Singleton(\App\Services\SmtpMailer::class)]
interface Mailer {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\Mailer", fc, "test.php")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d: %+v", len(bindings), bindings)
	}
	b := bindings[0]
	if b.Kind != BindAttribute || b.Lifetime != "singleton" || b.Concrete != "App\\Services\\SmtpMailer" {
		t.Errorf("got %+v, want Kind=BindAttribute Lifetime=singleton Concrete=App\\Services\\SmtpMailer", b)
	}
}

func TestExtractAttrBindings_ScopedBind(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
#[\Illuminate\Container\Attributes\ScopedBind(\App\Services\RequestLogger::class)]
interface Logger {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\Logger", fc, "test.php")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d: %+v", len(bindings), bindings)
	}
	b := bindings[0]
	if b.Kind != BindAttribute || b.Lifetime != "scoped" || b.Concrete != "App\\Services\\RequestLogger" {
		t.Errorf("got %+v, want Kind=BindAttribute Lifetime=scoped Concrete=App\\Services\\RequestLogger", b)
	}
}

func TestExtractAttrBindings_OnClassDeclaration(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
#[\Illuminate\Container\Attributes\Bind(\App\Services\StripeGateway::class)]
class PaymentGateway {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\PaymentGateway", fc, "test.php")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d: %+v", len(bindings), bindings)
	}
	if bindings[0].Concrete != "App\\Services\\StripeGateway" {
		t.Errorf("Concrete = %q, want %q", bindings[0].Concrete, "App\\Services\\StripeGateway")
	}
}

func TestExtractAttrBindings_MultipleAttributesInOneGroup(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
#[\Illuminate\Container\Attributes\Bind(\App\Services\StripeGateway::class), \Illuminate\Container\Attributes\Singleton(\App\Services\StripeGateway::class)]
interface PaymentGateway {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\PaymentGateway", fc, "test.php")
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings from one attribute group, got %d: %+v", len(bindings), bindings)
	}
	lifetimes := map[string]bool{}
	for _, b := range bindings {
		lifetimes[b.Lifetime] = true
	}
	if !lifetimes["transient"] || !lifetimes["singleton"] {
		t.Errorf("expected both transient and singleton lifetimes, got %+v", bindings)
	}
}

func TestExtractAttrBindings_UnknownAttributeIgnored(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
#[\Some\Other\Attribute(\App\Services\StripeGateway::class)]
interface PaymentGateway {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\PaymentGateway", fc, "test.php")
	if len(bindings) != 0 {
		t.Errorf("expected no bindings for an unrecognised attribute, got %+v", bindings)
	}
}

func TestExtractAttrBindings_NoArguments(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
#[\Illuminate\Container\Attributes\Bind]
interface PaymentGateway {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\PaymentGateway", fc, "test.php")
	if len(bindings) != 0 {
		t.Errorf("expected no bindings when the attribute has no arguments, got %+v", bindings)
	}
}

func TestExtractAttrBindings_NoAttributes(t *testing.T) {
	src := []byte(`<?php
namespace App\Contracts;
interface PaymentGateway {}
`)
	raw, fc, tree := firstDecl(t, src)
	defer tree.Close()

	bindings := extractAttrBindings(raw, src, "App\\Contracts\\PaymentGateway", fc, "test.php")
	if len(bindings) != 0 {
		t.Errorf("expected no bindings when there are no attributes at all, got %+v", bindings)
	}
}
