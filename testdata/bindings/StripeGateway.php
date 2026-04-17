<?php

namespace App\Services;

use App\Contracts\PaymentGateway;

class StripeGateway implements PaymentGateway
{
    public function charge(int $cents): void
    {
        // Stripe API call here
    }
}
