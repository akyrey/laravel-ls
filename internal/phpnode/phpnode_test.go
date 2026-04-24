package phpnode_test

import (
	"testing"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
)

// php81Standard is a realistic Laravel model using only PHP 8.1 constructs.
const php81Standard = `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Casts\Attribute;

class User extends Model {
    protected $fillable = ['email_address'];

    public function emailAddress(): Attribute {
        return Attribute::make(get: fn($v) => strtolower($v));
    }
}
`

// php84PropertyHooks exercises PHP 8.4 property hook syntax.
const php84PropertyHooks = `<?php
namespace App\Models;

class Sku {
    public string $price {
        get { return $this->raw_price; }
        set(string $value) { $this->raw_price = $value; }
    }
    public private(set) int $id = 0;
}
`

// php82ReadonlyClass exercises the PHP 8.2 readonly class modifier.
const php82ReadonlyClass = `<?php
readonly class Point {
    public function __construct(
        public float $x,
        public float $y,
    ) {}
}
`

func TestParseBytes_PHP81_NoError(t *testing.T) {
	tree, err := phpnode.ParseBytes([]byte(php81Standard))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		t.Errorf("PHP 8.1 standard code should parse without errors")
	}
}

func TestParseBytes_PHP84PropertyHooks_NoError(t *testing.T) {
	tree, err := phpnode.ParseBytes([]byte(php84PropertyHooks))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		t.Errorf("PHP 8.4 property hooks should parse without errors; sexp: %s",
			tree.RootNode().ToSexp())
	}
}

func TestParseBytes_PHP82ReadonlyClass_NoError(t *testing.T) {
	tree, err := phpnode.ParseBytes([]byte(php82ReadonlyClass))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		t.Errorf("PHP 8.2 readonly class should parse without errors; sexp: %s",
			tree.RootNode().ToSexp())
	}
}

func TestFromNode_ByteOffsets(t *testing.T) {
	src := []byte("<?php\nclass Foo {}\n")
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	// Find the class_declaration node.
	var classNode interface{ StartByte() uint }
	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() == "class_declaration" {
			classNode = child
			loc := phpnode.FromNode("/fake.php", child)
			if loc.Path != "/fake.php" {
				t.Errorf("path: got %q, want /fake.php", loc.Path)
			}
			if loc.StartLine != 2 {
				t.Errorf("StartLine: got %d, want 2", loc.StartLine)
			}
			if loc.StartByte != int(child.StartByte()) {
				t.Errorf("StartByte mismatch")
			}
			if loc.EndByte != int(child.EndByte()) {
				t.Errorf("EndByte mismatch")
			}
			break
		}
	}
	if classNode == nil {
		t.Fatal("class_declaration node not found")
	}
}
