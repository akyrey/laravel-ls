<?php

namespace App\Services;

use App\Contracts\Mailer;

class SmtpMailer implements Mailer
{
    public function send(string $to, string $subject, string $body): void {}
}
