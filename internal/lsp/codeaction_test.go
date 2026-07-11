package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
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
	if te.Range.Start != te.Range.End {
		t.Errorf("expected point insertion, got range %v", te.Range)
	}
	if !strings.Contains(te.NewText, "'phone_number'") {
		t.Errorf("NewText %q does not contain 'phone_number'", te.NewText)
	}
	if !strings.HasPrefix(te.NewText, ", ") {
		t.Errorf("expected leading comma for non-empty array, got %q", te.NewText)
	}
}

func TestBuildAddToFillableEdit_NoFillable(t *testing.T) {
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
	if err := fv.scan(src); err != nil {
		t.Fatalf("scan: %v", err)
	}

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
	if err := fv.scan(src); err != nil {
		t.Fatalf("scan: %v", err)
	}

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

func TestBuildCreateAccessorEdit_ExistingImportUsesAlias(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Casts\Attribute;

class User extends Model
{
    protected $fillable = ['email_address'];
}
`)
	edit := buildCreateAccessorEdit("/fake/User.php", src, "phone_number")
	if edit == nil {
		t.Fatal("expected non-nil edit")
	}
	uri := PathToURI("/fake/User.php")
	edits, ok := edit.Changes[uri]
	if !ok || len(edits) == 0 {
		t.Fatal("no edits for User.php")
	}
	newText := edits[0].NewText
	if !strings.Contains(newText, "public function phoneNumber(): Attribute") {
		t.Errorf("NewText = %q, want method signature using bare Attribute alias", newText)
	}
	if !strings.Contains(newText, "return Attribute::make(") {
		t.Errorf("NewText = %q, want Attribute::make(...) using the imported alias", newText)
	}
}

func TestBuildCreateAccessorEdit_NoImportUsesFullyQualifiedName(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;

class Post extends Model
{
}
`)
	edit := buildCreateAccessorEdit("/fake/Post.php", src, "slug_url")
	if edit == nil {
		t.Fatal("expected non-nil edit")
	}
	uri := PathToURI("/fake/Post.php")
	newText := edit.Changes[uri][0].NewText
	want := "public function slugUrl(): \\Illuminate\\Database\\Eloquent\\Casts\\Attribute"
	if !strings.Contains(newText, want) {
		t.Errorf("NewText = %q, want it to contain %q", newText, want)
	}
}

func TestBuildCreateAccessorEdit_NoClassDeclaration(t *testing.T) {
	src := []byte(`<?php
// no class here
`)
	if edit := buildCreateAccessorEdit("/fake/Empty.php", src, "x"); edit != nil {
		t.Errorf("expected nil edit when there is no class declaration, got %+v", edit)
	}
}

func TestCodeAction_IncludesCreateAccessorAction(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	s := newTestServer(nil, models)
	uri := PathToURI(userPath)
	s.docs.Set(uri, src)

	diagSrc := diagSource
	diag := protocol.Diagnostic{
		Source:  &diagSrc,
		Message: "unknown property 'phone_number' on App\\Models\\User",
	}

	result, err := s.CodeAction(&glsp.Context{}, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Context:      protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{diag}},
	})
	if err != nil {
		t.Fatalf("CodeAction: %v", err)
	}
	actions, ok := result.([]protocol.CodeAction)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}

	var found *protocol.CodeAction
	for i := range actions {
		if actions[i].Title == "Generate accessor for 'phone_number'" {
			found = &actions[i]
		}
	}
	if found == nil {
		titles := make([]string, len(actions))
		for i, a := range actions {
			titles[i] = a.Title
		}
		t.Fatalf("expected a 'Generate accessor' action; got titles: %v", titles)
	}
	newText := found.Edit.Changes[uri][0].NewText
	if !strings.Contains(newText, "public function phoneNumber(): Attribute") {
		t.Errorf("accessor action NewText = %q, missing expected method signature", newText)
	}
}

func TestCodeAction_NoActionsForUnknownSource(t *testing.T) {
	diags := []protocol.Diagnostic{
		{Message: "unknown property 'foo' on App\\Models\\User"},
	}
	_ = diags
	edit := buildAddToFillableEdit("/nonexistent.php", []byte{}, "x")
	if edit != nil {
		t.Error("expected nil edit for empty src")
	}
}
