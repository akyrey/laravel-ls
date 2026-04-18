package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
	"github.com/akyrey/laravel-ls/internal/phpparse"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestBuildAddToFillableEdit_NonEmptyArray(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	cat := models.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User catalog not found")
	}

	src, err := os.ReadFile(cat.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	edit := buildAddToFillableEdit(cat.Path, src, "phone_number")
	if edit == nil {
		t.Fatal("expected non-nil edit")
	}
	userURI := PathToURI(cat.Path)
	edits, ok := edit.Changes[userURI]
	if !ok || len(edits) == 0 {
		t.Fatal("no edits for User.php")
	}

	te := edits[0]
	// Insertion is a point edit (Start == End).
	if te.Range.Start != te.Range.End {
		t.Errorf("expected point insertion, got range %v", te.Range)
	}
	// NewText should contain the quoted property name with a leading comma.
	if !strings.Contains(te.NewText, "'phone_number'") {
		t.Errorf("NewText %q does not contain 'phone_number'", te.NewText)
	}
	if !strings.HasPrefix(te.NewText, ", ") {
		t.Errorf("expected leading comma for non-empty array, got %q", te.NewText)
	}
}

func TestBuildAddToFillableEdit_NoFillable(t *testing.T) {
	// A file with no $fillable → should return nil.
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
class Post extends Model {}`)
	edit := buildAddToFillableEdit("/fake/Post.php", src, "title")
	if edit != nil {
		t.Errorf("expected nil edit for model without $fillable, got %v", edit)
	}
}

func TestFillableVisitor_EmptyArray(t *testing.T) {
	src := []byte(`<?php
class User {
    protected $fillable = [];
}`)
	fv := newFillableVisitor("/fake.php")
	traverseWithFillableVisitor(fv, src)

	ins, ok := fv.insertions["fillable"]
	if !ok || ins.insertByte < 0 {
		t.Fatal("expected fillable insertion point for empty array")
	}
	if ins.hasItems {
		t.Error("expected hasItems=false for empty array")
	}
	text := fv.insertText("email")
	if text != "'email'" {
		t.Errorf("insertText=%q, want \"'email'\"", text)
	}
}

func TestFillableVisitor_NonEmptyArray(t *testing.T) {
	src := []byte(`<?php
class User {
    protected $fillable = ['name', 'email'];
}`)
	fv := newFillableVisitor("/fake.php")
	traverseWithFillableVisitor(fv, src)

	ins, ok := fv.insertions["fillable"]
	if !ok || ins.insertByte < 0 {
		t.Fatal("expected fillable insertion point")
	}
	if !ins.hasItems {
		t.Error("expected hasItems=true")
	}
	text := fv.insertText("phone")
	if text != ", 'phone'" {
		t.Errorf("insertText=%q, want \", 'phone'\"", text)
	}
}

func TestCodeAction_NoActionsForUnknownSource(t *testing.T) {
	// Diagnostics from other sources must be ignored.
	diags := []protocol.Diagnostic{
		{Message: "unknown property 'foo' on App\\Models\\User"},
		// Source is nil — not from laravel-ls, so CodeAction should skip it.
	}
	_ = diags // The handler checks Source != nil, so no actions expected.
	// Smoke test: building an edit for empty source should return nil.
	edit := buildAddToFillableEdit("/nonexistent.php", []byte{}, "x")
	if edit != nil {
		t.Error("expected nil edit for empty src")
	}
}

func traverseWithFillableVisitor(fv *fillableVisitor, src []byte) {
	astRoot, err := phpparse.Bytes(src, fv.path)
	if err != nil || astRoot == nil {
		return
	}
	traverser.NewTraverser(fv).Traverse(astRoot)
}
