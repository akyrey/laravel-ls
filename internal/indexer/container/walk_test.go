package container_test

import (
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

const fixtureRoot = "../../../testdata/bindings"

// Walk the entire fixture directory (treating it as the project root, scanning ".").
func walkFixtures(t *testing.T) *container.BindingIndex {
	t.Helper()
	idx, err := container.Walk(fixtureRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return idx
}

func TestAttrBinding_Bind(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\PaymentGateway"
	bindings := idx.Lookup(abstract)
	if len(bindings) == 0 {
		t.Fatalf("no bindings found for %s", abstract)
	}

	// PaymentGateway is bound via #[Bind(StripeGateway::class)] (attribute)
	// AND via AppServiceProvider->bind (call). Both should appear.
	found := false
	for _, b := range bindings {
		if b.Kind == container.BindAttribute && b.Concrete == "App\\Services\\StripeGateway" {
			found = true
			if b.Lifetime != "transient" {
				t.Errorf("Lifetime = %q, want %q", b.Lifetime, "transient")
			}
		}
	}
	if !found {
		t.Errorf("expected BindAttribute binding from #[Bind], got %+v", bindings)
	}
}

func TestCallBinding_Bind(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\PaymentGateway"
	bindings := idx.Lookup(abstract)
	found := false
	for _, b := range bindings {
		if b.Kind == container.BindCall && b.Concrete == "App\\Services\\StripeGateway" && b.Lifetime == "transient" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BindCall binding from AppServiceProvider, got %+v", bindings)
	}
}

func TestCallBinding_Singleton(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\Mailer"
	bindings := idx.Lookup(abstract)
	found := false
	for _, b := range bindings {
		if b.Kind == container.BindCall && b.Concrete == "App\\Services\\SmtpMailer" && b.Lifetime == "singleton" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected singleton BindCall for Mailer, got %+v", bindings)
	}
}

func TestCallBinding_FacadeBind(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\Queue"
	bindings := idx.Lookup(abstract)
	found := false
	for _, b := range bindings {
		if b.Kind == container.BindCall && b.Concrete == "App\\Services\\SqsQueue" && b.Lifetime == "transient" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BindCall from App facade for Queue, got %+v", bindings)
	}
}

func TestClosureBinding_Resolvable(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\Cache"
	bindings := idx.Lookup(abstract)
	found := false
	for _, b := range bindings {
		// Closure body: `return new RedisCache(...)` — concrete is resolvable.
		if b.Kind == container.BindClosure && b.Concrete == "App\\Services\\RedisCache" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BindClosure with concrete RedisCache for Cache, got %+v", bindings)
	}
}

func TestClosureBinding_ArrowFn(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\Logger"
	bindings := idx.Lookup(abstract)
	found := false
	for _, b := range bindings {
		if b.Kind == container.BindClosure && b.Concrete == "App\\Services\\FileLogger" && b.Lifetime == "singleton" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BindClosure singleton from arrow fn for Logger, got %+v", bindings)
	}
}

func TestConcreteLocation_FilledFromSymbolTable(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	const abstract phputil.FQN = "App\\Contracts\\Mailer"
	bindings := idx.Lookup(abstract)
	for _, b := range bindings {
		if b.Kind == container.BindCall && b.Concrete == "App\\Services\\SmtpMailer" {
			if b.Location.Zero() {
				t.Errorf("expected non-zero Location for SmtpMailer binding, got zero")
			}
			return
		}
	}
	t.Errorf("binding for Mailer->SmtpMailer not found")
}

func TestNonServiceProvider_NotIndexed(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	// StripeGateway is not a ServiceProvider; any bind calls inside it (there are none,
	// but this verifies the guard) must not appear in the index from that class.
	// We verify by checking that no binding has StripeGateway as Abstract (it's only Concrete).
	for _, b := range idx.All() {
		if b.Abstract == "App\\Services\\StripeGateway" {
			t.Errorf("unexpected binding with StripeGateway as abstract: %+v", b)
		}
	}
}
