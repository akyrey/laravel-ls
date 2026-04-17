<?php

namespace App\Contracts;

use App\Services\StripeGateway;

#[\Illuminate\Container\Attributes\Bind(StripeGateway::class)]
interface PaymentGateway
{
    public function charge(int $cents): void;
}
