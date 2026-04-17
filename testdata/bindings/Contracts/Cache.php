<?php

namespace App\Contracts;

interface Cache
{
    public function get(string $key): mixed;
    public function set(string $key, mixed $value, int $ttl = 3600): void;
}
