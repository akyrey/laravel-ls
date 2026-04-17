<?php

namespace App\Providers;

use Illuminate\Support\ServiceProvider;
use App\Contracts\PaymentGateway;
use App\Services\StripeGateway;
use App\Contracts\Mailer;
use App\Services\SmtpMailer;

class AppServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        // Direct class binding (Feature A, BindCall, transient)
        $this->app->bind(PaymentGateway::class, StripeGateway::class);

        // Singleton binding
        $this->app->singleton(Mailer::class, SmtpMailer::class);
    }
}
