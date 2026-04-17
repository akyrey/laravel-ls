package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func TestRename_EloquentReferenceAndDeclaration(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "email_address" in "$this->email_address"
	needle := []byte("'hi ' . $this->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + len("'hi ' . $this->")

	sym := identifySymbol(src, userPath, offset, bindings, models)
	if sym == nil || !sym.isEloquent() {
		t.Fatalf("expected Eloquent symbol, got %+v", sym)
	}

	docs := newDocumentStore()
	docs.Set(PathToURI(userPath), src)

	const newPropName = "contact_email"

	// Scan reference sites.
	reps := scanRenameRefs(modelsRoot, []string{"."}, sym, docs, models, newPropName)
	if len(reps) == 0 {
		t.Fatal("scanRenameRefs returned no replacements")
	}
	for _, r := range reps {
		if r.newText != newPropName {
			t.Errorf("reference replacement newText=%q, want %q", r.newText, newPropName)
		}
	}

	// Declaration renames.
	declReps := collectDeclReplacements(sym, models, newPropName, docs)
	if len(declReps) == 0 {
		t.Fatal("collectDeclReplacements returned no replacements")
	}
	// emailAddress → contactEmail (camelCase of contact_email)
	wantMethod := "contactEmail"
	foundMethod := false
	for _, r := range declReps {
		if r.newText == wantMethod {
			foundMethod = true
			break
		}
	}
	if !foundMethod {
		t.Errorf("expected declaration replacement %q, got %v", wantMethod, declReps)
	}
}

func TestRename_EloquentBuildWorkspaceEdit(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	needle := []byte("'hi ' . $this->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + len("'hi ' . $this->")

	sym := identifySymbol(src, userPath, offset, bindings, models)
	if sym == nil {
		t.Fatal("identifySymbol returned nil")
	}

	docs := newDocumentStore()
	docs.Set(PathToURI(userPath), src)

	reps := scanRenameRefs(modelsRoot, []string{"."}, sym, docs, models, "new_prop")
	reps = append(reps, collectDeclReplacements(sym, models, "new_prop", docs)...)

	edit := buildWorkspaceEdit(reps)
	if edit == nil {
		t.Fatal("buildWorkspaceEdit returned nil")
	}
	if len(edit.Changes) == 0 {
		t.Fatal("WorkspaceEdit has no changes")
	}
	// Should include at least the User.php file.
	userURI := string(PathToURI(userPath))
	if _, ok := edit.Changes[userURI]; !ok {
		t.Errorf("expected changes for %s, got keys: %v", userURI, func() []string {
			var ks []string
			for k := range edit.Changes {
				ks = append(ks, k)
			}
			return ks
		}())
	}
}

func TestRename_NilOnNonEloquent(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models := eloquent.NewModelIndex()
	bindings := container.NewBindingIndex()

	src := []byte("<?php $x->unknownProp;")
	sym := identifySymbol(src, "/fake.php", 10, bindings, models)
	if sym != nil {
		t.Fatalf("expected nil symbol for unknown prop, got %+v", sym)
	}

	// A nil sym means Rename returns nil — verify scanRenameRefs is safe.
	docs := newDocumentStore()
	if sym == nil {
		// Nothing to do; the handler returns nil before reaching scan.
		return
	}
	reps := scanRenameRefs(modelsRoot, []string{"."}, sym, docs, models, "new_name")
	if len(reps) != 0 {
		t.Errorf("expected no replacements for unknown sym, got %d", len(reps))
	}
}

func TestPrepareRename_EloquentReturnsRange(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	needle := []byte("'hi ' . $this->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + len("'hi ' . $this->")

	sym, tokenLoc := identifySymbolWithLoc(src, userPath, offset, bindings, models)
	if sym == nil {
		t.Fatal("identifySymbolWithLoc returned nil symbol")
	}
	if tokenLoc.Zero() {
		t.Fatal("identifySymbolWithLoc returned zero tokenLoc")
	}
	// EndByte is exclusive, so EndByte - StartByte == len("email_address") == 13.
	wantLen := len("email_address")
	gotLen := tokenLoc.EndByte - tokenLoc.StartByte
	if gotLen != wantLen {
		t.Errorf("token span = %d bytes, want %d (%d-%d)", gotLen, wantLen, tokenLoc.StartByte, tokenLoc.EndByte)
	}
}

func TestRename_MethodNameFor(t *testing.T) {
	cases := []struct {
		kind    eloquent.AttributeKind
		input   string
		want    string
	}{
		{eloquent.ModernAccessor, "contact_email", "contactEmail"},
		{eloquent.LegacyAccessor, "first_name", "getFirstNameAttribute"},
		{eloquent.LegacyMutator, "first_name", "setFirstNameAttribute"},
		{eloquent.Relationship, "posts", "posts"},
	}
	for _, c := range cases {
		got := methodNameFor(c.kind, c.input)
		if got != c.want {
			t.Errorf("methodNameFor(%v, %q) = %q, want %q", c.kind, c.input, got, c.want)
		}
	}
}
