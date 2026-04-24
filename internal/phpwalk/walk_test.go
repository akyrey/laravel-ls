package phpwalk_test

import (
	"testing"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// collectingVisitor records every event for assertion.
type collectingVisitor struct {
	phpwalk.NullVisitor

	namespaces []string
	useItems   [][2]string // [alias, fqn]
	classes    []phpwalk.ClassInfo
	interfaces []phpwalk.InterfaceInfo
	methods    []phpwalk.MethodInfo
	properties []phpwalk.PropertyInfo
	propFetches []phpwalk.PropertyFetchInfo
	constFetches []phpwalk.ClassConstFetchInfo
	newExprs   []phpwalk.NewExprInfo
	staticCalls []phpwalk.StaticCallInfo
	methodCalls []phpwalk.MethodCallInfo
	assigns    []phpwalk.AssignInfo
}

func (v *collectingVisitor) VisitNamespace(ns string) { v.namespaces = append(v.namespaces, ns) }
func (v *collectingVisitor) VisitUseItem(alias, fqn string) {
	v.useItems = append(v.useItems, [2]string{alias, fqn})
}
func (v *collectingVisitor) VisitClass(n phpwalk.ClassInfo)       { v.classes = append(v.classes, n) }
func (v *collectingVisitor) VisitInterface(n phpwalk.InterfaceInfo) { v.interfaces = append(v.interfaces, n) }
func (v *collectingVisitor) VisitClassMethod(n phpwalk.MethodInfo) { v.methods = append(v.methods, n) }
func (v *collectingVisitor) VisitProperty(n phpwalk.PropertyInfo) { v.properties = append(v.properties, n) }
func (v *collectingVisitor) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
	v.propFetches = append(v.propFetches, n)
}
func (v *collectingVisitor) VisitClassConstFetch(n phpwalk.ClassConstFetchInfo) {
	v.constFetches = append(v.constFetches, n)
}
func (v *collectingVisitor) VisitNew(n phpwalk.NewExprInfo) { v.newExprs = append(v.newExprs, n) }
func (v *collectingVisitor) VisitStaticCall(n phpwalk.StaticCallInfo) {
	v.staticCalls = append(v.staticCalls, n)
}
func (v *collectingVisitor) VisitMethodCall(n phpwalk.MethodCallInfo) {
	v.methodCalls = append(v.methodCalls, n)
}
func (v *collectingVisitor) VisitAssign(n phpwalk.AssignInfo) { v.assigns = append(v.assigns, n) }

const fixture = `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Casts\Attribute;
use App\{Baz, Qux as Q};

class User extends Model {
    protected $fillable = ['email', 'name'];
    protected $casts = ['dob' => 'date'];

    public function emailAddress(): Attribute {
        return Attribute::make(get: fn($v) => $v);
    }

    public function show(User $user): string {
        $x = new User();
        $y = User::find(1);
        $z = $user->email;
        App::bind(Foo::class, Bar::class);
        $this->app->singleton(Baz::class, fn() => new Baz());
        return $x instanceof User ? '' : '';
    }
}

interface Countable {}
`

func TestWalk_CollectsAllEvents(t *testing.T) {
	src := []byte(fixture)
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer tree.Close()

	v := &collectingVisitor{}
	phpwalk.Walk("", src, tree, v)

	// Namespace
	if len(v.namespaces) != 1 || v.namespaces[0] != "App\\Models" {
		t.Errorf("namespaces: got %v, want [App\\Models]", v.namespaces)
	}

	// Use items — regular + group
	wantUses := map[string]string{
		"Model":     "Illuminate\\Database\\Eloquent\\Model",
		"Attribute": "Illuminate\\Database\\Eloquent\\Casts\\Attribute",
		"Baz":       "App\\Baz",
		"Q":         "App\\Qux",
	}
	for _, u := range v.useItems {
		alias, fqn := u[0], u[1]
		if want, ok := wantUses[alias]; ok {
			if fqn != want {
				t.Errorf("use %s: got fqn %q, want %q", alias, fqn, want)
			}
			delete(wantUses, alias)
		}
	}
	for alias := range wantUses {
		t.Errorf("missing use item for alias %q", alias)
	}

	// Classes
	if len(v.classes) != 1 || v.classes[0].NameText != "User" {
		t.Errorf("classes: got %+v", v.classes)
	}
	if v.classes[0].ExtendsText != "Model" {
		t.Errorf("extends: got %q, want Model", v.classes[0].ExtendsText)
	}

	// Interfaces
	if len(v.interfaces) != 1 || v.interfaces[0].NameText != "Countable" {
		t.Errorf("interfaces: got %+v", v.interfaces)
	}

	// Methods
	methodNames := make(map[string]bool)
	for _, m := range v.methods {
		methodNames[m.Name] = true
	}
	for _, want := range []string{"emailAddress", "show"} {
		if !methodNames[want] {
			t.Errorf("method %q not found; got %v", want, methodNames)
		}
	}

	// Return type of emailAddress
	for _, m := range v.methods {
		if m.Name == "emailAddress" && m.ReturnTypeText != "Attribute" {
			t.Errorf("emailAddress return type: got %q, want Attribute", m.ReturnTypeText)
		}
	}

	// Parameters of show
	for _, m := range v.methods {
		if m.Name == "show" {
			if len(m.Params) == 0 {
				t.Errorf("show: no params extracted")
			} else if m.Params[0].VarName != "$user" || m.Params[0].TypeText != "User" {
				t.Errorf("show param[0]: got %+v", m.Params[0])
			}
		}
	}

	// Properties
	propNames := make(map[string]bool)
	for _, p := range v.properties {
		propNames[p.PropName] = true
	}
	for _, want := range []string{"fillable", "casts"} {
		if !propNames[want] {
			t.Errorf("property %q not found; got %v", want, propNames)
		}
	}

	// Property fetches ($user->email, $x->..., etc.)
	fetchNames := make(map[string]bool)
	for _, pf := range v.propFetches {
		fetchNames[pf.PropName] = true
	}
	if !fetchNames["email"] {
		t.Errorf("expected property fetch for 'email'; got %v", fetchNames)
	}

	// Class const fetches (Foo::class, Bar::class, Baz::class, User::class from instanceof)
	constNames := make(map[string]bool)
	for _, cf := range v.constFetches {
		constNames[cf.ClassName+"/"+cf.ConstName] = true
	}
	if !constNames["Foo/class"] {
		t.Errorf("expected Foo::class; got %v", constNames)
	}

	// new User(), new Baz()
	newNames := make(map[string]bool)
	for _, ne := range v.newExprs {
		newNames[ne.ClassName] = true
	}
	if !newNames["User"] {
		t.Errorf("expected new User; got %v", newNames)
	}

	// Static calls: User::find, App::bind
	staticMethods := make(map[string]bool)
	for _, sc := range v.staticCalls {
		staticMethods[sc.ClassName+"::"+sc.MethodName] = true
	}
	if !staticMethods["User::find"] {
		t.Errorf("expected User::find; got %v", staticMethods)
	}

	// Method calls: $this->app->singleton
	methodCallNames := make(map[string]bool)
	for _, mc := range v.methodCalls {
		methodCallNames[mc.MethodName] = true
	}
	if !methodCallNames["singleton"] {
		t.Errorf("expected singleton method call; got %v", methodCallNames)
	}

	// Assignments: $x = new ..., $y = User::find, $z = $user->email
	assignVars := make(map[string]bool)
	for _, a := range v.assigns {
		if a.VarName != "" {
			assignVars[a.VarName] = true
		}
	}
	for _, want := range []string{"$x", "$y", "$z"} {
		if !assignVars[want] {
			t.Errorf("assignment %q not found; got %v", want, assignVars)
		}
	}
}

func TestWalk_NullableReturnType(t *testing.T) {
	src := []byte(`<?php
class Foo {
    public function bar(): ?string { return null; }
    public function baz(): string { return ''; }
}
`)
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer tree.Close()

	v := &collectingVisitor{}
	phpwalk.Walk("", src, tree, v)

	for _, m := range v.methods {
		switch m.Name {
		case "bar":
			if m.ReturnTypeText != "string" {
				t.Errorf("bar: want return type 'string' (unwrapped), got %q", m.ReturnTypeText)
			}
		case "baz":
			if m.ReturnTypeText != "string" {
				t.Errorf("baz: want return type 'string', got %q", m.ReturnTypeText)
			}
		}
	}
}
