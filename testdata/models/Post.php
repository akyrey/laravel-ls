<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;

class Post extends Model
{
    protected $appends = ['slug_url'];
    protected $hidden = ['secret_token'];

    public function setTitleAttribute(string $value): void
    {
        $this->attributes['title'] = strtolower($value);
    }

    // Untyped relationship — no return-type annotation.
    public function author()
    {
        return $this->belongsTo(User::class);
    }

    // Typed relationship with chained query builder call.
    public function primaryAuthor(): BelongsTo
    {
        return $this->belongsTo(User::class)->where('active', true);
    }
}
