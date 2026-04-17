<?php

namespace App\Http\Controllers;

use App\Contracts\PaymentGateway;

class PaymentController
{
    public function charge(PaymentGateway $gateway): void
    {
        // type-hint cursor lands here
    }

    public function make(): void
    {
        $gw = new PaymentGateway();
    }

    public function check(mixed $x): bool
    {
        return $x instanceof PaymentGateway;
    }
}
