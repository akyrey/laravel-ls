package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func TestDefinition_ContainerLookup(t *testing.T) {
	bindingsRoot := filepath.Join("..", "..", "testdata", "bindings")
	bindings, err := container.Walk(bindingsRoot, []string{"."})
	if err != nil {
		t.Fatalf("container.Walk: %v", err)
	}
	models := eloquent.NewModelIndex()

	spPath := filepath.Join(bindingsRoot, "AppServiceProvider.php")
	src, err := os.ReadFile(spPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Find cursor position: on "PaymentGateway" inside "PaymentGateway::class"
	needle := []byte("PaymentGateway::class")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("PaymentGateway::class not found in fixture")
	}
	offset := idx // cursor on 'P'

	locs := findDefinition(src, spPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected at least one location, got none")
	}

	// Jump target must be StripeGateway.php
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "StripeGateway.php" {
		t.Errorf("want StripeGateway.php, got %s", got)
	}
}

func TestDefinition_ContainerFromNew(t *testing.T) {
	bindingsRoot := filepath.Join("..", "..", "testdata", "bindings")
	bindings, err := container.Walk(bindingsRoot, []string{"."})
	if err != nil {
		t.Fatalf("container.Walk: %v", err)
	}
	models := eloquent.NewModelIndex()

	ctrlPath := filepath.Join(bindingsRoot, "PaymentController.php")
	src, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "PaymentGateway" in "new PaymentGateway()"
	needle := []byte("new PaymentGateway()")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("new PaymentGateway() not found in fixture")
	}
	offset := idx + len("new ") // on 'P'

	locs := findDefinition(src, ctrlPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected container location via new, got none")
	}
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "StripeGateway.php" {
		t.Errorf("want StripeGateway.php, got %s", got)
	}
}

func TestDefinition_ContainerFromInstanceOf(t *testing.T) {
	bindingsRoot := filepath.Join("..", "..", "testdata", "bindings")
	bindings, err := container.Walk(bindingsRoot, []string{"."})
	if err != nil {
		t.Fatalf("container.Walk: %v", err)
	}
	models := eloquent.NewModelIndex()

	ctrlPath := filepath.Join(bindingsRoot, "PaymentController.php")
	src, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "PaymentGateway" in "$x instanceof PaymentGateway"
	needle := []byte("instanceof PaymentGateway")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("instanceof PaymentGateway not found in fixture")
	}
	offset := idx + len("instanceof ") // on 'P'

	locs := findDefinition(src, ctrlPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected container location via instanceof, got none")
	}
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "StripeGateway.php" {
		t.Errorf("want StripeGateway.php, got %s", got)
	}
}

func TestDefinition_EloquentPropertyAccess(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Find cursor on "email_address" in the return statement (not the comment above it).
	needle := []byte("'hi ' . $this->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("'hi ' . $this->email_address not found in fixture")
	}
	// "'hi ' . $this->" is 15 bytes; cursor on first char of "email_address"
	offset := idx + len("'hi ' . $this->")

	locs := findDefinition(src, userPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected at least one location, got none")
	}

	// First result must be User.php (the emailAddress() method)
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "User.php" {
		t.Errorf("want User.php, got %s", got)
	}
}

func TestDefinition_NilOnStringLiteral(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models := eloquent.NewModelIndex()

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor inside string literal "'hi '"
	needle := []byte("'hi '")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("'hi ' not found in fixture")
	}
	offset := idx + 1 // cursor on 'h' inside the string

	locs := findDefinition(src, userPath, offset, bindings, models)
	if len(locs) != 0 {
		t.Errorf("expected no locations for string literal, got %d", len(locs))
	}
}

func TestDefinition_EloquentFromStaticCall(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	ctrlPath := filepath.Join(modelsRoot, "UserController.php")
	src, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "email_address" in "$user->email_address" inside show()
	needle := []byte("$user = User::find($id);\n        return $user->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + bytes.Index(needle, []byte("$user->email_address")) + len("$user->")

	locs := findDefinition(src, ctrlPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected at least one location via User::find, got none")
	}
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "User.php" {
		t.Errorf("want User.php, got %s", got)
	}
}

func TestDefinition_EloquentFromNew(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	ctrlPath := filepath.Join(modelsRoot, "UserController.php")
	src, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "email_address" in "$user->email_address" inside create()
	needle := []byte("$user = new User();\n        return $user->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + bytes.Index(needle, []byte("$user->email_address")) + len("$user->")

	locs := findDefinition(src, ctrlPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected at least one location via new User(), got none")
	}
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "User.php" {
		t.Errorf("want User.php, got %s", got)
	}
}

func TestDefinition_EloquentRelationship(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	ctrlPath := filepath.Join(modelsRoot, "UserController.php")
	src, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "posts" in "$user->posts" inside posts()
	needle := []byte("$user = User::find($id);\n        return $user->posts->toArray()")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + bytes.Index(needle, []byte("$user->posts")) + len("$user->")

	locs := findDefinition(src, ctrlPath, offset, bindings, models)
	if len(locs) == 0 {
		t.Fatal("expected location for relationship, got none")
	}
	got := filepath.Base(URIToPath(locs[0].URI))
	if got != "User.php" {
		t.Errorf("want User.php, got %s", got)
	}
}

func TestDefinition_NilWhenIndexesEmpty(t *testing.T) {
	bindings := container.NewBindingIndex()
	models := eloquent.NewModelIndex()
	src := []byte("<?php $user->name;")
	locs := findDefinition(src, "/fake.php", 10, bindings, models)
	if len(locs) != 0 {
		t.Errorf("expected no locations with empty indexes, got %d", len(locs))
	}
}
