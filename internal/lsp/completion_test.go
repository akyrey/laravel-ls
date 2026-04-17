package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func TestLhsBeforeArrow(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"$user->", "$user"},
		{"$user->em", "$user"},
		{"$this->email_address", "$this"},
		{"$user ->", "$user"},
		{"->", ""},         // no variable before arrow
		{"$->", ""},        // empty name
		{"$user>", ""},     // not ->
		{"plain text", ""}, // no arrow
	}
	for _, tc := range cases {
		got := lhsBeforeArrow([]byte(tc.src), len(tc.src))
		if got != tc.want {
			t.Errorf("lhsBeforeArrow(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestEloquentCompletions_ThisArrow(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// Source where cursor is after $this->
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Casts\Attribute;
class User extends Model {
    public function test(): string {
        return $this->
    }
}`)
	// offset just after "$this->"
	needle := []byte("$this->")
	offset := strings.Index(string(src), string(needle)) + len(needle)

	items := eloquentCompletions(src, "/fake/User.php", offset, models)
	if len(items) == 0 {
		t.Fatal("expected completion items for $this->, got none")
	}

	labels := make(map[string]bool)
	for _, it := range items {
		labels[it.Label] = true
	}

	for _, want := range []string{"email_address", "first_name", "posts"} {
		if !labels[want] {
			t.Errorf("want label %q in completions, not found", want)
		}
	}
}

func TestEloquentCompletions_TypedParam(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function show(User $user): string {
        return $user->
    }
}`)
	needle := []byte("$user->")
	// find the one inside show()
	offset := strings.LastIndex(string(src), string(needle)) + len(needle)

	items := eloquentCompletions(src, "/fake/Ctrl.php", offset, models)
	if len(items) == 0 {
		t.Fatal("expected completions for typed param $user->, got none")
	}
	labels := make(map[string]bool)
	for _, it := range items {
		labels[it.Label] = true
	}
	if !labels["email_address"] {
		t.Errorf("want email_address in completions")
	}
}

func TestEloquentCompletions_Assignment(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function show(int $id): string {
        $user = User::find($id);
        return $user->
    }
}`)
	needle := []byte("return $user->")
	offset := strings.Index(string(src), string(needle)) + len(needle)

	items := eloquentCompletions(src, "/fake/Ctrl.php", offset, models)
	if len(items) == 0 {
		t.Fatal("expected completions for assigned $user->, got none")
	}
	labels := make(map[string]bool)
	for _, it := range items {
		labels[it.Label] = true
	}
	if !labels["email_address"] {
		t.Errorf("want email_address in completions")
	}
}

func TestEloquentCompletions_NoMatch(t *testing.T) {
	models := eloquent.NewModelIndex()
	src := []byte(`<?php $unknown->`)
	items := eloquentCompletions(src, "/fake.php", len(src), models)
	if len(items) != 0 {
		t.Errorf("expected no items for unknown var, got %d", len(items))
	}
}

