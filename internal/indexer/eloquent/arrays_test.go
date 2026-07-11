package eloquent

import "testing"

func TestExtractArrayProperties_CastsWithNonStringValues(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use App\Enums\UserStatus;
class User extends Model {
    protected $casts = [
        'status' => UserStatus::class,
        'email_verified_at' => 'datetime',
        'options' => \Illuminate\Database\Eloquent\Casts\AsCollection::class,
        'settings' => [self::class, 'castSettings'],
    ];
}`)
	raw, _, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractArrayProperties("/fake/User.php", raw, src)

	seen := map[string]bool{}
	for _, a := range attrs {
		if a.Kind != CastArray {
			t.Errorf("attribute %q has kind %d, want CastArray", a.ExposedName, a.Kind)
		}
		seen[a.ExposedName] = true
	}
	for _, want := range []string{"status", "email_verified_at", "options", "settings"} {
		if !seen[want] {
			t.Errorf("cast key %q not indexed; got %v", want, seen)
		}
	}
}

func TestExtractArrayProperties_CastsWithoutKeyIgnored(t *testing.T) {
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
class User extends Model {
    protected $casts = ['orphan_value'];
}`)
	raw, _, tree := firstClassNode(t, src)
	defer tree.Close()

	attrs := extractArrayProperties("/fake/User.php", raw, src)
	for _, a := range attrs {
		t.Errorf("unexpected attribute %q from keyless cast entry", a.ExposedName)
	}
}
