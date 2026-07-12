package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
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

	edit := buildWorkspaceEdit(reps, docs)
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
		kind  eloquent.AttributeKind
		input string
		want  string
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

func TestPrepareRename_DeclarationSites(t *testing.T) {
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

	tests := []struct {
		name      string
		needle    string // cursor lands just past this prefix
		wantToken string
	}{
		{
			name:      "modern accessor method name",
			needle:    "function email",
			wantToken: "emailAddress",
		},
		{
			name:      "fillable array entry",
			needle:    "$fillable = ['email",
			wantToken: "'email_address'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := bytes.Index(src, []byte(tt.needle))
			if idx < 0 {
				t.Fatalf("needle %q not found in fixture", tt.needle)
			}
			offset := idx + len(tt.needle)

			sym, tokenLoc := identifySymbolWithLoc(src, userPath, offset, bindings, models)
			if sym == nil || !sym.isEloquent() {
				t.Fatalf("expected Eloquent symbol, got %+v", sym)
			}
			if tokenLoc.Zero() {
				t.Fatal("tokenLoc is zero — PrepareRename rejects declaration-site rename")
			}
			if tokenLoc.Path != userPath {
				t.Errorf("tokenLoc.Path = %q, want %q", tokenLoc.Path, userPath)
			}
			if got := string(src[tokenLoc.StartByte:tokenLoc.EndByte]); got != tt.wantToken {
				t.Errorf("token = %q, want %q", got, tt.wantToken)
			}
		})
	}
}

func TestRename_RejectsInvalidNewName(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	s := newTestServer(container.NewBindingIndex(), models)
	s.root = modelsRoot
	s.cfg.ReferenceDirs = []string{"."}

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s.docs.Set(PathToURI(userPath), src)

	idx := bytes.Index(src, []byte("'hi ' . $this->email_address"))
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + len("'hi ' . $this->") + 2
	line, col := byteOffsetToLineCol(src, offset)
	params := func(newName string) *protocol.RenameParams {
		return &protocol.RenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: PathToURI(userPath)},
				Position:     protocol.Position{Line: uint32(line), Character: uint32(col)},
			},
			NewName: newName,
		}
	}

	for _, bad := range []string{"", "FooBar", "contact-email", "9lives", "contact email", "contact_émail"} {
		edit, renameErr := s.Rename(nil, params(bad))
		if renameErr == nil {
			t.Errorf("Rename(%q): expected error, got nil (edit=%v)", bad, edit)
		}
		if edit != nil {
			t.Errorf("Rename(%q): expected nil edit, got %v", bad, edit)
		}
	}

	edit, renameErr := s.Rename(nil, params("contact_email"))
	if renameErr != nil {
		t.Fatalf("Rename(valid): unexpected error %v", renameErr)
	}
	if edit == nil {
		t.Fatal("Rename(valid): expected a WorkspaceEdit, got nil")
	}
}

func TestCollectDeclReplacements_ArrayDeclarations(t *testing.T) {
	root := t.TempDir()
	modelDir := filepath.Join(root, "app", "Models")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	model := `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
class User extends Model {
    protected $fillable = ['nickname', 'email'];
    protected $casts = ['nickname' => 'string'];
    protected $appends = ["nickname"];
    protected $hidden = ['secret'];
    protected function casts(): array
    {
        return ['nickname' => 'encrypted'];
    }
}`
	userPath := filepath.Join(modelDir, "User.php")
	if err := os.WriteFile(userPath, []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}

	models, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}

	sym := &refSymbol{modelFQN: "App\\Models\\User", propName: "nickname"}
	reps := collectDeclReplacements(sym, models, "alias", newDocumentStore())

	// $fillable entry, $casts key, $appends entry (double-quoted), and the
	// casts() method key — four array-declaration sites.
	if len(reps) != 4 {
		for _, r := range reps {
			t.Logf("replacement: %+v -> %q", r.loc, r.newText)
		}
		t.Fatalf("replacements = %d, want 4", len(reps))
	}
	for _, r := range reps {
		old := string(src[r.loc.StartByte:r.loc.EndByte])
		quote := old[0]
		if old != string(quote)+"nickname"+string(quote) {
			t.Errorf("replacement targets %q, want a quoted 'nickname' string", old)
		}
		if r.newText != string(quote)+"alias"+string(quote) {
			t.Errorf("newText = %q, want quote-preserving %q", r.newText, string(quote)+"alias"+string(quote))
		}
	}
}
