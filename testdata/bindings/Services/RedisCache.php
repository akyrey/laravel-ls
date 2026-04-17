<?php

namespace App\Services;

use App\Contracts\Cache;

class RedisCache implements Cache
{
    public function __construct(private readonly mixed $redis) {}
    public function get(string $key): mixed { return null; }
    public function set(string $key, mixed $value, int $ttl = 3600): void {}
}
