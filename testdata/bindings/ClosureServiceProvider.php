<?php

namespace App\Providers;

use Illuminate\Support\ServiceProvider;
use App\Contracts\Cache;
use App\Services\RedisCache;
use App\Contracts\Logger;
use App\Services\FileLogger;

class ClosureServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        // Closure binding with single `new X` — concrete is RedisCache (BindClosure, resolvable)
        $this->app->bind(Cache::class, function ($app) {
            return new RedisCache($app->make('redis'));
        });

        // Arrow function binding — also BindClosure, resolvable
        $this->app->singleton(Logger::class, fn($app) => new FileLogger('/tmp/app.log'));
    }
}
