<?php

namespace App\Providers;

use Illuminate\Support\ServiceProvider;
use Illuminate\Support\Facades\App;
use App\Contracts\Queue;
use App\Services\SqsQueue;

class FacadeServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        // App facade static call — Feature A, BindCall
        App::bind(Queue::class, SqsQueue::class);
    }
}
